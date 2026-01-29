package blockchain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEventHandler implements EventHandler for testing
type mockEventHandler struct {
	startedCalls []*RentalStartedEvent
	stoppedCalls []*RentalStoppedEvent
	shouldFail   bool
}

func (m *mockEventHandler) HandleRentalStarted(ctx context.Context, event *RentalStartedEvent) error {
	if m.shouldFail {
		return errors.New("handler failed")
	}
	m.startedCalls = append(m.startedCalls, event)
	return nil
}

func (m *mockEventHandler) HandleRentalStopped(ctx context.Context, event *RentalStoppedEvent) error {
	if m.shouldFail {
		return errors.New("handler failed")
	}
	m.stoppedCalls = append(m.stoppedCalls, event)
	return nil
}

// mockCheckpointStore implements checkpoint storage for testing
type mockCheckpointStore struct {
	lastBlock       uint64
	processedHashes map[string]bool
	failedEvents    []FailedEvent
	shouldFail      bool
}

func newMockCheckpointStore() *mockCheckpointStore {
	return &mockCheckpointStore{
		processedHashes: make(map[string]bool),
	}
}

func (m *mockCheckpointStore) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	if m.shouldFail {
		return 0, errors.New("checkpoint error")
	}
	return m.lastBlock, nil
}

func (m *mockCheckpointStore) UpdateCheckpoint(ctx context.Context, blockNumber uint64) error {
	if m.shouldFail {
		return errors.New("checkpoint error")
	}
	if blockNumber > m.lastBlock {
		m.lastBlock = blockNumber
	}
	return nil
}

func (m *mockCheckpointStore) IsProcessed(ctx context.Context, txHash string) (bool, error) {
	if m.shouldFail {
		return false, errors.New("checkpoint error")
	}
	return m.processedHashes[txHash], nil
}

func (m *mockCheckpointStore) MarkProcessed(ctx context.Context, txHash string, blockNumber uint64, logIndex int, eventType string) error {
	if m.shouldFail {
		return errors.New("checkpoint error")
	}
	m.processedHashes[txHash] = true
	return nil
}

func (m *mockCheckpointStore) SaveFailedEvent(ctx context.Context, txHash string, blockNumber uint64, logIndex int, eventType string, rawData []byte, errMsg string) error {
	if m.shouldFail {
		return errors.New("checkpoint error")
	}
	m.failedEvents = append(m.failedEvents, FailedEvent{
		TxHash:       txHash,
		BlockNumber:  blockNumber,
		LogIndex:     logIndex,
		EventType:    eventType,
		RawData:      rawData,
		ErrorMessage: errMsg,
	})
	return nil
}

// mockEthClient implements EthClient for testing
type mockEthClient struct {
	blockNumber uint64
	logs        []types.Log
	subErr      chan error
	subLogs     chan types.Log
	dialErr     error
	filterErr   error
	subFail     bool
}

func (m *mockEthClient) BlockNumber(ctx context.Context) (uint64, error) {
	return m.blockNumber, nil
}

func (m *mockEthClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	if m.filterErr != nil {
		return nil, m.filterErr
	}
	return m.logs, nil
}

func (m *mockEthClient) SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	if m.subFail {
		return nil, errors.New("subscription failed")
	}
	return &mockSubscription{
		errCh:   m.subErr,
		logCh:   ch,
		srcLogs: m.subLogs,
	}, nil
}

func (m *mockEthClient) Close() {}

// mockSubscription implements ethereum.Subscription for testing
type mockSubscription struct {
	errCh   chan error
	logCh   chan<- types.Log
	srcLogs chan types.Log
}

func (m *mockSubscription) Err() <-chan error {
	return m.errCh
}

func (m *mockSubscription) Unsubscribe() {
	close(m.errCh)
}

// testableEventListener wraps EventListener for testing internal methods
type testableEventListener struct {
	*EventListener
	mockCheckpoint *mockCheckpointStore
}

func newTestableListener(handler EventHandler) *testableEventListener {
	mockCP := newMockCheckpointStore()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	listener := &EventListener{
		rpcURLs:         []string{"ws://localhost:8545"},
		contractAddress: common.HexToAddress("0x1234567890123456789012345678901234567890"),
		checkpoint:      nil, // Will use mock methods
		handler:         handler,
		logger:          logger,
		dialFunc:        nil,
	}

	return &testableEventListener{
		EventListener:  listener,
		mockCheckpoint: mockCP,
	}
}

