package sessions_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/worldland/worldland-hub/internal/blockchain"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/sessions"
)

// Mock implementations for testing

type mockConfirmationSessionRepo struct {
	sessions             map[string]*domain.RentalSession
	listPendingWithTxErr error
}

func newMockConfirmationSessionRepo() *mockConfirmationSessionRepo {
	return &mockConfirmationSessionRepo{
		sessions: make(map[string]*domain.RentalSession),
	}
}

func (m *mockConfirmationSessionRepo) Create(ctx context.Context, session *domain.RentalSession) error {
	m.sessions[session.ID] = session
	return nil
}

func (m *mockConfirmationSessionRepo) GetByID(ctx context.Context, id string) (*domain.RentalSession, error) {
	if session, ok := m.sessions[id]; ok {
		return session, nil
	}
	return nil, errors.New("session not found")
}

func (m *mockConfirmationSessionRepo) GetByRentalID(ctx context.Context, rentalID uint64) (*domain.RentalSession, error) {
	for _, s := range m.sessions {
		if s.RentalID != nil && *s.RentalID == rentalID {
			return s, nil
		}
	}
	return nil, errors.New("session not found")
}

func (m *mockConfirmationSessionRepo) Update(ctx context.Context, session *domain.RentalSession) error {
	m.sessions[session.ID] = session
	return nil
}

func (m *mockConfirmationSessionRepo) ListByUser(ctx context.Context, userAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *mockConfirmationSessionRepo) ListByProvider(ctx context.Context, providerAddress string, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *mockConfirmationSessionRepo) ListByState(ctx context.Context, state domain.RentalSessionState, limit, offset int) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *mockConfirmationSessionRepo) FindStale(ctx context.Context, state domain.RentalSessionState, olderThan time.Duration) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *mockConfirmationSessionRepo) FindByUserAndState(ctx context.Context, userAddress string, state domain.RentalSessionState) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *mockConfirmationSessionRepo) FindPendingSettlement(ctx context.Context, userAddress string) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *mockConfirmationSessionRepo) FindAllPendingSettlement(ctx context.Context) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *mockConfirmationSessionRepo) UpdateSettlement(ctx context.Context, sessionID, amount string, settledAt time.Time) error {
	return nil
}

func (m *mockConfirmationSessionRepo) GetByTxHash(ctx context.Context, txHash string) (*domain.RentalSession, error) {
	for _, s := range m.sessions {
		if s.TxHash != nil && *s.TxHash == txHash {
			return s, nil
		}
	}
	return nil, errors.New("session not found")
}

func (m *mockConfirmationSessionRepo) SetTxHash(ctx context.Context, sessionID, txHash string) error {
	if s, ok := m.sessions[sessionID]; ok {
		s.TxHash = &txHash
		return nil
	}
	return errors.New("session not found")
}

func (m *mockConfirmationSessionRepo) SoftDeletePendingBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	return 0, nil
}

