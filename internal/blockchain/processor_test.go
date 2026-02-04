package blockchain

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/worldland/worldland-hub/internal/domain"
)

// MockSessionTransitioner mocks the SessionTransitioner interface for testing
type MockSessionTransitioner struct {
	mock.Mock
}

func (m *MockSessionTransitioner) TransitionToRunning(ctx context.Context, sessionID string, rentalID uint64, blockNum uint64, txHash string, startTime time.Time) error {
	args := m.Called(ctx, sessionID, rentalID, blockNum, txHash, startTime)
	return args.Error(0)
}

func (m *MockSessionTransitioner) TransitionToStopped(ctx context.Context, sessionID string, endTime time.Time, cost string, txHash string, blockNum uint64) error {
	args := m.Called(ctx, sessionID, endTime, cost, txHash, blockNum)
	return args.Error(0)
}

// MockRentalSessionRepo mocks domain.RentalSessionRepository for testing
type MockRentalSessionRepo struct {
	mock.Mock
}

func (m *MockRentalSessionRepo) Create(ctx context.Context, session *domain.RentalSession) error {
	args := m.Called(ctx, session)
	return args.Error(0)
}

func (m *MockRentalSessionRepo) GetByID(ctx context.Context, id string) (*domain.RentalSession, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.RentalSession), args.Error(1)
}

func (m *MockRentalSessionRepo) GetByRentalID(ctx context.Context, rentalID uint64) (*domain.RentalSession, error) {
	args := m.Called(ctx, rentalID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.RentalSession), args.Error(1)
}

func (m *MockRentalSessionRepo) Update(ctx context.Context, session *domain.RentalSession) error {
	args := m.Called(ctx, session)
	return args.Error(0)
}

func (m *MockRentalSessionRepo) TouchSession(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}

func (m *MockRentalSessionRepo) ListByUser(ctx context.Context, userAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	args := m.Called(ctx, userAddress, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.RentalSession), args.Error(1)
}

func (m *MockRentalSessionRepo) ListByProvider(ctx context.Context, providerAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	args := m.Called(ctx, providerAddress, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.RentalSession), args.Error(1)
}

func (m *MockRentalSessionRepo) ListByState(ctx context.Context, state domain.RentalSessionState, limit, offset int) ([]*domain.RentalSession, error) {
	args := m.Called(ctx, state, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.RentalSession), args.Error(1)
}

func (m *MockRentalSessionRepo) FindStale(ctx context.Context, state domain.RentalSessionState, olderThan time.Duration) ([]*domain.RentalSession, error) {
	args := m.Called(ctx, state, olderThan)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.RentalSession), args.Error(1)
}

func (m *MockRentalSessionRepo) FindByUserAndState(ctx context.Context, userAddress string, state domain.RentalSessionState) ([]*domain.RentalSession, error) {
	args := m.Called(ctx, userAddress, state)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.RentalSession), args.Error(1)
}

func (m *MockRentalSessionRepo) FindPendingSettlement(ctx context.Context, userAddress string) ([]*domain.RentalSession, error) {
	args := m.Called(ctx, userAddress)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.RentalSession), args.Error(1)
}

func (m *MockRentalSessionRepo) FindAllPendingSettlement(ctx context.Context) ([]*domain.RentalSession, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.RentalSession), args.Error(1)
}

func (m *MockRentalSessionRepo) UpdateSettlement(ctx context.Context, sessionID, amount string, settledAt time.Time) error {
	args := m.Called(ctx, sessionID, amount, settledAt)
	return args.Error(0)
}

func (m *MockRentalSessionRepo) GetByTxHash(ctx context.Context, txHash string) (*domain.RentalSession, error) {
	args := m.Called(ctx, txHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.RentalSession), args.Error(1)
}

func (m *MockRentalSessionRepo) SetTxHash(ctx context.Context, sessionID, txHash string) error {
	args := m.Called(ctx, sessionID, txHash)
	return args.Error(0)
}

