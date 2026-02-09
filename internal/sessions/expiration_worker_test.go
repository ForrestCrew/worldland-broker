package sessions

import (
	"context"
	"testing"
	"time"

	"log/slog"
	"os"

	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/rental"
)

func TestExpirationWorker_ProcessesExpiredSessions(t *testing.T) {
	// Setup
	now := time.Now()
	expiredSession := &domain.RentalSession{
		ID:              "expired-session-1",
		State:           domain.RentalStateRunning,
		ExtendedUntil:   timePtr(now.Add(-5 * time.Minute)),
		NodeID:          "node-1",
		UserAddress:     "0xuser123",
		ProviderAddress: "0xprovider456",
	}

	// Use simple in-memory mock
	mockRepo := &SimpleSessionRepo{
		sessions: map[string]*domain.RentalSession{
			"expired-session-1": expiredSession,
		},
		expiring: []*domain.RentalSession{expiredSession},
	}
	mockNodeRepo := &SimpleNodeRepo{
		nodes: map[string]*domain.Node{
			"node-1": {ID: "node-1", APIEndpoint: "http://node:8080"},
		},
	}
	mockNodeClient := &SimpleNodeClient{}
	mockProviderRepo := &SimpleProviderRepo{}
	manager := NewSessionManager(mockRepo, mockNodeRepo, mockProviderRepo)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	worker := NewExpirationWorker(mockRepo, manager, mockNodeClient, mockNodeRepo, logger)
	worker.ProcessOnce(context.Background())

	// Verify session was stopped (state should be STOPPED)
	session := mockRepo.sessions["expired-session-1"]
	if session.State != domain.RentalStateStopped {
		t.Errorf("Expected session state STOPPED, got %s", session.State)
	}
}

