package settlement

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/worldland/worldland-hub/internal/domain"
)

func TestProcessBatch_NoSessions(t *testing.T) {
	mockRepo := &mockSettlementRepo{sessions: []*domain.RentalSession{}}
	calc := NewCalculator(mockRepo)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	processor := NewBatchProcessor(calc, mockRepo, nil, logger)

	err := processor.ProcessBatch(context.Background())

	assert.NoError(t, err)
}

func TestProcessBatch_SettlesSessions(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute)
	end := time.Now().Add(-2 * time.Minute)

	mockRepo := &mockSettlementRepo{
		sessions: []*domain.RentalSession{
			{
				ID:             "session-1",
				State:          domain.RentalStateStopped,
				StartTime:      &start,
				EndTime:        &end,
				PricePerSecond: "1000000000000000",
			},
		},
	}
	calc := NewCalculator(mockRepo)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	processor := NewBatchProcessor(calc, mockRepo, nil, logger)

	err := processor.ProcessBatch(context.Background())

	require.NoError(t, err)
	assert.True(t, mockRepo.updateSettlementCalled)
	assert.Equal(t, "session-1", mockRepo.lastSettledSessionID)
}

func TestProcessBatch_ContinuesOnFailure(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute)
	end := time.Now().Add(-2 * time.Minute)

	mockRepo := &mockSettlementRepo{
		sessions: []*domain.RentalSession{
			{ID: "session-1", State: domain.RentalStateStopped, StartTime: &start, EndTime: &end, PricePerSecond: "1000000000000000"},
			{ID: "session-2", State: domain.RentalStateStopped, StartTime: &start, EndTime: &end, PricePerSecond: "1000000000000000"},
		},
		failOnSessionID: "session-1",
	}
	calc := NewCalculator(mockRepo)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	processor := NewBatchProcessor(calc, mockRepo, nil, logger)

	err := processor.ProcessBatch(context.Background())

	require.NoError(t, err) // Continues despite first failure
	assert.Equal(t, "session-2", mockRepo.lastSettledSessionID)
}

func TestStart_RunsOnInterval(t *testing.T) {
	mockRepo := &mockSettlementRepo{sessions: []*domain.RentalSession{}}
	calc := NewCalculator(mockRepo)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	processor := NewBatchProcessor(calc, mockRepo, nil, logger)
	processor.SetInterval(50 * time.Millisecond) // Short for test

	ctx, cancel := context.WithCancel(context.Background())
	processor.Start(ctx)

	time.Sleep(120 * time.Millisecond) // Wait for ~2 ticks
	cancel()
	processor.Stop()

	assert.GreaterOrEqual(t, mockRepo.findPendingSettlementCalls, 2)
}

func TestStop_GracefulShutdown(t *testing.T) {
	mockRepo := &mockSettlementRepo{sessions: []*domain.RentalSession{}}
	calc := NewCalculator(mockRepo)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	processor := NewBatchProcessor(calc, mockRepo, nil, logger)
	processor.SetInterval(1 * time.Hour)

	ctx := context.Background()
	processor.Start(ctx)

	// Stop should return without blocking
	done := make(chan struct{})
	go func() {
		processor.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("Stop() blocked")
	}
}

// mockSettlementRepo for testing
type mockSettlementRepo struct {
	domain.RentalSessionRepository
	sessions                   []*domain.RentalSession
	updateSettlementCalled     bool
	lastSettledSessionID       string
	findPendingSettlementCalls int
	failOnSessionID            string
}

func (m *mockSettlementRepo) FindAllPendingSettlement(ctx context.Context) ([]*domain.RentalSession, error) {
	m.findPendingSettlementCalls++
	return m.sessions, nil
}

func (m *mockSettlementRepo) UpdateSettlement(ctx context.Context, sessionID, amount string, settledAt time.Time) error {
	if sessionID == m.failOnSessionID {
		return errors.New("mock error")
	}
	m.updateSettlementCalled = true
	m.lastSettledSessionID = sessionID
	return nil
}

