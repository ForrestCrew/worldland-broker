package sessions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
)

// mockRentalSessionRepo is a mock implementation of RentalSessionRepository
type mockRentalSessionRepo struct {
	sessions map[string]*domain.RentalSession
	createFn func(ctx context.Context, session *domain.RentalSession) error
	getFn    func(ctx context.Context, id string) (*domain.RentalSession, error)
	updateFn func(ctx context.Context, session *domain.RentalSession) error
}

func newMockRentalSessionRepo() *mockRentalSessionRepo {
	return &mockRentalSessionRepo{
		sessions: make(map[string]*domain.RentalSession),
	}
}

func (m *mockRentalSessionRepo) Create(ctx context.Context, session *domain.RentalSession) error {
	if m.createFn != nil {
		return m.createFn(ctx, session)
	}
	m.sessions[session.ID] = session
	return nil
}

func (m *mockRentalSessionRepo) GetByID(ctx context.Context, id string) (*domain.RentalSession, error) {
	if m.getFn != nil {
		return m.getFn(ctx, id)
	}
	s, ok := m.sessions[id]
	if !ok {
		return nil, errors.New("session not found")
	}
	return s, nil
}

func (m *mockRentalSessionRepo) GetByRentalID(ctx context.Context, rentalID uint64) (*domain.RentalSession, error) {
	for _, s := range m.sessions {
		if s.RentalID != nil && *s.RentalID == rentalID {
			return s, nil
		}
	}
	return nil, errors.New("session not found")
}

func (m *mockRentalSessionRepo) Update(ctx context.Context, session *domain.RentalSession) error {
	if m.updateFn != nil {
		return m.updateFn(ctx, session)
	}
	m.sessions[session.ID] = session
	return nil
}

func (m *mockRentalSessionRepo) ListByUser(ctx context.Context, userAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *mockRentalSessionRepo) ListByProvider(ctx context.Context, providerAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *mockRentalSessionRepo) ListByState(ctx context.Context, state domain.RentalSessionState, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *mockRentalSessionRepo) FindStale(ctx context.Context, state domain.RentalSessionState, olderThan time.Duration) ([]*domain.RentalSession, error) {
	return nil, nil
}

// mockNodeRepo is a mock implementation of NodeRepository
type mockNodeRepo struct {
	nodes map[string]*domain.Node
}

func newMockNodeRepo() *mockNodeRepo {
	return &mockNodeRepo{
		nodes: make(map[string]*domain.Node),
	}
}

func (m *mockNodeRepo) Create(ctx context.Context, node *domain.Node) error {
	m.nodes[node.ID] = node
	return nil
}

func (m *mockNodeRepo) GetByID(ctx context.Context, id string) (*domain.Node, error) {
	n, ok := m.nodes[id]
	if !ok {
		return nil, errors.New("node not found")
	}
	return n, nil
}

func (m *mockNodeRepo) GetByProvider(ctx context.Context, providerID string) ([]*domain.Node, error) {
	return nil, nil
}

func (m *mockNodeRepo) Update(ctx context.Context, node *domain.Node) error {
	m.nodes[node.ID] = node
	return nil
}

func (m *mockNodeRepo) Delete(ctx context.Context, id string) error {
	delete(m.nodes, id)
	return nil
}

// mockProviderRepo is a mock implementation of ProviderRepository
type mockProviderRepo struct {
	providers map[string]*domain.Provider
}

func newMockProviderRepo() *mockProviderRepo {
	return &mockProviderRepo{
		providers: make(map[string]*domain.Provider),
	}
}

func (m *mockProviderRepo) Create(ctx context.Context, provider *domain.Provider) error {
	m.providers[provider.ID] = provider
	return nil
}

func (m *mockProviderRepo) GetByID(ctx context.Context, id string) (*domain.Provider, error) {
	p, ok := m.providers[id]
	if !ok {
		return nil, errors.New("provider not found")
	}
	return p, nil
}

func (m *mockProviderRepo) GetByWallet(ctx context.Context, walletAddress string) (*domain.Provider, error) {
	for _, p := range m.providers {
		if p.WalletAddress == walletAddress {
			return p, nil
		}
	}
	return nil, errors.New("provider not found")
}

func (m *mockProviderRepo) Update(ctx context.Context, provider *domain.Provider) error {
	m.providers[provider.ID] = provider
	return nil
}

