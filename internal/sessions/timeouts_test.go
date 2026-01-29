package sessions

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/worldland/worldland-hub/internal/domain"
)

// MockStaleSessionRepo implements only FindStale for timeout tests
type MockStaleSessionRepo struct {
	mock.Mock
}

func (m *MockStaleSessionRepo) Create(ctx context.Context, session *domain.RentalSession) error {
	return nil
}

func (m *MockStaleSessionRepo) GetByID(ctx context.Context, id string) (*domain.RentalSession, error) {
	return nil, errors.New("not implemented")
}

func (m *MockStaleSessionRepo) GetByRentalID(ctx context.Context, rentalID uint64) (*domain.RentalSession, error) {
	return nil, errors.New("not implemented")
}

func (m *MockStaleSessionRepo) Update(ctx context.Context, session *domain.RentalSession) error {
	return nil
}

func (m *MockStaleSessionRepo) ListByUser(ctx context.Context, userAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *MockStaleSessionRepo) ListByProvider(ctx context.Context, providerAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *MockStaleSessionRepo) ListByState(ctx context.Context, state domain.RentalSessionState, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *MockStaleSessionRepo) FindStale(ctx context.Context, state domain.RentalSessionState, olderThan time.Duration) ([]*domain.RentalSession, error) {
	args := m.Called(ctx, state, olderThan)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.RentalSession), args.Error(1)
}

// MockSessionFailer implements SessionFailer for testing
type MockSessionFailer struct {
	mock.Mock
}

func (m *MockSessionFailer) TransitionToFailed(ctx context.Context, sessionID string, reason string) error {
	args := m.Called(ctx, sessionID, reason)
	return args.Error(0)
}

// testLogger creates a discard logger for tests
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ============================================================================
// PENDING Timeout Tests
// ============================================================================

func TestEnforcePendingTimeouts_FailsStaleSessions(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	logger := testLogger()

	// Setup: 2 stale PENDING sessions
	staleSessions := []*domain.RentalSession{
		{
			ID:        "session-1",
			State:     domain.RentalStatePending,
			CreatedAt: time.Now().Add(-10 * time.Minute),
			UpdatedAt: time.Now().Add(-10 * time.Minute),
		},
		{
			ID:        "session-2",
			State:     domain.RentalStatePending,
			CreatedAt: time.Now().Add(-6 * time.Minute),
			UpdatedAt: time.Now().Add(-6 * time.Minute),
		},
	}

	// Expect FindStale to be called for PENDING and RUNNING states
	mockRepo.On("FindStale", mock.Anything, domain.RentalStatePending, PendingTimeout).Return(staleSessions, nil)
	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).Return([]*domain.RentalSession{}, nil)

	// Expect TransitionToFailed to be called for both sessions
	mockFailer.On("TransitionToFailed", mock.Anything, "session-1", mock.AnythingOfType("string")).Return(nil)
	mockFailer.On("TransitionToFailed", mock.Anything, "session-2", mock.AnythingOfType("string")).Return(nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	assert.NoError(t, err)
	mockFailer.AssertNumberOfCalls(t, "TransitionToFailed", 2)
	mockFailer.AssertCalled(t, "TransitionToFailed", mock.Anything, "session-1", "PENDING timeout: no RentalStarted event received")
	mockFailer.AssertCalled(t, "TransitionToFailed", mock.Anything, "session-2", "PENDING timeout: no RentalStarted event received")
}

func TestEnforcePendingTimeouts_NoStaleSessions(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	logger := testLogger()

	// No stale sessions
	mockRepo.On("FindStale", mock.Anything, domain.RentalStatePending, PendingTimeout).Return([]*domain.RentalSession{}, nil)
	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).Return([]*domain.RentalSession{}, nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	assert.NoError(t, err)
	mockFailer.AssertNotCalled(t, "TransitionToFailed")
}