// mockContractTransferer for testing blockchain transfers
type mockContractTransferer struct {
	transferCalled   bool
	lastFrom         string
	lastTo           string
	lastAmount       *big.Int
	shouldFail       bool
	transferCallsMux int
}

func (m *mockContractTransferer) TransferSettlement(ctx context.Context, from, to string, amount *big.Int) error {
	m.transferCalled = true
	m.lastFrom = from
	m.lastTo = to
	m.lastAmount = amount
	m.transferCallsMux++
	if m.shouldFail {
		return errors.New("blockchain transfer failed")
	}
	return nil
}

// Test: TransferSettlement is called after session settled
func TestProcessBatch_CallsTransferSettlement(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute)
	end := time.Now().Add(-2 * time.Minute)

	mockRepo := &mockSettlementRepo{
		sessions: []*domain.RentalSession{
			{
				ID:              "session-1",
				State:           domain.RentalStateStopped,
				StartTime:       &start,
				EndTime:         &end,
				PricePerSecond:  "1000000000000000",
				UserAddress:     "0xUser123",
				ProviderAddress: "0xProvider456",
			},
		},
	}
	mockTransfer := &mockContractTransferer{}
	calc := NewCalculator(mockRepo)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	processor := NewBatchProcessor(calc, mockRepo, mockTransfer, logger)

	err := processor.ProcessBatch(context.Background())

	require.NoError(t, err)
	assert.True(t, mockRepo.updateSettlementCalled)
	assert.True(t, mockTransfer.transferCalled, "TransferSettlement should be called after DB update")
	assert.Equal(t, "0xUser123", mockTransfer.lastFrom)
	assert.Equal(t, "0xProvider456", mockTransfer.lastTo)
	assert.NotNil(t, mockTransfer.lastAmount)
}

// Test: Settlement succeeds even if TransferSettlement returns error
func TestProcessBatch_SucceedsEvenIfTransferFails(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute)
	end := time.Now().Add(-2 * time.Minute)

	mockRepo := &mockSettlementRepo{
		sessions: []*domain.RentalSession{
			{
				ID:              "session-1",
				State:           domain.RentalStateStopped,
				StartTime:       &start,
				EndTime:         &end,
				PricePerSecond:  "1000000000000000",
				UserAddress:     "0xUser123",
				ProviderAddress: "0xProvider456",
			},
		},
	}
	mockTransfer := &mockContractTransferer{shouldFail: true}
	calc := NewCalculator(mockRepo)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	processor := NewBatchProcessor(calc, mockRepo, mockTransfer, logger)

	err := processor.ProcessBatch(context.Background())

	// Settlement should succeed even though blockchain transfer failed
	require.NoError(t, err, "Settlement should succeed even if blockchain transfer fails")
	assert.True(t, mockRepo.updateSettlementCalled, "DB settlement should still be recorded")
	assert.True(t, mockTransfer.transferCalled, "TransferSettlement should still be called")
}

// Test: No transfer call when contractClient is nil
func TestProcessBatch_NoTransferWhenClientNil(t *testing.T) {
	start := time.Now().Add(-10 * time.Minute)
	end := time.Now().Add(-2 * time.Minute)

	mockRepo := &mockSettlementRepo{
		sessions: []*domain.RentalSession{
			{
				ID:              "session-1",
				State:           domain.RentalStateStopped,
				StartTime:       &start,
				EndTime:         &end,
				PricePerSecond:  "1000000000000000",
				UserAddress:     "0xUser123",
				ProviderAddress: "0xProvider456",
			},
		},
	}
	calc := NewCalculator(mockRepo)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Pass nil contractClient
	processor := NewBatchProcessor(calc, mockRepo, nil, logger)

	err := processor.ProcessBatch(context.Background())

	require.NoError(t, err)
	assert.True(t, mockRepo.updateSettlementCalled, "DB settlement should be recorded even without contractClient")
}