// Test fixtures
func setupTestManager() (*SessionManager, *mockRentalSessionRepo, *mockNodeRepo, *mockProviderRepo) {
	sessionRepo := newMockRentalSessionRepo()
	nodeRepo := newMockNodeRepo()
	providerRepo := newMockProviderRepo()

	// Setup test node and provider
	provider := &domain.Provider{
		ID:            "provider-1",
		WalletAddress: "0x1234567890abcdef1234567890abcdef12345678",
		Status:        domain.ProviderStatusActive,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	providerRepo.providers[provider.ID] = provider

	node := &domain.Node{
		ID:             "node-1",
		ProviderID:     provider.ID,
		GPUUUID:        "GPU-12345",
		GPUType:        "NVIDIA RTX 4090",
		MemoryGB:       24,
		PricePerSecond: "1000000000000000", // 0.001 ETH
		Status:         domain.NodeStatusActive,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	nodeRepo.nodes[node.ID] = node

	manager := NewSessionManager(sessionRepo, nodeRepo, providerRepo)
	return manager, sessionRepo, nodeRepo, providerRepo
}

// createTestSession creates a session in the given state for testing transitions
func createTestSession(state domain.RentalSessionState) *domain.RentalSession {
	now := time.Now()
	session := &domain.RentalSession{
		ID:              "test-session-1",
		UserAddress:     "0xuser123",
		ProviderAddress: "0x1234567890abcdef1234567890abcdef12345678",
		NodeID:          "node-1",
		State:           state,
		PricePerSecond:  "1000000000000000",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if state == domain.RentalStateRunning || state == domain.RentalStateStopped {
		rentalID := uint64(42)
		blockNum := uint64(1000)
		session.RentalID = &rentalID
		session.BlockNumber = &blockNum
		session.StartTime = &now
	}
	if state == domain.RentalStateStopped {
		session.EndTime = &now
	}
	return session
}

// ============================================================================
// CreateSession Tests
// ============================================================================

func TestCreateSession_PendingState(t *testing.T) {
	manager, sessionRepo, _, _ := setupTestManager()
	ctx := context.Background()

	session, err := manager.CreateSession(ctx, "0xuser123", "node-1", "1000000000000000")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Session should be in PENDING state
	if session.State != domain.RentalStatePending {
		t.Errorf("Expected state PENDING, got %s", session.State)
	}

	// Session should have a UUID
	if session.ID == "" {
		t.Error("Expected session to have an ID")
	}

	// Session should have correct fields
	if session.UserAddress != "0xuser123" {
		t.Errorf("Expected user address 0xuser123, got %s", session.UserAddress)
	}

	if session.NodeID != "node-1" {
		t.Errorf("Expected node ID node-1, got %s", session.NodeID)
	}

	// Provider address should be set from node lookup
	if session.ProviderAddress != "0x1234567890abcdef1234567890abcdef12345678" {
		t.Errorf("Expected provider address from node, got %s", session.ProviderAddress)
	}

	// StartTime should be nil in PENDING state
	if session.StartTime != nil {
		t.Error("Expected StartTime to be nil in PENDING state")
	}

	// CreatedAt should be set
	if session.CreatedAt.IsZero() {
		t.Error("Expected CreatedAt to be set")
	}

	// Session should be persisted
	saved, _ := sessionRepo.GetByID(ctx, session.ID)
	if saved == nil {
		t.Error("Expected session to be persisted")
	}
}

// ============================================================================
// TransitionToRunning Tests
// ============================================================================

func TestTransitionToRunning_FromPending_Success(t *testing.T) {
	manager, sessionRepo, _, _ := setupTestManager()
	ctx := context.Background()

	// Setup: create a session in PENDING state
	session := createTestSession(domain.RentalStatePending)
	sessionRepo.sessions[session.ID] = session

	rentalID := uint64(100)
	blockNum := uint64(5000)
	txHash := "0xabc123"
	startTime := time.Now()

	err := manager.TransitionToRunning(ctx, session.ID, rentalID, blockNum, txHash, startTime)
	if err != nil {
		t.Fatalf("TransitionToRunning failed: %v", err)
	}

	// Check updated session
	updated, _ := sessionRepo.GetByID(ctx, session.ID)
	if updated.State != domain.RentalStateRunning {
		t.Errorf("Expected state RUNNING, got %s", updated.State)
	}
	if updated.RentalID == nil || *updated.RentalID != rentalID {
		t.Errorf("Expected RentalID %d, got %v", rentalID, updated.RentalID)
	}
	if updated.BlockNumber == nil || *updated.BlockNumber != blockNum {
		t.Errorf("Expected BlockNumber %d, got %v", blockNum, updated.BlockNumber)
	}
	if updated.TxHash == nil || *updated.TxHash != txHash {
		t.Errorf("Expected TxHash %s, got %v", txHash, updated.TxHash)
	}
	if updated.StartTime == nil {
		t.Error("Expected StartTime to be set")
	}
}

func TestTransitionToRunning_FromRunning_Fails(t *testing.T) {
	manager, sessionRepo, _, _ := setupTestManager()
	ctx := context.Background()

	// Setup: create a session already in RUNNING state
	session := createTestSession(domain.RentalStateRunning)
	sessionRepo.sessions[session.ID] = session

	err := manager.TransitionToRunning(ctx, session.ID, 200, 6000, "0xdef456", time.Now())
	if err == nil {
		t.Fatal("Expected error when transitioning from RUNNING to RUNNING")
	}

	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("Expected ErrInvalidTransition, got %v", err)
	}
}

func TestTransitionToRunning_FromStopped_Fails(t *testing.T) {
	manager, sessionRepo, _, _ := setupTestManager()
	ctx := context.Background()

	// Setup: create a session in STOPPED (terminal) state
	session := createTestSession(domain.RentalStateStopped)
	sessionRepo.sessions[session.ID] = session

	err := manager.TransitionToRunning(ctx, session.ID, 200, 6000, "0xdef456", time.Now())
	if err == nil {
		t.Fatal("Expected error when transitioning from STOPPED to RUNNING")
	}

	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("Expected ErrInvalidTransition, got %v", err)
	}
}

// ============================================================================
// TransitionToStopped Tests
// ============================================================================

func TestTransitionToStopped_FromRunning_Success(t *testing.T) {
	manager, sessionRepo, _, _ := setupTestManager()
	ctx := context.Background()

	// Setup: create a session in RUNNING state
	session := createTestSession(domain.RentalStateRunning)
	sessionRepo.sessions[session.ID] = session

	endTime := time.Now()
	totalCost := "5000000000000000000" // 5 ETH
	txHash := "0xstop123"
	blockNum := uint64(6000)

	err := manager.TransitionToStopped(ctx, session.ID, endTime, totalCost, txHash, blockNum)
	if err != nil {
		t.Fatalf("TransitionToStopped failed: %v", err)
	}

	// Check updated session
	updated, _ := sessionRepo.GetByID(ctx, session.ID)
	if updated.State != domain.RentalStateStopped {
		t.Errorf("Expected state STOPPED, got %s", updated.State)
	}
	if updated.EndTime == nil {
		t.Error("Expected EndTime to be set")
	}
	if updated.TxHash == nil || *updated.TxHash != txHash {
		t.Errorf("Expected TxHash %s, got %v", txHash, updated.TxHash)
	}
	if updated.BlockNumber == nil || *updated.BlockNumber != blockNum {
		t.Errorf("Expected BlockNumber %d, got %v", blockNum, updated.BlockNumber)
	}
}

func TestTransitionToStopped_FromPending_Fails(t *testing.T) {
	manager, sessionRepo, _, _ := setupTestManager()
	ctx := context.Background()

	// Setup: create a session in PENDING state (can't go directly to STOPPED)
	session := createTestSession(domain.RentalStatePending)
	sessionRepo.sessions[session.ID] = session

	err := manager.TransitionToStopped(ctx, session.ID, time.Now(), "0", "0xhash", 1000)
	if err == nil {
		t.Fatal("Expected error when transitioning from PENDING to STOPPED")
	}

	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("Expected ErrInvalidTransition, got %v", err)
	}
}

