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
	"github.com/stretchr/testify/require"

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

func (m *MockStaleSessionRepo) FindByUserAndState(ctx context.Context, userAddress string, state domain.RentalSessionState) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *MockStaleSessionRepo) FindPendingSettlement(ctx context.Context, userAddress string) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *MockStaleSessionRepo) FindAllPendingSettlement(ctx context.Context) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *MockStaleSessionRepo) UpdateSettlement(ctx context.Context, sessionID, amount string, settledAt time.Time) error {
	return nil
}

func (m *MockStaleSessionRepo) GetByTxHash(ctx context.Context, txHash string) (*domain.RentalSession, error) {
	return nil, errors.New("not implemented")
}

func (m *MockStaleSessionRepo) SetTxHash(ctx context.Context, sessionID, txHash string) error {
	return nil
}

func (m *MockStaleSessionRepo) SoftDeletePendingBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	args := m.Called(ctx, cutoff)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockStaleSessionRepo) ListPendingWithTxHash(ctx context.Context) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *MockStaleSessionRepo) FindExpiringSessions(ctx context.Context, cutoff time.Time) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *MockStaleSessionRepo) UpdateExtension(ctx context.Context, sessionID string, extendedUntil time.Time, extensionMinutes int) error {
	return nil
}

func (m *MockStaleSessionRepo) CreateExtensionRecord(ctx context.Context, sessionID string, extensionMinutes int, costEstimate, idempotencyKey string) (string, error) {
	return "", nil
}

// MockSessionFailer implements SessionFailer for testing
type MockSessionFailer struct {
	mock.Mock
}

func (m *MockSessionFailer) TransitionToFailed(ctx context.Context, sessionID string, reason string) error {
	args := m.Called(ctx, sessionID, reason)
	return args.Error(0)
}

// MockSoftDeleter implements SoftDeleter for isolated testing
type MockSoftDeleter struct {
	DeleteCount int64
	CallCount   int
	LastCutoff  time.Time
	Err         error
}

func (m *MockSoftDeleter) SoftDeletePendingBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	m.CallCount++
	m.LastCutoff = cutoff
	return m.DeleteCount, m.Err
}

// testLogger creates a discard logger for tests
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ============================================================================
// Constants Tests (Phase 14 requirements)
// ============================================================================

func TestTimeoutEnforcer_Constants(t *testing.T) {
	// Verify timeout constants match Phase 14 CONTEXT.md requirements
	assert.Equal(t, 10*time.Minute, PendingTimeout, "PendingTimeout should be 10 minutes per Phase 14")
	assert.Equal(t, 30*time.Second, RunningTimeout, "RunningTimeout should be 30 seconds per CONTEXT.md")
	assert.Equal(t, 1*time.Minute, DefaultCheckInterval, "DefaultCheckInterval should be 1 minute per Phase 14")
}

// ============================================================================
// PENDING Timeout Tests (Soft Delete - Phase 14)
// ============================================================================

func TestEnforcePendingTimeouts_SoftDeletes(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	mockDeleter := &MockSoftDeleter{
		DeleteCount: 2, // Simulates 2 sessions soft deleted
	}
	logger := testLogger()

	// RUNNING timeouts still use FindStale
	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).
		Return([]*domain.RentalSession{}, nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockDeleter, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	require.NoError(t, err)
	assert.Equal(t, 1, mockDeleter.CallCount)
	// Verify cutoff was 10 minutes ago
	assert.WithinDuration(t,
		time.Now().Add(-10*time.Minute),
		mockDeleter.LastCutoff,
		5*time.Second,
	)
	// TransitionToFailed should NOT be called for PENDING sessions
	mockFailer.AssertNotCalled(t, "TransitionToFailed", mock.Anything, mock.Anything, "PENDING timeout: no RentalStarted event received")
}