func (m *MockRentalSessionRepo) SoftDeletePendingBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	args := m.Called(ctx, cutoff)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockRentalSessionRepo) ListPendingWithTxHash(ctx context.Context) ([]*domain.RentalSession, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.RentalSession), args.Error(1)
}

func (m *MockRentalSessionRepo) FindExpiringSessions(ctx context.Context, cutoff time.Time) ([]*domain.RentalSession, error) {
	args := m.Called(ctx, cutoff)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.RentalSession), args.Error(1)
}

func (m *MockRentalSessionRepo) UpdateExtension(ctx context.Context, sessionID string, extendedUntil time.Time, extensionMinutes int) error {
	args := m.Called(ctx, sessionID, extendedUntil, extensionMinutes)
	return args.Error(0)
}

func (m *MockRentalSessionRepo) CreateExtensionRecord(ctx context.Context, sessionID string, extensionMinutes int, costEstimate, idempotencyKey string) (string, error) {
	args := m.Called(ctx, sessionID, extensionMinutes, costEstimate, idempotencyKey)
	return args.String(0), args.Error(1)
}

// Test fixtures
var (
	testUserAddr     = common.HexToAddress("0x1234567890123456789012345678901234567890")
	testProviderAddr = common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd")
	testTxHash       = common.HexToHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ============================================================================
// HandleRentalStarted Tests
// ============================================================================

func TestHandleRentalStarted_TransitionsPendingToRunning(t *testing.T) {
	ctx := context.Background()
	mockManager := new(MockSessionTransitioner)
	mockRepo := new(MockRentalSessionRepo)
	logger := testLogger()

	processor := NewEventProcessor(mockManager, mockRepo, logger)

	// Create a pending session
	pendingSession := &domain.RentalSession{
		ID:              "session-123",
		UserAddress:     testUserAddr.Hex(),
		ProviderAddress: testProviderAddr.Hex(),
		State:           domain.RentalStatePending,
	}

	// Setup mock expectations
	mockRepo.On("ListByUser", ctx, testUserAddr.Hex(), 10, 0).Return([]*domain.RentalSession{pendingSession}, nil)
	mockManager.On("TransitionToRunning", ctx, "session-123", uint64(42), uint64(12345), testTxHash.Hex(), mock.AnythingOfType("time.Time")).Return(nil)

	event := &RentalStartedEvent{
		RentalID:    42,
		User:        testUserAddr,
		Provider:    testProviderAddr,
		StartTime:   1706500000,
		BlockNumber: 12345,
		TxHash:      testTxHash,
	}

	err := processor.HandleRentalStarted(ctx, event)
	require.NoError(t, err)

	mockRepo.AssertExpectations(t)
	mockManager.AssertExpectations(t)
}

func TestHandleRentalStarted_NoPendingSession_SkipsGracefully(t *testing.T) {
	ctx := context.Background()
	mockManager := new(MockSessionTransitioner)
	mockRepo := new(MockRentalSessionRepo)
	logger := testLogger()

	processor := NewEventProcessor(mockManager, mockRepo, logger)

	// Return empty list - no pending sessions
	mockRepo.On("ListByUser", ctx, testUserAddr.Hex(), 10, 0).Return([]*domain.RentalSession{}, nil)

	event := &RentalStartedEvent{
		RentalID:    42,
		User:        testUserAddr,
		Provider:    testProviderAddr,
		StartTime:   1706500000,
		BlockNumber: 12345,
		TxHash:      testTxHash,
	}

	// HandleRentalStarted should return nil (no error) even when no session found
	err := processor.HandleRentalStarted(ctx, event)
	assert.NoError(t, err)

	mockRepo.AssertExpectations(t)
	// TransitionToRunning should NOT be called
	mockManager.AssertNotCalled(t, "TransitionToRunning")
}

func TestHandleRentalStarted_WrongProvider_SkipsGracefully(t *testing.T) {
	ctx := context.Background()
	mockManager := new(MockSessionTransitioner)
	mockRepo := new(MockRentalSessionRepo)
	logger := testLogger()

	processor := NewEventProcessor(mockManager, mockRepo, logger)

	// Create a pending session with different provider
	differentProvider := common.HexToAddress("0x9999999999999999999999999999999999999999")
	pendingSession := &domain.RentalSession{
		ID:              "session-123",
		UserAddress:     testUserAddr.Hex(),
		ProviderAddress: differentProvider.Hex(), // Different provider
		State:           domain.RentalStatePending,
	}

	mockRepo.On("ListByUser", ctx, testUserAddr.Hex(), 10, 0).Return([]*domain.RentalSession{pendingSession}, nil)

	event := &RentalStartedEvent{
		RentalID:    42,
		User:        testUserAddr,
		Provider:    testProviderAddr, // Provider doesn't match session
		StartTime:   1706500000,
		BlockNumber: 12345,
		TxHash:      testTxHash,
	}

	// Should return nil (skip gracefully) since no matching session found
	err := processor.HandleRentalStarted(ctx, event)
	assert.NoError(t, err)

	mockRepo.AssertExpectations(t)
	mockManager.AssertNotCalled(t, "TransitionToRunning")
}

func TestHandleRentalStarted_SessionNotPending_SkipsGracefully(t *testing.T) {
	ctx := context.Background()
	mockManager := new(MockSessionTransitioner)
	mockRepo := new(MockRentalSessionRepo)
	logger := testLogger()

	processor := NewEventProcessor(mockManager, mockRepo, logger)

	// Create a session that's already RUNNING (not PENDING)
	runningSession := &domain.RentalSession{
		ID:              "session-123",
		UserAddress:     testUserAddr.Hex(),
		ProviderAddress: testProviderAddr.Hex(),
		State:           domain.RentalStateRunning, // Already running
	}

	mockRepo.On("ListByUser", ctx, testUserAddr.Hex(), 10, 0).Return([]*domain.RentalSession{runningSession}, nil)

	event := &RentalStartedEvent{
		RentalID:    42,
		User:        testUserAddr,
		Provider:    testProviderAddr,
		StartTime:   1706500000,
		BlockNumber: 12345,
		TxHash:      testTxHash,
	}

	// Should skip since session is not in PENDING state
	err := processor.HandleRentalStarted(ctx, event)
	assert.NoError(t, err)

	mockRepo.AssertExpectations(t)
	mockManager.AssertNotCalled(t, "TransitionToRunning")
}

func TestHandleRentalStarted_TransitionError_ReturnsError(t *testing.T) {
	ctx := context.Background()
	mockManager := new(MockSessionTransitioner)
	mockRepo := new(MockRentalSessionRepo)
	logger := testLogger()

	processor := NewEventProcessor(mockManager, mockRepo, logger)

	pendingSession := &domain.RentalSession{
		ID:              "session-123",
		UserAddress:     testUserAddr.Hex(),
		ProviderAddress: testProviderAddr.Hex(),
		State:           domain.RentalStatePending,
	}

	mockRepo.On("ListByUser", ctx, testUserAddr.Hex(), 10, 0).Return([]*domain.RentalSession{pendingSession}, nil)
	mockManager.On("TransitionToRunning", ctx, "session-123", uint64(42), uint64(12345), testTxHash.Hex(), mock.AnythingOfType("time.Time")).Return(errors.New("transition failed"))

	event := &RentalStartedEvent{
		RentalID:    42,
		User:        testUserAddr,
		Provider:    testProviderAddr,
		StartTime:   1706500000,
		BlockNumber: 12345,
		TxHash:      testTxHash,
	}

	err := processor.HandleRentalStarted(ctx, event)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "transition to running")

	mockRepo.AssertExpectations(t)
	mockManager.AssertExpectations(t)
}