func (m *mockConfirmationSessionRepo) ListPendingWithTxHash(ctx context.Context) ([]*domain.RentalSession, error) {
	if m.listPendingWithTxErr != nil {
		return nil, m.listPendingWithTxErr
	}
	var result []*domain.RentalSession
	for _, s := range m.sessions {
		if s.State == domain.RentalStatePending && s.TxHash != nil {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockConfirmationSessionRepo) FindExpiringSessions(ctx context.Context, cutoff time.Time) ([]*domain.RentalSession, error) {
	return nil, nil
}

func (m *mockConfirmationSessionRepo) UpdateExtension(ctx context.Context, sessionID string, extendedUntil time.Time, extensionMinutes int) error {
	return nil
}

func (m *mockConfirmationSessionRepo) CreateExtensionRecord(ctx context.Context, sessionID string, extensionMinutes int, costEstimate, idempotencyKey string) (string, error) {
	return "", nil
}

func (m *mockConfirmationSessionRepo) TouchSession(ctx context.Context, sessionID string) error {
	if _, ok := m.sessions[sessionID]; !ok {
		return errors.New("session not found")
	}
	return nil
}

func (m *mockConfirmationSessionRepo) UpdateSSHInfo(ctx context.Context, sessionID, host string, port int32, user, password string) error {
	return nil
}

func (m *mockConfirmationSessionRepo) LoadRunningSSHInfo(ctx context.Context) (map[string]*domain.SSHConnectionInfo, error) {
	return nil, nil
}

type mockConfirmationNodeRepo struct {
	nodes map[string]*domain.Node
}

func newMockConfirmationNodeRepo() *mockConfirmationNodeRepo {
	return &mockConfirmationNodeRepo{
		nodes: make(map[string]*domain.Node),
	}
}

func (m *mockConfirmationNodeRepo) Create(ctx context.Context, node *domain.Node) error {
	m.nodes[node.ID] = node
	return nil
}

func (m *mockConfirmationNodeRepo) GetByID(ctx context.Context, id string) (*domain.Node, error) {
	if node, ok := m.nodes[id]; ok {
		return node, nil
	}
	return nil, errors.New("node not found")
}

func (m *mockConfirmationNodeRepo) GetByProvider(ctx context.Context, providerID string) ([]*domain.Node, error) {
	return nil, nil
}

func (m *mockConfirmationNodeRepo) Update(ctx context.Context, node *domain.Node) error {
	return nil
}

func (m *mockConfirmationNodeRepo) Delete(ctx context.Context, id string) error {
	return nil
}

func (m *mockConfirmationNodeRepo) ListActive(ctx context.Context) ([]*domain.Node, error) {
	return nil, nil
}

func (m *mockConfirmationNodeRepo) ListActiveGroupedByGPU(ctx context.Context) ([]*domain.GPUTypeGroup, error) {
	return nil, nil
}

func (m *mockConfirmationNodeRepo) GetByGPUUUID(ctx context.Context, gpuUUID string) (*domain.Node, error) {
	return nil, nil
}

type mockConfirmationProviderRepo struct {
	providers map[string]*domain.Provider
}

func newMockConfirmationProviderRepo() *mockConfirmationProviderRepo {
	return &mockConfirmationProviderRepo{
		providers: make(map[string]*domain.Provider),
	}
}

func (m *mockConfirmationProviderRepo) Create(ctx context.Context, provider *domain.Provider) error {
	m.providers[provider.ID] = provider
	return nil
}

func (m *mockConfirmationProviderRepo) GetByID(ctx context.Context, id string) (*domain.Provider, error) {
	if provider, ok := m.providers[id]; ok {
		return provider, nil
	}
	return nil, errors.New("provider not found")
}

func (m *mockConfirmationProviderRepo) GetByWallet(ctx context.Context, walletAddress string) (*domain.Provider, error) {
	for _, p := range m.providers {
		if p.WalletAddress == walletAddress {
			return p, nil
		}
	}
	return nil, errors.New("provider not found")
}

func (m *mockConfirmationProviderRepo) Update(ctx context.Context, provider *domain.Provider) error {
	return nil
}

func (m *mockConfirmationProviderRepo) ListByType(ctx context.Context, providerType domain.ProviderType) ([]*domain.Provider, error) {
	return nil, nil
}

// mockJobExecutor implements domain.JobExecutor for testing
type mockJobExecutor struct {
	createPassword string
	createErr      error
	deleteErr      error
	sshInfo        *domain.SSHConnectionInfo
	sshErr         error
}

func (m *mockJobExecutor) CreateGPUSession(ctx context.Context, spec domain.JobSpec) (string, error) {
	if m.createErr != nil {
		return "", m.createErr
	}
	return m.createPassword, nil
}

func (m *mockJobExecutor) DeleteGPUSession(ctx context.Context, session *domain.RentalSession) error {
	return m.deleteErr
}

func (m *mockJobExecutor) GetSSHConnectionInfo(ctx context.Context, session *domain.RentalSession) (*domain.SSHConnectionInfo, error) {
	if m.sshErr != nil {
		return nil, m.sshErr
	}
	return m.sshInfo, nil
}

func (m *mockJobExecutor) GetPodStatus(ctx context.Context, session *domain.RentalSession) (string, error) {
	return "Pending", nil
}

// Mock ReceiptClient for TransactionVerifier
type mockReceiptClient struct {
	receipt *types.Receipt
	err     error
}

func (m *mockReceiptClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.receipt, nil
}

// Helper to create a confirmed receipt with RentalStarted event
func createConfirmedReceipt(contractAddr common.Address, userAddr common.Address, providerAddr common.Address) *types.Receipt {
	// Create RentalStarted event log
	// Event signature: RentalStarted(uint256 indexed rentalId, address indexed user, address indexed provider, uint256 startTime)
	log := &types.Log{
		Address: contractAddr,
		Topics: []common.Hash{
			blockchain.RentalStartedSig, // Event signature
			common.BigToHash(common.Big1),
			common.HexToHash(userAddr.Hex()),
			common.HexToHash(providerAddr.Hex()),
		},
		Data: common.Hex2Bytes("0000000000000000000000000000000000000000000000000000000067890123"), // startTime as uint256
	}

	blockNum := uint64(12345678)
	return &types.Receipt{
		Status:      types.ReceiptStatusSuccessful,
		BlockNumber: common.Big1.SetUint64(blockNum),
		Logs:        []*types.Log{log},
	}
}

// Tests

func TestConfirmationWorker_VerifiesAndTransitions(t *testing.T) {
	// Setup mocks
	sessionRepo := newMockConfirmationSessionRepo()
	nodeRepo := newMockConfirmationNodeRepo()
	providerRepo := newMockConfirmationProviderRepo()

	userAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	providerAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")
	contractAddr := common.HexToAddress("0x9999999999999999999999999999999999999999")
	txHash := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	// Add session
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     userAddr.Hex(),
		ProviderAddress: providerAddr.Hex(),
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		PricePerSecond:  "1000000000000000",
		TxHash:          &txHash,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// Add node
	nodeRepo.nodes["node-1"] = &domain.Node{
		ID:          "node-1",
		GPUUUID:     "GPU-uuid-123",
		APIEndpoint: "https://node.example.com:8443",
	}

	// Mock receipt client with confirmed transaction
	receiptClient := &mockReceiptClient{
		receipt: createConfirmedReceipt(contractAddr, userAddr, providerAddr),
	}

	// Create verifier
	verifier := blockchain.NewTransactionVerifier(receiptClient, contractAddr)

	// Create session manager
	sessionManager := sessions.NewSessionManager(sessionRepo, nodeRepo, providerRepo)

	// Mock executor
	executor := &mockJobExecutor{
		createPassword: "test-password",
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Create worker
	worker := sessions.NewConfirmationWorker(
		sessionRepo,
		verifier,
		sessionManager,
		nodeRepo,
		executor,
		logger,
	)

	// Process once
	ctx := context.Background()
	worker.ProcessOnce(ctx)

	// Verify session transitioned to RUNNING
	session := sessionRepo.sessions["session-1"]
	assert.Equal(t, domain.RentalStateRunning, session.State)
	assert.NotNil(t, session.RentalID)
	assert.NotNil(t, session.BlockNumber)
}

func TestConfirmationWorker_PendingSkipsGracefully(t *testing.T) {
	// Setup mocks
	sessionRepo := newMockConfirmationSessionRepo()
	nodeRepo := newMockConfirmationNodeRepo()
	providerRepo := newMockConfirmationProviderRepo()

	userAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	providerAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")
	contractAddr := common.HexToAddress("0x9999999999999999999999999999999999999999")
	txHash := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	// Add session
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     userAddr.Hex(),
		ProviderAddress: providerAddr.Hex(),
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		TxHash:          &txHash,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// Mock receipt client with NotFound (transaction pending)
	receiptClient := &mockReceiptClient{
		err: ethereum.NotFound,
	}

	verifier := blockchain.NewTransactionVerifier(receiptClient, contractAddr)
	sessionManager := sessions.NewSessionManager(sessionRepo, nodeRepo, providerRepo)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	worker := sessions.NewConfirmationWorker(
		sessionRepo,
		verifier,
		sessionManager,
		nodeRepo,
		nil, // no executor needed
		logger,
	)

	ctx := context.Background()
	worker.ProcessOnce(ctx)

	// Session should remain PENDING
	session := sessionRepo.sessions["session-1"]
	assert.Equal(t, domain.RentalStatePending, session.State)
}

func TestConfirmationWorker_FailedTransitions(t *testing.T) {
	// Setup mocks
	sessionRepo := newMockConfirmationSessionRepo()
	nodeRepo := newMockConfirmationNodeRepo()
	providerRepo := newMockConfirmationProviderRepo()

	userAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	providerAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")
	contractAddr := common.HexToAddress("0x9999999999999999999999999999999999999999")
	txHash := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	// Add session
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     userAddr.Hex(),
		ProviderAddress: providerAddr.Hex(),
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		TxHash:          &txHash,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// Mock receipt client with reverted transaction
	blockNum := uint64(12345678)
	receiptClient := &mockReceiptClient{
		receipt: &types.Receipt{
			Status:      types.ReceiptStatusFailed, // Transaction reverted
			BlockNumber: common.Big1.SetUint64(blockNum),
			Logs:        []*types.Log{},
		},
	}

	verifier := blockchain.NewTransactionVerifier(receiptClient, contractAddr)
	sessionManager := sessions.NewSessionManager(sessionRepo, nodeRepo, providerRepo)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	worker := sessions.NewConfirmationWorker(
		sessionRepo,
		verifier,
		sessionManager,
		nodeRepo,
		nil, // no executor needed for failed transitions
		logger,
	)

	ctx := context.Background()
	worker.ProcessOnce(ctx)

	// Session should transition to FAILED
	session := sessionRepo.sessions["session-1"]
	assert.Equal(t, domain.RentalStateFailed, session.State)
}

func TestConfirmationWorker_ExecutorFailure(t *testing.T) {
	// Setup mocks
	sessionRepo := newMockConfirmationSessionRepo()
	nodeRepo := newMockConfirmationNodeRepo()
	providerRepo := newMockConfirmationProviderRepo()

	userAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	providerAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")
	contractAddr := common.HexToAddress("0x9999999999999999999999999999999999999999")
	txHash := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	// Add session
	sessionRepo.sessions["session-1"] = &domain.RentalSession{
		ID:              "session-1",
		UserAddress:     userAddr.Hex(),
		ProviderAddress: providerAddr.Hex(),
		NodeID:          "node-1",
		State:           domain.RentalStatePending,
		TxHash:          &txHash,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// Add node
	nodeRepo.nodes["node-1"] = &domain.Node{
		ID:          "node-1",
		GPUUUID:     "GPU-uuid-123",
		APIEndpoint: "https://node.example.com:8443",
	}

	// Mock receipt client with confirmed transaction
	receiptClient := &mockReceiptClient{
		receipt: createConfirmedReceipt(contractAddr, userAddr, providerAddr),
	}

	verifier := blockchain.NewTransactionVerifier(receiptClient, contractAddr)
	sessionManager := sessions.NewSessionManager(sessionRepo, nodeRepo, providerRepo)

	// Mock executor that fails
	executor := &mockJobExecutor{
		createErr: errors.New("K8s cluster unreachable"),
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	worker := sessions.NewConfirmationWorker(
		sessionRepo,
		verifier,
		sessionManager,
		nodeRepo,
		executor,
		logger,
	)

	ctx := context.Background()
	worker.ProcessOnce(ctx)

	// Session should transition to FAILED because executor failed
	session := sessionRepo.sessions["session-1"]
	assert.Equal(t, domain.RentalStateFailed, session.State)
}

func TestConfirmationWorker_ContextCancellation(t *testing.T) {
	sessionRepo := newMockConfirmationSessionRepo()
	nodeRepo := newMockConfirmationNodeRepo()
	providerRepo := newMockConfirmationProviderRepo()

	contractAddr := common.HexToAddress("0x9999999999999999999999999999999999999999")

	receiptClient := &mockReceiptClient{
		err: ethereum.NotFound,
	}

	verifier := blockchain.NewTransactionVerifier(receiptClient, contractAddr)
	sessionManager := sessions.NewSessionManager(sessionRepo, nodeRepo, providerRepo)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	worker := sessions.NewConfirmationWorker(
		sessionRepo,
		verifier,
		sessionManager,
		nodeRepo,
		nil,
		logger,
	).WithInterval(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())

	// Start worker in goroutine
	done := make(chan error, 1)
	go func() {
		done <- worker.Start(ctx)
	}()

	// Let it run briefly
	time.Sleep(100 * time.Millisecond)

	// Cancel context
	cancel()

	// Should return context.Canceled
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(1 * time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
}

func TestConfirmationWorker_ListError(t *testing.T) {
	sessionRepo := newMockConfirmationSessionRepo()
	sessionRepo.listPendingWithTxErr = errors.New("database error")

	nodeRepo := newMockConfirmationNodeRepo()
	providerRepo := newMockConfirmationProviderRepo()
	contractAddr := common.HexToAddress("0x9999999999999999999999999999999999999999")

	receiptClient := &mockReceiptClient{}
	verifier := blockchain.NewTransactionVerifier(receiptClient, contractAddr)
	sessionManager := sessions.NewSessionManager(sessionRepo, nodeRepo, providerRepo)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	worker := sessions.NewConfirmationWorker(
		sessionRepo,
		verifier,
		sessionManager,
		nodeRepo,
		nil, // no executor needed
		logger,
	)

	// Should not panic on list error
	ctx := context.Background()
	worker.ProcessOnce(ctx) // Should handle error gracefully
}