// ============================================================================
// TransitionToFailed Tests
// ============================================================================

func TestTransitionToFailed_FromPending_Success(t *testing.T) {
	manager, sessionRepo, _, _ := setupTestManager()
	ctx := context.Background()

	// Setup: create a session in PENDING state
	session := createTestSession(domain.RentalStatePending)
	sessionRepo.sessions[session.ID] = session

	reason := "Timeout: no RentalStarted within 5 minutes"

	err := manager.TransitionToFailed(ctx, session.ID, reason)
	if err != nil {
		t.Fatalf("TransitionToFailed failed: %v", err)
	}

	// Check updated session
	updated, _ := sessionRepo.GetByID(ctx, session.ID)
	if updated.State != domain.RentalStateFailed {
		t.Errorf("Expected state FAILED, got %s", updated.State)
	}
	if updated.EndTime == nil {
		t.Error("Expected EndTime to be set")
	}
}

func TestTransitionToFailed_FromRunning_Success(t *testing.T) {
	manager, sessionRepo, _, _ := setupTestManager()
	ctx := context.Background()

	// Setup: create a session in RUNNING state
	session := createTestSession(domain.RentalStateRunning)
	sessionRepo.sessions[session.ID] = session

	reason := "Node unresponsive for 30 seconds"

	err := manager.TransitionToFailed(ctx, session.ID, reason)
	if err != nil {
		t.Fatalf("TransitionToFailed failed: %v", err)
	}

	// Check updated session
	updated, _ := sessionRepo.GetByID(ctx, session.ID)
	if updated.State != domain.RentalStateFailed {
		t.Errorf("Expected state FAILED, got %s", updated.State)
	}
}