// ============================================================================
// HandleRentalStopped Tests
// ============================================================================

func TestHandleRentalStopped_TransitionsRunningToStopped(t *testing.T) {
	ctx := context.Background()
	mockManager := new(MockSessionTransitioner)
	mockRepo := new(MockRentalSessionRepo)
	logger := testLogger()

	processor := NewEventProcessor(mockManager, mockRepo, logger)

	rentalID := uint64(42)
	runningSession := &domain.RentalSession{
		ID:              "session-123",
		UserAddress:     testUserAddr.Hex(),
		ProviderAddress: testProviderAddr.Hex(),
		RentalID:        &rentalID,
		State:           domain.RentalStateRunning,
	}

	mockRepo.On("GetByRentalID", ctx, uint64(42)).Return(runningSession, nil)
	mockManager.On("TransitionToStopped", ctx, "session-123", mock.AnythingOfType("time.Time"), "5000000000000000000", testTxHash.Hex(), uint64(12400)).Return(nil)

	event := &RentalStoppedEvent{
		RentalID:    42,
		EndTime:     1706510000,
		Cost:        big.NewInt(5000000000000000000), // 5 ETH
		TxHash:      testTxHash,
		BlockNumber: 12400,
	}

	err := processor.HandleRentalStopped(ctx, event)
	require.NoError(t, err)

	mockRepo.AssertExpectations(t)
	mockManager.AssertExpectations(t)
}

