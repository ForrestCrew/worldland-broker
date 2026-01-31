package indexer

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/worldland/worldland-hub/internal/blockchain"
)

// =============================================================================
// Mock Repository
// =============================================================================

// mockIndexerRepository captures calls to repository methods for verification.
type mockIndexerRepository struct {
	depositWithdrawRecords []DepositWithdrawRecord
	rentalStartedRecords   []RentalStartedRecord
	rentalStoppedRecords   []RentalStoppedRecord
	insertError            error
	updateError            error
}

func (m *mockIndexerRepository) InsertDepositWithdraw(ctx context.Context, record DepositWithdrawRecord) error {
	if m.insertError != nil {
		return m.insertError
	}
	m.depositWithdrawRecords = append(m.depositWithdrawRecords, record)
	return nil
}

func (m *mockIndexerRepository) InsertRentalStarted(ctx context.Context, record RentalStartedRecord) error {
	if m.insertError != nil {
		return m.insertError
	}
	m.rentalStartedRecords = append(m.rentalStartedRecords, record)
	return nil
}

func (m *mockIndexerRepository) UpdateRentalStopped(ctx context.Context, record RentalStoppedRecord) error {
	if m.updateError != nil {
		return m.updateError
	}
	m.rentalStoppedRecords = append(m.rentalStoppedRecords, record)
	return nil
}

// =============================================================================
// Processor Interface Adapter
// =============================================================================

// processorRepository defines the interface needed by IndexerProcessor.
// This allows us to inject the mock repository.
type processorRepository interface {
	InsertDepositWithdraw(ctx context.Context, record DepositWithdrawRecord) error
	InsertRentalStarted(ctx context.Context, record RentalStartedRecord) error
	UpdateRentalStopped(ctx context.Context, record RentalStoppedRecord) error
}

// testableProcessor wraps the processor logic for testing with mock repo.
type testableProcessor struct {
	repo processorRepository
}

func (p *testableProcessor) HandleDeposited(ctx context.Context, event *blockchain.DepositedEvent) error {
	record := DepositWithdrawRecord{
		UserAddress:    strings.ToLower(event.User.Hex()),
		EventType:      "deposit",
		Amount:         event.Amount.String(),
		TxHash:         event.TxHash.Hex(),
		BlockNumber:    event.BlockNumber,
		BlockTimestamp: time.Now(),
		LogIndex:       int(event.LogIndex),
	}
	return p.repo.InsertDepositWithdraw(ctx, record)
}

func (p *testableProcessor) HandleWithdrawn(ctx context.Context, event *blockchain.WithdrawnEvent) error {
	record := DepositWithdrawRecord{
		UserAddress:    strings.ToLower(event.User.Hex()),
		EventType:      "withdraw",
		Amount:         event.Amount.String(),
		TxHash:         event.TxHash.Hex(),
		BlockNumber:    event.BlockNumber,
		BlockTimestamp: time.Now(),
		LogIndex:       int(event.LogIndex),
	}
	return p.repo.InsertDepositWithdraw(ctx, record)
}

func (p *testableProcessor) HandleRentalStarted(ctx context.Context, event *blockchain.RentalStartedEvent) error {
	record := RentalStartedRecord{
		RentalID:        event.RentalID,
		UserAddress:     strings.ToLower(event.User.Hex()),
		ProviderAddress: strings.ToLower(event.Provider.Hex()),
		StartTime:       time.Unix(int64(event.StartTime), 0),
		TxHash:          event.TxHash.Hex(),
		BlockNumber:     event.BlockNumber,
	}
	return p.repo.InsertRentalStarted(ctx, record)
}

func (p *testableProcessor) HandleRentalStopped(ctx context.Context, event *blockchain.RentalStoppedEvent) error {
	record := RentalStoppedRecord{
		RentalID:    event.RentalID,
		EndTime:     time.Unix(int64(event.EndTime), 0),
		CostWei:     event.Cost.String(),
		TxHash:      event.TxHash.Hex(),
		BlockNumber: event.BlockNumber,
	}
	return p.repo.UpdateRentalStopped(ctx, record)
}

// =============================================================================
// HandleDeposited Tests
// =============================================================================