func TestExpirationWorker_SkipsNonExpiredSessions(t *testing.T) {
	// Sessions with future extended_until should not be returned by FindExpiringSessions
	mockRepo := &SimpleSessionRepo{
		sessions: make(map[string]*domain.RentalSession),
		expiring: []*domain.RentalSession{}, // No expired sessions
	}
	mockNodeRepo := &SimpleNodeRepo{nodes: make(map[string]*domain.Node)}
	mockProviderRepo := &SimpleProviderRepo{}
	manager := NewSessionManager(mockRepo, mockNodeRepo, mockProviderRepo)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	worker := NewExpirationWorker(mockRepo, manager, nil, nil, logger)
	worker.ProcessOnce(context.Background())

	// Verify no sessions were modified
	if len(mockRepo.sessions) > 0 {
		t.Error("Should not modify any sessions when none expired")
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}

// SimpleSessionRepo - minimal in-memory repo for testing
type SimpleSessionRepo struct {
	sessions map[string]*domain.RentalSession
	expiring []*domain.RentalSession
}

func (s *SimpleSessionRepo) GetByID(ctx context.Context, id string) (*domain.RentalSession, error) {
	session, ok := s.sessions[id]
	if !ok {
		return nil, nil
	}
	return session, nil
}

func (s *SimpleSessionRepo) Create(ctx context.Context, session *domain.RentalSession) error {
	s.sessions[session.ID] = session
	return nil
}

func (s *SimpleSessionRepo) Update(ctx context.Context, session *domain.RentalSession) error {
	s.sessions[session.ID] = session
	return nil
}

func (s *SimpleSessionRepo) TouchSession(ctx context.Context, sessionID string) error {
	if session, ok := s.sessions[sessionID]; ok {
		session.UpdatedAt = time.Now()
	}
	return nil
}

func (s *SimpleSessionRepo) ListByUser(ctx context.Context, userAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (s *SimpleSessionRepo) ListByProvider(ctx context.Context, providerAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (s *SimpleSessionRepo) ListByState(ctx context.Context, state domain.RentalSessionState, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (s *SimpleSessionRepo) FindStale(ctx context.Context, state domain.RentalSessionState, olderThan time.Duration) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (s *SimpleSessionRepo) FindByUserAndState(ctx context.Context, userAddress string, state domain.RentalSessionState) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (s *SimpleSessionRepo) FindPendingSettlement(ctx context.Context, userAddress string) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (s *SimpleSessionRepo) FindAllPendingSettlement(ctx context.Context) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (s *SimpleSessionRepo) UpdateSettlement(ctx context.Context, sessionID, amount string, settledAt time.Time) error {
	return nil
}

func (s *SimpleSessionRepo) GetByTxHash(ctx context.Context, txHash string) (*domain.RentalSession, error) {
	return nil, nil
}

func (s *SimpleSessionRepo) GetByRentalID(ctx context.Context, rentalID uint64) (*domain.RentalSession, error) {
	return nil, nil
}

func (s *SimpleSessionRepo) SetTxHash(ctx context.Context, sessionID, txHash string) error {
	return nil
}

func (s *SimpleSessionRepo) SoftDeletePendingBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	return 0, nil
}

func (s *SimpleSessionRepo) ListPendingWithTxHash(ctx context.Context) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (s *SimpleSessionRepo) FindExpiringSessions(ctx context.Context, cutoff time.Time) ([]*domain.RentalSession, error) {
	return s.expiring, nil
}

func (s *SimpleSessionRepo) UpdateExtension(ctx context.Context, sessionID string, extendedUntil time.Time, extensionMinutes int) error {
	return nil
}

func (s *SimpleSessionRepo) CreateExtensionRecord(ctx context.Context, sessionID string, extensionMinutes int, costEstimate, idempotencyKey string) (string, error) {
	return "", nil
}

func (s *SimpleSessionRepo) UpdateSSHInfo(ctx context.Context, sessionID, host string, port int32, user, password string) error {
	return nil
}

func (s *SimpleSessionRepo) LoadRunningSSHInfo(ctx context.Context) (map[string]*domain.SSHConnectionInfo, error) {
	return nil, nil
}

// SimpleNodeRepo - minimal in-memory node repo
type SimpleNodeRepo struct {
	nodes map[string]*domain.Node
}

func (s *SimpleNodeRepo) GetByID(ctx context.Context, id string) (*domain.Node, error) {
	node, ok := s.nodes[id]
	if !ok {
		return nil, nil
	}
	return node, nil
}

func (s *SimpleNodeRepo) Create(ctx context.Context, node *domain.Node) error {
	return nil
}

func (s *SimpleNodeRepo) Update(ctx context.Context, node *domain.Node) error {
	return nil
}

func (s *SimpleNodeRepo) Delete(ctx context.Context, id string) error {
	return nil
}

func (s *SimpleNodeRepo) ListByProvider(ctx context.Context, providerID string) ([]*domain.Node, error) {
	return nil, nil
}

func (s *SimpleNodeRepo) GetByProvider(ctx context.Context, providerID string) ([]*domain.Node, error) {
	return nil, nil
}

func (s *SimpleNodeRepo) FindAvailableNodes(ctx context.Context) ([]*domain.Node, error) {
	return nil, nil
}

func (s *SimpleNodeRepo) ListActive(ctx context.Context) ([]*domain.Node, error) {
	return nil, nil
}

func (s *SimpleNodeRepo) GetByGPUUUID(ctx context.Context, gpuUUID string) (*domain.Node, error) {
	return nil, nil
}

// SimpleProviderRepo - minimal provider repo
type SimpleProviderRepo struct{}

func (s *SimpleProviderRepo) Create(ctx context.Context, provider *domain.Provider) error {
	return nil
}

func (s *SimpleProviderRepo) GetByID(ctx context.Context, id string) (*domain.Provider, error) {
	return nil, nil
}

func (s *SimpleProviderRepo) GetByWalletAddress(ctx context.Context, address string) (*domain.Provider, error) {
	return nil, nil
}

func (s *SimpleProviderRepo) GetByWallet(ctx context.Context, walletAddress string) (*domain.Provider, error) {
	return nil, nil
}

func (s *SimpleProviderRepo) Update(ctx context.Context, provider *domain.Provider) error {
	return nil
}

func (s *SimpleProviderRepo) List(ctx context.Context) ([]*domain.Provider, error) {
	return nil, nil
}

func (s *SimpleProviderRepo) ListByType(ctx context.Context, providerType domain.ProviderType) ([]*domain.Provider, error) {
	return nil, nil
}

// SimpleNodeClient - minimal node client
type SimpleNodeClient struct{}

func (s *SimpleNodeClient) StartRental(ctx context.Context, nodeURL string, req rental.StartRentalRequest) (*rental.StartRentalResponse, error) {
	return nil, nil
}

func (s *SimpleNodeClient) StopRental(ctx context.Context, nodeURL string, req rental.StopRentalRequest) (*rental.StopRentalResponse, error) {
	return &rental.StopRentalResponse{}, nil
}