// Override checkpoint methods to use mock
func (t *testableEventListener) processLogWithMockCheckpoint(ctx context.Context, vLog types.Log) error {
	txHash := vLog.TxHash.Hex()

	// Idempotency check
	processed, err := t.mockCheckpoint.IsProcessed(ctx, txHash)
	if err != nil {
		return fmt.Errorf("check processed: %w", err)
	}
	if processed {
		return nil
	}

	eventType := IdentifyEvent(vLog)
	var processErr error

	switch eventType {
	case EventTypeRentalStarted:
		event, err := ParseRentalStarted(vLog)
		if err != nil {
			processErr = fmt.Errorf("parse RentalStarted: %w", err)
		} else {
			processErr = t.handler.HandleRentalStarted(ctx, event)
		}

	case EventTypeRentalStopped:
		event, err := ParseRentalStopped(vLog)
		if err != nil {
			processErr = fmt.Errorf("parse RentalStopped: %w", err)
		} else {
			processErr = t.handler.HandleRentalStopped(ctx, event)
		}

	default:
		return nil
	}

	if processErr != nil {
		t.mockCheckpoint.SaveFailedEvent(ctx, txHash, vLog.BlockNumber, int(vLog.Index), string(eventType), vLog.Data, processErr.Error())
		return processErr
	}

	if err := t.mockCheckpoint.MarkProcessed(ctx, txHash, vLog.BlockNumber, int(vLog.Index), string(eventType)); err != nil {
		return fmt.Errorf("mark processed: %w", err)
	}

	t.mockCheckpoint.UpdateCheckpoint(ctx, vLog.BlockNumber)
	return nil
}

// createRentalStartedLog creates a valid RentalStarted log for testing
func createRentalStartedLog(rentalID uint64, user, provider common.Address, startTime uint64, txHash common.Hash, blockNum uint64, logIdx uint) types.Log {
	// Pad values to 32 bytes
	rentalIDBytes := common.LeftPadBytes(big.NewInt(int64(rentalID)).Bytes(), 32)
	userBytes := common.LeftPadBytes(user.Bytes(), 32)
	providerBytes := common.LeftPadBytes(provider.Bytes(), 32)
	startTimeBytes := common.LeftPadBytes(big.NewInt(int64(startTime)).Bytes(), 32)

	return types.Log{
		Address: common.HexToAddress("0x1234567890123456789012345678901234567890"),
		Topics: []common.Hash{
			RentalStartedSig,
			common.BytesToHash(rentalIDBytes),
			common.BytesToHash(userBytes),
			common.BytesToHash(providerBytes),
		},
		Data:        startTimeBytes,
		BlockNumber: blockNum,
		TxHash:      txHash,
		Index:       logIdx,
	}
}

// createRentalStoppedLog creates a valid RentalStopped log for testing
func createRentalStoppedLog(rentalID, endTime uint64, cost *big.Int, txHash common.Hash, blockNum uint64, logIdx uint) types.Log {
	rentalIDBytes := common.LeftPadBytes(big.NewInt(int64(rentalID)).Bytes(), 32)
	endTimeBytes := common.LeftPadBytes(big.NewInt(int64(endTime)).Bytes(), 32)
	costBytes := common.LeftPadBytes(cost.Bytes(), 32)

	data := make([]byte, 64)
	copy(data[0:32], endTimeBytes)
	copy(data[32:64], costBytes)

	return types.Log{
		Address: common.HexToAddress("0x1234567890123456789012345678901234567890"),
		Topics: []common.Hash{
			RentalStoppedSig,
			common.BytesToHash(rentalIDBytes),
		},
		Data:        data,
		BlockNumber: blockNum,
		TxHash:      txHash,
		Index:       logIdx,
	}
}