func TestEnforcePendingTimeouts_RepoError_ContinuesExecution(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	logger := testLogger()

	// FindStale returns error for PENDING but succeeds for RUNNING
	mockRepo.On("FindStale", mock.Anything, domain.RentalStatePending, PendingTimeout).
		Return(nil, errors.New("db connection error"))
	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).
		Return([]*domain.RentalSession{}, nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	// Should not return error - checkTimeouts always returns nil
	assert.NoError(t, err)
	// Running timeout check should still have been called
	mockRepo.AssertCalled(t, "FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout)
}

// ============================================================================
// RUNNING Timeout Tests
// ============================================================================

func TestEnforceRunningTimeouts_FailsUnresponsiveSessions(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	logger := testLogger()

	// Setup: 1 stale RUNNING session (no heartbeat for 60 seconds)
	staleSessions := []*domain.RentalSession{
		{
			ID:        "session-3",
			State:     domain.RentalStateRunning,
			CreatedAt: time.Now().Add(-5 * time.Minute),
			UpdatedAt: time.Now().Add(-60 * time.Second), // Last heartbeat 60s ago
		},
	}

	mockRepo.On("FindStale", mock.Anything, domain.RentalStatePending, PendingTimeout).Return([]*domain.RentalSession{}, nil)
	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).Return(staleSessions, nil)

	mockFailer.On("TransitionToFailed", mock.Anything, "session-3", mock.AnythingOfType("string")).Return(nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	assert.NoError(t, err)
	mockFailer.AssertCalled(t, "TransitionToFailed", mock.Anything, "session-3", "RUNNING timeout: node unresponsive")
}

func TestEnforceRunningTimeouts_MultipleStale(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	logger := testLogger()

	// Setup: 3 stale RUNNING sessions
	staleSessions := []*domain.RentalSession{
		{ID: "session-a", State: domain.RentalStateRunning, UpdatedAt: time.Now().Add(-45 * time.Second)},
		{ID: "session-b", State: domain.RentalStateRunning, UpdatedAt: time.Now().Add(-60 * time.Second)},
		{ID: "session-c", State: domain.RentalStateRunning, UpdatedAt: time.Now().Add(-2 * time.Minute)},
	}

	mockRepo.On("FindStale", mock.Anything, domain.RentalStatePending, PendingTimeout).Return([]*domain.RentalSession{}, nil)
	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).Return(staleSessions, nil)

	mockFailer.On("TransitionToFailed", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	assert.NoError(t, err)
	mockFailer.AssertNumberOfCalls(t, "TransitionToFailed", 3)
}

// ============================================================================
// Transition Error Handling Tests
// ============================================================================

func TestEnforceTimeouts_TransitionError_ContinuesToNext(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	logger := testLogger()

	// Setup: 3 stale sessions, middle one fails to transition
	staleSessions := []*domain.RentalSession{
		{ID: "session-1", State: domain.RentalStatePending, CreatedAt: time.Now().Add(-10 * time.Minute)},
		{ID: "session-2", State: domain.RentalStatePending, CreatedAt: time.Now().Add(-10 * time.Minute)},
		{ID: "session-3", State: domain.RentalStatePending, CreatedAt: time.Now().Add(-10 * time.Minute)},
	}

	mockRepo.On("FindStale", mock.Anything, domain.RentalStatePending, PendingTimeout).Return(staleSessions, nil)
	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).Return([]*domain.RentalSession{}, nil)

	// First succeeds, second fails, third succeeds
	mockFailer.On("TransitionToFailed", mock.Anything, "session-1", mock.AnythingOfType("string")).Return(nil)
	mockFailer.On("TransitionToFailed", mock.Anything, "session-2", mock.AnythingOfType("string")).Return(errors.New("transition failed"))
	mockFailer.On("TransitionToFailed", mock.Anything, "session-3", mock.AnythingOfType("string")).Return(nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	assert.NoError(t, err)
	// All three should be attempted even if one fails
	mockFailer.AssertNumberOfCalls(t, "TransitionToFailed", 3)
}

// ============================================================================
// Context Cancellation Tests
// ============================================================================

func TestTimeoutEnforcer_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	logger := testLogger()

	enforcer := NewTimeoutEnforcer(mockFailer, mockRepo, logger).
		WithCheckInterval(100 * time.Millisecond)

	done := make(chan error)
	go func() {
		done <- enforcer.Start(ctx)
	}()

	// Cancel after short delay (before first tick)
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for enforcer to stop")
	}
}

func TestTimeoutEnforcer_ContextCancellation_DuringCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	logger := testLogger()

	// Setup mock to return some sessions
	mockRepo.On("FindStale", mock.Anything, domain.RentalStatePending, PendingTimeout).
		Return([]*domain.RentalSession{}, nil)
	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).
		Return([]*domain.RentalSession{}, nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockRepo, logger).
		WithCheckInterval(50 * time.Millisecond)

	done := make(chan error)
	go func() {
		done <- enforcer.Start(ctx)
	}()

	// Wait for at least one check to complete
	time.Sleep(80 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for enforcer to stop")
	}
}

// ============================================================================
// Configuration Tests
// ============================================================================

func TestTimeoutEnforcer_WithCheckInterval(t *testing.T) {
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	logger := testLogger()

	enforcer := NewTimeoutEnforcer(mockFailer, mockRepo, logger)
	assert.Equal(t, DefaultCheckInterval, enforcer.checkInterval)

	customInterval := 10 * time.Second
	enforcer = enforcer.WithCheckInterval(customInterval)
	assert.Equal(t, customInterval, enforcer.checkInterval)
}

func TestTimeoutEnforcer_Constants(t *testing.T) {
	// Verify timeout constants match CONTEXT.md requirements
	assert.Equal(t, 5*time.Minute, PendingTimeout, "PendingTimeout should be 5 minutes per CONTEXT.md")
	assert.Equal(t, 30*time.Second, RunningTimeout, "RunningTimeout should be 30 seconds per CONTEXT.md")
	assert.Equal(t, 30*time.Second, DefaultCheckInterval, "DefaultCheckInterval should be 30 seconds")
}

// ============================================================================
// Integration-style Tests (using CheckOnce)
// ============================================================================

func TestCheckOnce_ChecksBothTimeoutTypes(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	logger := testLogger()

	// Setup: one stale PENDING and one stale RUNNING
	pendingSessions := []*domain.RentalSession{
		{ID: "pending-1", State: domain.RentalStatePending, CreatedAt: time.Now().Add(-10 * time.Minute)},
	}
	runningSessions := []*domain.RentalSession{
		{ID: "running-1", State: domain.RentalStateRunning, UpdatedAt: time.Now().Add(-60 * time.Second)},
	}

	mockRepo.On("FindStale", mock.Anything, domain.RentalStatePending, PendingTimeout).Return(pendingSessions, nil)
	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).Return(runningSessions, nil)

	mockFailer.On("TransitionToFailed", mock.Anything, "pending-1", mock.AnythingOfType("string")).Return(nil)
	mockFailer.On("TransitionToFailed", mock.Anything, "running-1", mock.AnythingOfType("string")).Return(nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	assert.NoError(t, err)
	mockFailer.AssertNumberOfCalls(t, "TransitionToFailed", 2)

	// Verify correct reasons for each type
	mockFailer.AssertCalled(t, "TransitionToFailed", mock.Anything, "pending-1", "PENDING timeout: no RentalStarted event received")
	mockFailer.AssertCalled(t, "TransitionToFailed", mock.Anything, "running-1", "RUNNING timeout: node unresponsive")
}