func TestHandleRentalStopped_UnknownRentalID_SkipsGracefully(t *testing.T) {
	ctx := context.Background()
	mockManager := new(MockSessionTransitioner)
	mockRepo := new(MockRentalSessionRepo)
	logger := testLogger()

	processor := NewEventProcessor(mockManager, mockRepo, logger)

	// Return error - rental not found
	mockRepo.On("GetByRentalID", ctx, uint64(999)).Return(nil, errors.New("session not found"))

	event := &RentalStoppedEvent{
		RentalID:    999, // Unknown rental
		EndTime:     1706510000,
		Cost:        big.NewInt(1000000000000000000),
		TxHash:      testTxHash,
		BlockNumber: 12400,
	}

	// Should return nil (skip gracefully)
	err := processor.HandleRentalStopped(ctx, event)
	assert.NoError(t, err)

	mockRepo.AssertExpectations(t)
	mockManager.AssertNotCalled(t, "TransitionToStopped")
}

func TestHandleRentalStopped_TransitionError_ReturnsError(t *testing.T) {
	ctx := context.Background()
	mockManager := new(MockSessionTransitioner)
	mockRepo := new(MockRentalSessionRepo)
	logger := testLogger()

	processor := NewEventProcessor(mockManager, mockRepo, logger)

	rentalID := uint64(42)
	runningSession := &domain.RentalSession{
		ID:              "session-123",
		UserAddress:     testUserAddr.Hex(),
		ProviderAddress: testProviderAddr.Hex(),
		RentalID:        &rentalID,
		State:           domain.RentalStateRunning,
	}

	mockRepo.On("GetByRentalID", ctx, uint64(42)).Return(runningSession, nil)
	mockManager.On("TransitionToStopped", ctx, "session-123", mock.AnythingOfType("time.Time"), "5000000000000000000", testTxHash.Hex(), uint64(12400)).Return(errors.New("transition failed"))

	event := &RentalStoppedEvent{
		RentalID:    42,
		EndTime:     1706510000,
		Cost:        big.NewInt(5000000000000000000),
		TxHash:      testTxHash,
		BlockNumber: 12400,
	}

	err := processor.HandleRentalStopped(ctx, event)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "transition to stopped")

	mockRepo.AssertExpectations(t)
	mockManager.AssertExpectations(t)
}

// ============================================================================
// EventProcessor Interface Verification
// ============================================================================