func TestTransitionToFailed_FromStopped_Fails(t *testing.T) {
	manager, sessionRepo, _, _ := setupTestManager()
	ctx := context.Background()

	// Setup: create a session in STOPPED (terminal) state
	session := createTestSession(domain.RentalStateStopped)
	sessionRepo.sessions[session.ID] = session

	err := manager.TransitionToFailed(ctx, session.ID, "some reason")
	if err == nil {
		t.Fatal("Expected error when transitioning from STOPPED to FAILED")
	}

	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("Expected ErrInvalidTransition, got %v", err)
	}
}

// ============================================================================
// TransitionToCancelled Tests
// ============================================================================

func TestTransitionToCancelled_FromPending_Success(t *testing.T) {
	manager, sessionRepo, _, _ := setupTestManager()
	ctx := context.Background()

	// Setup: create a session in PENDING state
	session := createTestSession(domain.RentalStatePending)
	sessionRepo.sessions[session.ID] = session

	err := manager.TransitionToCancelled(ctx, session.ID)
	if err != nil {
		t.Fatalf("TransitionToCancelled failed: %v", err)
	}

	// Check updated session
	updated, _ := sessionRepo.GetByID(ctx, session.ID)
	if updated.State != domain.RentalStateCancelled {
		t.Errorf("Expected state CANCELLED, got %s", updated.State)
	}
	if updated.EndTime == nil {
		t.Error("Expected EndTime to be set")
	}
}

func TestTransitionToCancelled_FromRunning_Fails(t *testing.T) {
	manager, sessionRepo, _, _ := setupTestManager()
	ctx := context.Background()

	// Setup: create a session in RUNNING state (can only cancel from PENDING)
	session := createTestSession(domain.RentalStateRunning)
	sessionRepo.sessions[session.ID] = session

	err := manager.TransitionToCancelled(ctx, session.ID)
	if err == nil {
		t.Fatal("Expected error when transitioning from RUNNING to CANCELLED")
	}

	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("Expected ErrInvalidTransition, got %v", err)
	}
}

// ============================================================================
// Additional Edge Cases
// ============================================================================

func TestTransitionFromFailed_Blocked(t *testing.T) {
	manager, sessionRepo, _, _ := setupTestManager()
	ctx := context.Background()

	// Setup: create a session in FAILED (terminal) state
	session := createTestSession(domain.RentalStatePending)
	session.State = domain.RentalStateFailed
	sessionRepo.sessions[session.ID] = session

	// Try all possible transitions - all should fail
	err := manager.TransitionToRunning(ctx, session.ID, 100, 5000, "0xhash", time.Now())
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("FAILED -> RUNNING should fail with ErrInvalidTransition, got %v", err)
	}

	err = manager.TransitionToStopped(ctx, session.ID, time.Now(), "0", "0xhash", 1000)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("FAILED -> STOPPED should fail with ErrInvalidTransition, got %v", err)
	}

	err = manager.TransitionToCancelled(ctx, session.ID)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("FAILED -> CANCELLED should fail with ErrInvalidTransition, got %v", err)
	}
}

func TestTransitionFromCancelled_Blocked(t *testing.T) {
	manager, sessionRepo, _, _ := setupTestManager()
	ctx := context.Background()

	// Setup: create a session in CANCELLED (terminal) state
	session := createTestSession(domain.RentalStatePending)
	session.State = domain.RentalStateCancelled
	sessionRepo.sessions[session.ID] = session

	// Try all possible transitions - all should fail
	err := manager.TransitionToRunning(ctx, session.ID, 100, 5000, "0xhash", time.Now())
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("CANCELLED -> RUNNING should fail with ErrInvalidTransition, got %v", err)
	}

	err = manager.TransitionToStopped(ctx, session.ID, time.Now(), "0", "0xhash", 1000)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("CANCELLED -> STOPPED should fail with ErrInvalidTransition, got %v", err)
	}

	err = manager.TransitionToFailed(ctx, session.ID, "reason")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("CANCELLED -> FAILED should fail with ErrInvalidTransition, got %v", err)
	}
}

func TestSessionNotFound(t *testing.T) {
	manager, _, _, _ := setupTestManager()
	ctx := context.Background()

	err := manager.TransitionToRunning(ctx, "nonexistent-session", 100, 5000, "0xhash", time.Now())
	if err == nil {
		t.Fatal("Expected error when session not found")
	}

	if errors.Is(err, ErrInvalidTransition) {
		t.Error("Session not found should not return ErrInvalidTransition")
	}
}