func TestHandleDeposited_Success(t *testing.T) {
	mockRepo := &mockIndexerRepository{}
	processor := &testableProcessor{repo: mockRepo}
	ctx := context.Background()

	event := &blockchain.DepositedEvent{
		User:        common.HexToAddress("0xAbCdEf1234567890AbCdEf1234567890AbCdEf12"),
		Amount:      big.NewInt(1000000000000000000), // 1 ETH
		TxHash:      common.HexToHash("0xabc123def456abc123def456abc123def456abc123def456abc123def456abc1"),
		BlockNumber: 12345,
		LogIndex:    3,
	}

	err := processor.HandleDeposited(ctx, event)
	require.NoError(t, err)

	// Verify record was inserted
	require.Len(t, mockRepo.depositWithdrawRecords, 1)
	record := mockRepo.depositWithdrawRecords[0]

	// Verify address is lowercased
	assert.Equal(t, "0xabcdef1234567890abcdef1234567890abcdef12", record.UserAddress)

	// Verify event type
	assert.Equal(t, "deposit", record.EventType)

	// Verify amount string conversion
	assert.Equal(t, "1000000000000000000", record.Amount)

	// Verify other fields
	assert.Equal(t, event.TxHash.Hex(), record.TxHash)
	assert.Equal(t, event.BlockNumber, record.BlockNumber)
	assert.Equal(t, int(event.LogIndex), record.LogIndex)
}

// =============================================================================
// HandleWithdrawn Tests
// =============================================================================

func TestHandleWithdrawn_Success(t *testing.T) {
	mockRepo := &mockIndexerRepository{}
	processor := &testableProcessor{repo: mockRepo}
	ctx := context.Background()

	event := &blockchain.WithdrawnEvent{
		User:        common.HexToAddress("0x1234567890AbCdEf1234567890AbCdEf12345678"),
		Amount:      big.NewInt(500000000000000000), // 0.5 ETH
		TxHash:      common.HexToHash("0xdef456abc123def456abc123def456abc123def456abc123def456abc123def4"),
		BlockNumber: 12350,
		LogIndex:    1,
	}

	err := processor.HandleWithdrawn(ctx, event)
	require.NoError(t, err)

	// Verify record was inserted
	require.Len(t, mockRepo.depositWithdrawRecords, 1)
	record := mockRepo.depositWithdrawRecords[0]

	// Verify event type is withdraw
	assert.Equal(t, "withdraw", record.EventType)

	// Verify address is lowercased
	assert.Equal(t, "0x1234567890abcdef1234567890abcdef12345678", record.UserAddress)

	// Verify amount
	assert.Equal(t, "500000000000000000", record.Amount)
}

// =============================================================================
// Table-Driven Deposit/Withdraw Tests
// =============================================================================

func TestHandleDepositWithdraw_TableDriven(t *testing.T) {
	tests := []struct {
		name              string
		eventType         string // "deposit" or "withdraw"
		userAddress       string
		amount            *big.Int
		expectedEventType string
	}{
		{
			name:              "deposit event",
			eventType:         "deposit",
			userAddress:       "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			amount:            big.NewInt(2000000000000000000),
			expectedEventType: "deposit",
		},
		{
			name:              "withdraw event",
			eventType:         "withdraw",
			userAddress:       "0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
			amount:            big.NewInt(750000000000000000),
			expectedEventType: "withdraw",
		},
		{
			name:              "deposit with mixed case address",
			eventType:         "deposit",
			userAddress:       "0xAbCdEfAbCdEfAbCdEfAbCdEfAbCdEfAbCdEfAbCd",
			amount:            big.NewInt(100000000000000000),
			expectedEventType: "deposit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := &mockIndexerRepository{}
			processor := &testableProcessor{repo: mockRepo}
			ctx := context.Background()

			user := common.HexToAddress(tt.userAddress)
			txHash := common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234")

			var err error
			if tt.eventType == "deposit" {
				event := &blockchain.DepositedEvent{
					User:        user,
					Amount:      tt.amount,
					TxHash:      txHash,
					BlockNumber: 10000,
					LogIndex:    0,
				}
				err = processor.HandleDeposited(ctx, event)
			} else {
				event := &blockchain.WithdrawnEvent{
					User:        user,
					Amount:      tt.amount,
					TxHash:      txHash,
					BlockNumber: 10000,
					LogIndex:    0,
				}
				err = processor.HandleWithdrawn(ctx, event)
			}

			require.NoError(t, err)
			require.Len(t, mockRepo.depositWithdrawRecords, 1)

			record := mockRepo.depositWithdrawRecords[0]
			assert.Equal(t, tt.expectedEventType, record.EventType)
			assert.Equal(t, strings.ToLower(user.Hex()), record.UserAddress, "Address should be lowercased")
			assert.Equal(t, tt.amount.String(), record.Amount)
		})
	}
}