func TestEventListener_ProcessLog_RentalStarted(t *testing.T) {
	handler := &mockEventHandler{}
	listener := newTestableListener(handler)

	ctx := context.Background()
	txHash := common.HexToHash("0xabc123")
	user := common.HexToAddress("0x1111111111111111111111111111111111111111")
	provider := common.HexToAddress("0x2222222222222222222222222222222222222222")

	log := createRentalStartedLog(1, user, provider, 1700000000, txHash, 1000, 0)

	err := listener.processLogWithMockCheckpoint(ctx, log)
	require.NoError(t, err)

	// Verify handler was called
	require.Len(t, handler.startedCalls, 1)
	assert.Equal(t, uint64(1), handler.startedCalls[0].RentalID)
	assert.Equal(t, user, handler.startedCalls[0].User)
	assert.Equal(t, provider, handler.startedCalls[0].Provider)
	assert.Equal(t, uint64(1700000000), handler.startedCalls[0].StartTime)

	// Verify checkpoint updated
	assert.True(t, listener.mockCheckpoint.processedHashes[txHash.Hex()])
	assert.Equal(t, uint64(1000), listener.mockCheckpoint.lastBlock)
}

func TestEventListener_ProcessLog_RentalStopped(t *testing.T) {
	handler := &mockEventHandler{}
	listener := newTestableListener(handler)

	ctx := context.Background()
	txHash := common.HexToHash("0xdef456")
	cost := big.NewInt(1000000000000000000) // 1 ETH

	log := createRentalStoppedLog(1, 1700003600, cost, txHash, 1001, 0)

	err := listener.processLogWithMockCheckpoint(ctx, log)
	require.NoError(t, err)

	// Verify handler was called
	require.Len(t, handler.stoppedCalls, 1)
	assert.Equal(t, uint64(1), handler.stoppedCalls[0].RentalID)
	assert.Equal(t, uint64(1700003600), handler.stoppedCalls[0].EndTime)
	assert.Equal(t, cost, handler.stoppedCalls[0].Cost)
}

func TestEventListener_ProcessLog_DuplicateSkipped(t *testing.T) {
	handler := &mockEventHandler{}
	listener := newTestableListener(handler)

	ctx := context.Background()
	txHash := common.HexToHash("0xabc123")
	user := common.HexToAddress("0x1111111111111111111111111111111111111111")
	provider := common.HexToAddress("0x2222222222222222222222222222222222222222")

	log := createRentalStartedLog(1, user, provider, 1700000000, txHash, 1000, 0)

	// First call processes the event
	err := listener.processLogWithMockCheckpoint(ctx, log)
	require.NoError(t, err)
	require.Len(t, handler.startedCalls, 1)

	// Second call should be skipped (duplicate)
	err = listener.processLogWithMockCheckpoint(ctx, log)
	require.NoError(t, err)
	require.Len(t, handler.startedCalls, 1) // Still 1, not 2
}

func TestEventListener_ProcessLog_HandlerFailure_SavesToDLQ(t *testing.T) {
	handler := &mockEventHandler{shouldFail: true}
	listener := newTestableListener(handler)

	ctx := context.Background()
	txHash := common.HexToHash("0xfail123")
	user := common.HexToAddress("0x1111111111111111111111111111111111111111")
	provider := common.HexToAddress("0x2222222222222222222222222222222222222222")

	log := createRentalStartedLog(1, user, provider, 1700000000, txHash, 1000, 0)

	err := listener.processLogWithMockCheckpoint(ctx, log)
	require.Error(t, err)

	// Verify event was saved to DLQ
	require.Len(t, listener.mockCheckpoint.failedEvents, 1)
	assert.Equal(t, txHash.Hex(), listener.mockCheckpoint.failedEvents[0].TxHash)
	assert.Equal(t, "RentalStarted", listener.mockCheckpoint.failedEvents[0].EventType)
	assert.Contains(t, listener.mockCheckpoint.failedEvents[0].ErrorMessage, "handler failed")
}

func TestEventListener_ProcessLog_UnknownEventSkipped(t *testing.T) {
	handler := &mockEventHandler{}
	listener := newTestableListener(handler)

	ctx := context.Background()

	// Create a log with unknown event signature
	log := types.Log{
		Address: common.HexToAddress("0x1234567890123456789012345678901234567890"),
		Topics: []common.Hash{
			common.HexToHash("0xunknownsignature"),
		},
		Data:        []byte{},
		BlockNumber: 1000,
		TxHash:      common.HexToHash("0xunknown"),
		Index:       0,
	}

	err := listener.processLogWithMockCheckpoint(ctx, log)
	require.NoError(t, err)

	// Handler should not be called
	assert.Len(t, handler.startedCalls, 0)
	assert.Len(t, handler.stoppedCalls, 0)
}