func TestEnforcePendingTimeouts_SoftDelete_ExcludesConfirming(t *testing.T) {
	// This test verifies the contract: SoftDeletePendingBefore is called
	// The actual "excludes sessions with tx_hash" is tested in repository tests
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	mockDeleter := &MockSoftDeleter{
		DeleteCount: 5, // Simulates 5 sessions soft deleted
	}
	logger := testLogger()

	// RUNNING timeouts
	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).
		Return([]*domain.RentalSession{}, nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockDeleter, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	require.NoError(t, err)
	assert.Equal(t, 1, mockDeleter.CallCount)
	// The cutoff should be 10 minutes ago (PendingTimeout)
	expectedCutoff := time.Now().Add(-PendingTimeout)
	assert.WithinDuration(t, expectedCutoff, mockDeleter.LastCutoff, 5*time.Second)
}

func TestEnforcePendingTimeouts_NoSessions(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	mockDeleter := &MockSoftDeleter{
		DeleteCount: 0, // No sessions to delete
	}
	logger := testLogger()

	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).
		Return([]*domain.RentalSession{}, nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockDeleter, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	require.NoError(t, err)
	assert.Equal(t, 1, mockDeleter.CallCount) // Still called, just returns 0
	mockFailer.AssertNotCalled(t, "TransitionToFailed")
}

func TestEnforcePendingTimeouts_SoftDeleteError_ContinuesExecution(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	mockDeleter := &MockSoftDeleter{
		Err: errors.New("db connection error"),
	}
	logger := testLogger()

	// Even if soft delete fails, RUNNING timeout check should still happen
	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).
		Return([]*domain.RentalSession{}, nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockDeleter, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	// Should not return error - checkTimeouts always returns nil
	require.NoError(t, err)
	// Running timeout check should still have been called
	mockRepo.AssertCalled(t, "FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout)
}

// ============================================================================
// RUNNING Timeout Tests (unchanged behavior - still uses TransitionToFailed)
// ============================================================================

func TestEnforceRunningTimeouts_FailsUnresponsiveSessions(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	mockDeleter := &MockSoftDeleter{DeleteCount: 0}
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

	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).Return(staleSessions, nil)

	mockFailer.On("TransitionToFailed", mock.Anything, "session-3", mock.AnythingOfType("string")).Return(nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockDeleter, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	assert.NoError(t, err)
	mockFailer.AssertCalled(t, "TransitionToFailed", mock.Anything, "session-3", "RUNNING timeout: node unresponsive")
}

func TestEnforceRunningTimeouts_MultipleStale(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	mockDeleter := &MockSoftDeleter{DeleteCount: 0}
	logger := testLogger()

	// Setup: 3 stale RUNNING sessions
	staleSessions := []*domain.RentalSession{
		{ID: "session-a", State: domain.RentalStateRunning, UpdatedAt: time.Now().Add(-45 * time.Second)},
		{ID: "session-b", State: domain.RentalStateRunning, UpdatedAt: time.Now().Add(-60 * time.Second)},
		{ID: "session-c", State: domain.RentalStateRunning, UpdatedAt: time.Now().Add(-2 * time.Minute)},
	}

	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).Return(staleSessions, nil)

	mockFailer.On("TransitionToFailed", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockDeleter, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	assert.NoError(t, err)
	mockFailer.AssertNumberOfCalls(t, "TransitionToFailed", 3)
}

func TestEnforceRunningTimeouts_TransitionError_ContinuesToNext(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	mockDeleter := &MockSoftDeleter{DeleteCount: 0}
	logger := testLogger()

	// Setup: 3 stale RUNNING sessions, middle one fails to transition
	staleSessions := []*domain.RentalSession{
		{ID: "session-1", State: domain.RentalStateRunning, UpdatedAt: time.Now().Add(-60 * time.Second)},
		{ID: "session-2", State: domain.RentalStateRunning, UpdatedAt: time.Now().Add(-60 * time.Second)},
		{ID: "session-3", State: domain.RentalStateRunning, UpdatedAt: time.Now().Add(-60 * time.Second)},
	}

	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).Return(staleSessions, nil)

	// First succeeds, second fails, third succeeds
	mockFailer.On("TransitionToFailed", mock.Anything, "session-1", mock.AnythingOfType("string")).Return(nil)
	mockFailer.On("TransitionToFailed", mock.Anything, "session-2", mock.AnythingOfType("string")).Return(errors.New("transition failed"))
	mockFailer.On("TransitionToFailed", mock.Anything, "session-3", mock.AnythingOfType("string")).Return(nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockDeleter, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	assert.NoError(t, err)
	// All three should be attempted even if one fails
	mockFailer.AssertNumberOfCalls(t, "TransitionToFailed", 3)
}

func TestEnforceRunningTimeouts_RepoError(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	mockDeleter := &MockSoftDeleter{DeleteCount: 0}
	logger := testLogger()

	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).
		Return(nil, errors.New("db error"))

	enforcer := NewTimeoutEnforcer(mockFailer, mockDeleter, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	// Should not return error - checkTimeouts always returns nil
	assert.NoError(t, err)
	mockFailer.AssertNotCalled(t, "TransitionToFailed")
}

// ============================================================================
// Context Cancellation Tests
// ============================================================================

func TestTimeoutEnforcer_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	mockDeleter := &MockSoftDeleter{DeleteCount: 0}
	logger := testLogger()

	enforcer := NewTimeoutEnforcer(mockFailer, mockDeleter, mockRepo, logger).
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
	mockDeleter := &MockSoftDeleter{DeleteCount: 0}
	logger := testLogger()

	// Setup mock for RUNNING check
	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).
		Return([]*domain.RentalSession{}, nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockDeleter, mockRepo, logger).
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
	mockDeleter := &MockSoftDeleter{DeleteCount: 0}
	logger := testLogger()

	enforcer := NewTimeoutEnforcer(mockFailer, mockDeleter, mockRepo, logger)
	assert.Equal(t, DefaultCheckInterval, enforcer.checkInterval)

	customInterval := 10 * time.Second
	enforcer = enforcer.WithCheckInterval(customInterval)
	assert.Equal(t, customInterval, enforcer.checkInterval)
}