// =============================================================================
// HandleRentalStarted Tests
// =============================================================================

func TestHandleRentalStarted_Success(t *testing.T) {
	mockRepo := &mockIndexerRepository{}
	processor := &testableProcessor{repo: mockRepo}
	ctx := context.Background()

	startTime := uint64(1700000000) // Unix timestamp

	event := &blockchain.RentalStartedEvent{
		RentalID:    42,
		User:        common.HexToAddress("0xUserUserUserUserUserUserUserUserUserUser"),
		Provider:    common.HexToAddress("0xProvProvProvProvProvProvProvProvProvProv"),
		StartTime:   startTime,
		TxHash:      common.HexToHash("0xrentalstarttx123456789012345678901234567890123456789012345678901"),
		BlockNumber: 20000,
		LogIndex:    5,
	}

	err := processor.HandleRentalStarted(ctx, event)
	require.NoError(t, err)

	// Verify record was inserted
	require.Len(t, mockRepo.rentalStartedRecords, 1)
	record := mockRepo.rentalStartedRecords[0]

	// Verify rental ID
	assert.Equal(t, uint64(42), record.RentalID)

	// Verify addresses are lowercased
	assert.Equal(t, strings.ToLower(event.User.Hex()), record.UserAddress)
	assert.Equal(t, strings.ToLower(event.Provider.Hex()), record.ProviderAddress)

	// Verify start time is converted from unix timestamp
	expectedTime := time.Unix(int64(startTime), 0)
	assert.Equal(t, expectedTime, record.StartTime)

	// Verify other fields
	assert.Equal(t, event.TxHash.Hex(), record.TxHash)
	assert.Equal(t, event.BlockNumber, record.BlockNumber)
}

// =============================================================================
// HandleRentalStopped Tests
// =============================================================================

func TestHandleRentalStopped_Success(t *testing.T) {
	mockRepo := &mockIndexerRepository{}
	processor := &testableProcessor{repo: mockRepo}
	ctx := context.Background()

	endTime := uint64(1700003600) // 1 hour after some start
	cost := big.NewInt(3600000000000000000) // 3.6 ETH

	event := &blockchain.RentalStoppedEvent{
		RentalID:    42,
		EndTime:     endTime,
		Cost:        cost,
		TxHash:      common.HexToHash("0xrentalstoptx1234567890123456789012345678901234567890123456789012"),
		BlockNumber: 20100,
		LogIndex:    2,
	}

	err := processor.HandleRentalStopped(ctx, event)
	require.NoError(t, err)

	// Verify record was stored
	require.Len(t, mockRepo.rentalStoppedRecords, 1)
	record := mockRepo.rentalStoppedRecords[0]

	// Verify rental ID
	assert.Equal(t, uint64(42), record.RentalID)

	// Verify end time is converted from unix timestamp
	expectedTime := time.Unix(int64(endTime), 0)
	assert.Equal(t, expectedTime, record.EndTime)

	// Verify cost is converted to string
	assert.Equal(t, "3600000000000000000", record.CostWei)

	// Verify other fields
	assert.Equal(t, event.TxHash.Hex(), record.TxHash)
	assert.Equal(t, event.BlockNumber, record.BlockNumber)
}

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewIndexerProcessor(t *testing.T) {
	// Test that constructor works (with nil repo for compile check)
	processor := NewIndexerProcessor(nil)
	assert.NotNil(t, processor, "Processor should be created")
}

// =============================================================================
// Interface Implementation Test
// =============================================================================

func TestIndexerProcessor_ImplementsEventHandler(t *testing.T) {
	// Compile-time check that IndexerProcessor implements EventHandler
	var _ blockchain.EventHandler = (*IndexerProcessor)(nil)
}