func TestEventProcessor_ImplementsEventHandler(t *testing.T) {
	// This is a compile-time check, but we can test it explicitly
	var handler EventHandler = &EventProcessor{}
	assert.NotNil(t, handler)
}

// ============================================================================
// FindPendingSession Tests
// ============================================================================

func TestFindPendingSession_ReturnsMatchingSession(t *testing.T) {
	ctx := context.Background()
	mockManager := new(MockSessionTransitioner)
	mockRepo := new(MockRentalSessionRepo)
	logger := testLogger()

	processor := NewEventProcessor(mockManager, mockRepo, logger)

	pendingSession := &domain.RentalSession{
		ID:              "session-123",
		UserAddress:     testUserAddr.Hex(),
		ProviderAddress: testProviderAddr.Hex(),
		State:           domain.RentalStatePending,
	}

	mockRepo.On("ListByUser", ctx, testUserAddr.Hex(), 10, 0).Return([]*domain.RentalSession{pendingSession}, nil)

	session, err := processor.findPendingSession(ctx, testUserAddr.Hex(), testProviderAddr.Hex())
	require.NoError(t, err)
	assert.Equal(t, "session-123", session.ID)

	mockRepo.AssertExpectations(t)
}

func TestFindPendingSession_MultipleSessionsReturnsCorrectOne(t *testing.T) {
	ctx := context.Background()
	mockManager := new(MockSessionTransitioner)
	mockRepo := new(MockRentalSessionRepo)
	logger := testLogger()

	processor := NewEventProcessor(mockManager, mockRepo, logger)

	otherProvider := common.HexToAddress("0x8888888888888888888888888888888888888888")

	sessions := []*domain.RentalSession{
		{
			ID:              "session-wrong-provider",
			UserAddress:     testUserAddr.Hex(),
			ProviderAddress: otherProvider.Hex(),
			State:           domain.RentalStatePending,
		},
		{
			ID:              "session-running",
			UserAddress:     testUserAddr.Hex(),
			ProviderAddress: testProviderAddr.Hex(),
			State:           domain.RentalStateRunning, // Not pending
		},
		{
			ID:              "session-correct",
			UserAddress:     testUserAddr.Hex(),
			ProviderAddress: testProviderAddr.Hex(),
			State:           domain.RentalStatePending,
		},
	}

	mockRepo.On("ListByUser", ctx, testUserAddr.Hex(), 10, 0).Return(sessions, nil)

	session, err := processor.findPendingSession(ctx, testUserAddr.Hex(), testProviderAddr.Hex())
	require.NoError(t, err)
	assert.Equal(t, "session-correct", session.ID)

	mockRepo.AssertExpectations(t)
}

func TestFindPendingSession_NoMatchReturnsError(t *testing.T) {
	ctx := context.Background()
	mockManager := new(MockSessionTransitioner)
	mockRepo := new(MockRentalSessionRepo)
	logger := testLogger()

	processor := NewEventProcessor(mockManager, mockRepo, logger)

	mockRepo.On("ListByUser", ctx, testUserAddr.Hex(), 10, 0).Return([]*domain.RentalSession{}, nil)

	session, err := processor.findPendingSession(ctx, testUserAddr.Hex(), testProviderAddr.Hex())
	assert.Nil(t, session)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no pending session found")

	mockRepo.AssertExpectations(t)
}

func TestFindPendingSession_RepoErrorPropagates(t *testing.T) {
	ctx := context.Background()
	mockManager := new(MockSessionTransitioner)
	mockRepo := new(MockRentalSessionRepo)
	logger := testLogger()

	processor := NewEventProcessor(mockManager, mockRepo, logger)

	mockRepo.On("ListByUser", ctx, testUserAddr.Hex(), 10, 0).Return(nil, errors.New("database error"))

	session, err := processor.findPendingSession(ctx, testUserAddr.Hex(), testProviderAddr.Hex())
	assert.Nil(t, session)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database error")

	mockRepo.AssertExpectations(t)
}