// ============================================================================
// Integration-style Tests (using CheckOnce)
// ============================================================================

func TestCheckOnce_ChecksBothTimeoutTypes(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockStaleSessionRepo)
	mockFailer := new(MockSessionFailer)
	mockDeleter := &MockSoftDeleter{
		DeleteCount: 1, // 1 PENDING session soft deleted
	}
	logger := testLogger()

	// Setup: one stale RUNNING (soft deleter handles PENDING now)
	runningSessions := []*domain.RentalSession{
		{ID: "running-1", State: domain.RentalStateRunning, UpdatedAt: time.Now().Add(-60 * time.Second)},
	}

	mockRepo.On("FindStale", mock.Anything, domain.RentalStateRunning, RunningTimeout).Return(runningSessions, nil)

	mockFailer.On("TransitionToFailed", mock.Anything, "running-1", mock.AnythingOfType("string")).Return(nil)

	enforcer := NewTimeoutEnforcer(mockFailer, mockDeleter, mockRepo, logger)
	err := enforcer.CheckOnce(ctx)

	assert.NoError(t, err)

	// Verify soft delete was called for PENDING
	assert.Equal(t, 1, mockDeleter.CallCount)

	// Verify TransitionToFailed was called for RUNNING
	mockFailer.AssertNumberOfCalls(t, "TransitionToFailed", 1)
	mockFailer.AssertCalled(t, "TransitionToFailed", mock.Anything, "running-1", "RUNNING timeout: node unresponsive")
}

// ============================================================================
// TODO: Integration test concept for repository level
// ============================================================================
// This is documented for future implementation in test/integration/ttl_cleanup_test.go
//
// Integration test should verify:
// 1. Create PENDING session without tx_hash, set created_at > 10 minutes ago
// 2. Create PENDING session with tx_hash, set created_at > 10 minutes ago
// 3. Run SoftDeletePendingBefore cleanup
// 4. Verify: first session has deleted_at set, second session preserved
//
// This test ensures the repository correctly protects sessions with tx_hash
// from TTL cleanup (RESEARCH.md Pitfall #3)