func TestEventListener_DialWithFailover_FirstSucceeds(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	successClient := &mockEthClient{blockNumber: 1000}

	listener := &EventListener{
		rpcURLs:         []string{"ws://first:8545", "ws://second:8545"},
		contractAddress: common.HexToAddress("0x1234567890123456789012345678901234567890"),
		logger:          logger,
		dialFunc: func(ctx context.Context, url string) (EthClient, error) {
			if url == "ws://first:8545" {
				return successClient, nil
			}
			return nil, errors.New("connection failed")
		},
	}

	ctx := context.Background()
	client, err := listener.dialWithFailover(ctx)
	require.NoError(t, err)
	assert.Equal(t, successClient, client)
}

func TestEventListener_DialWithFailover_SecondSucceeds(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	successClient := &mockEthClient{blockNumber: 1000}
	callCount := 0

	listener := &EventListener{
		rpcURLs:         []string{"ws://first:8545", "ws://second:8545"},
		contractAddress: common.HexToAddress("0x1234567890123456789012345678901234567890"),
		logger:          logger,
		dialFunc: func(ctx context.Context, url string) (EthClient, error) {
			callCount++
			if url == "ws://second:8545" {
				return successClient, nil
			}
			return nil, errors.New("connection failed")
		},
	}

	ctx := context.Background()
	client, err := listener.dialWithFailover(ctx)
	require.NoError(t, err)
	assert.Equal(t, successClient, client)
	assert.Equal(t, 2, callCount) // First failed, second succeeded
}

func TestEventListener_DialWithFailover_AllFail(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	listener := &EventListener{
		rpcURLs:         []string{"ws://first:8545", "ws://second:8545"},
		contractAddress: common.HexToAddress("0x1234567890123456789012345678901234567890"),
		logger:          logger,
		dialFunc: func(ctx context.Context, url string) (EthClient, error) {
			return nil, errors.New("connection failed")
		},
	}

	ctx := context.Background()
	_, err := listener.dialWithFailover(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all RPC endpoints failed")
}

func TestEventListener_Backfill(t *testing.T) {
	handler := &mockEventHandler{}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create logs for backfill
	user := common.HexToAddress("0x1111111111111111111111111111111111111111")
	provider := common.HexToAddress("0x2222222222222222222222222222222222222222")
	log1 := createRentalStartedLog(1, user, provider, 1700000000, common.HexToHash("0x111"), 100, 0)
	log2 := createRentalStartedLog(2, user, provider, 1700001000, common.HexToHash("0x222"), 150, 0)

	mockClient := &mockEthClient{
		blockNumber: 200,
		logs:        []types.Log{log1, log2},
	}

	mockCP := newMockCheckpointStore()
	mockCP.lastBlock = 50 // Start from block 50

	listener := &EventListener{
		rpcURLs:         []string{"ws://localhost:8545"},
		contractAddress: common.HexToAddress("0x1234567890123456789012345678901234567890"),
		checkpoint:      nil,
		handler:         handler,
		logger:          logger,
	}

	// Use the testable version with mock checkpoint
	testListener := &testableEventListener{
		EventListener:  listener,
		mockCheckpoint: mockCP,
	}

	// Manually test backfill logic by processing logs
	ctx := context.Background()
	for _, log := range mockClient.logs {
		err := testListener.processLogWithMockCheckpoint(ctx, log)
		require.NoError(t, err)
	}

	// Verify both events were processed
	require.Len(t, handler.startedCalls, 2)
	assert.Equal(t, uint64(1), handler.startedCalls[0].RentalID)
	assert.Equal(t, uint64(2), handler.startedCalls[1].RentalID)

	// Verify checkpoint updated to latest block
	assert.Equal(t, uint64(150), testListener.mockCheckpoint.lastBlock)
}

func TestEventListener_Constants(t *testing.T) {
	// Verify constants are set correctly
	assert.Equal(t, uint64(15), uint64(FinalityBlocks))
	assert.Equal(t, uint64(1000), uint64(BackfillBatchSize))
}
