package settlement

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/worldland/worldland-hub/internal/domain"
)

func TestCalculateCost_ExactMinutes(t *testing.T) {
	calc := NewCalculator(nil) // No repo needed for pure calculation

	start := time.Now()
	end := start.Add(10 * time.Minute) // Exactly 10 minutes
	pricePerSecond := big.NewInt(1000000000000000) // 0.001 ETH/sec

	cost := calc.CalculateCost(start, end, pricePerSecond)

	// 10 minutes = 600 seconds
	// 600 * 0.001 ETH = 0.6 ETH = 600000000000000000 Wei
	expected := big.NewInt(600000000000000000)
	assert.Equal(t, 0, cost.Cmp(expected))
}

func TestCalculateCost_RoundsUpToMinute(t *testing.T) {
	calc := NewCalculator(nil)

	start := time.Now()
	end := start.Add(1*time.Minute + 30*time.Second) // 1.5 minutes -> rounds to 2
	pricePerSecond := big.NewInt(1000000000000000)

	cost := calc.CalculateCost(start, end, pricePerSecond)

	// 2 minutes = 120 seconds (rounded up)
	expected := big.NewInt(120000000000000000)
	assert.Equal(t, 0, cost.Cmp(expected))
}

func TestCalculateCost_ZeroDuration(t *testing.T) {
	calc := NewCalculator(nil)

	now := time.Now()
	cost := calc.CalculateCost(now, now, big.NewInt(1000000000000000))

	assert.Equal(t, 0, cost.Cmp(big.NewInt(0)))
}

func TestCalculateCost_NegativeDuration(t *testing.T) {
	calc := NewCalculator(nil)

	start := time.Now()
	end := start.Add(-5 * time.Minute) // Negative
	cost := calc.CalculateCost(start, end, big.NewInt(1000000000000000))

	assert.Equal(t, 0, cost.Cmp(big.NewInt(0)))
}

func TestCalculateEstimatedMinutesRemaining(t *testing.T) {
	calc := NewCalculator(nil)

	// 1 ETH available, 0.001 ETH/sec rate
	available := big.NewInt(1000000000000000000)  // 1 ETH
	ratePerSec := big.NewInt(1000000000000000)    // 0.001 ETH/sec

	// 1 ETH / (0.001 * 60) = 1 ETH / 0.06 ETH/min = 16.67 minutes -> 16
	minutes := calc.CalculateEstimatedMinutesRemaining(available, ratePerSec)

	assert.Equal(t, int64(16), minutes)
}

func TestCalculatePendingSettlement_RunningSessions(t *testing.T) {
	// Use fixed time to avoid timing issues
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	startTime := now.Add(-5 * time.Minute)

	// Create mock session repo with RUNNING session
	mockRepo := &mockSessionRepo{
		runningSessions: []*domain.RentalSession{
			{
				ID:             "session-1",
				State:          domain.RentalStateRunning,
				StartTime:      &startTime,
				PricePerSecond: "1000000000000000",
			},
		},
	}

	calc := NewCalculator(mockRepo)
	// Override time.Now() isn't possible in Go, so we'll calculate based on actual elapsed time
	pending, err := calc.CalculatePendingSettlement(context.Background(), "0xUser")

	assert.NoError(t, err)
	// Should be at least 5 minutes (300 seconds) worth of cost
	// 300 * 1000000000000000 = 300000000000000000
	minExpected := big.NewInt(300000000000000000)
	assert.GreaterOrEqual(t, pending.Cmp(minExpected), 0, "pending should be >= 5 minutes cost")
}

func TestCalculatePendingSettlement_StoppedUnSettled(t *testing.T) {
	// Use fixed times for deterministic testing
	start := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	end := start.Add(8 * time.Minute) // Exactly 8 minutes

	mockRepo := &mockSessionRepo{
		stoppedSessions: []*domain.RentalSession{
			{
				ID:             "session-2",
				State:          domain.RentalStateStopped,
				StartTime:      &start,
				EndTime:        &end,
				PricePerSecond: "1000000000000000",
			},
		},
	}

	calc := NewCalculator(mockRepo)
	pending, err := calc.CalculatePendingSettlement(context.Background(), "0xUser")

	assert.NoError(t, err)
	// 8 minutes = 480 seconds
	expected := big.NewInt(480000000000000000)
	assert.Equal(t, 0, pending.Cmp(expected))
}

// mockSessionRepo implements domain.RentalSessionRepository for testing
type mockSessionRepo struct {
	domain.RentalSessionRepository
	runningSessions []*domain.RentalSession
	stoppedSessions []*domain.RentalSession
}

func (m *mockSessionRepo) FindByUserAndState(ctx context.Context, user string, state domain.RentalSessionState) ([]*domain.RentalSession, error) {
	if state == domain.RentalStateRunning {
		return m.runningSessions, nil
	}
	return nil, nil
}

func (m *mockSessionRepo) FindPendingSettlement(ctx context.Context, user string) ([]*domain.RentalSession, error) {
	return m.stoppedSessions, nil
}

func (m *mockSessionRepo) UpdateSSHInfo(ctx context.Context, sessionID, host string, port int32, user, password string) error {
	return nil
}

func (m *mockSessionRepo) LoadRunningSSHInfo(ctx context.Context) (map[string]*domain.SSHConnectionInfo, error) {
	return nil, nil
}

func timePtr(t time.Time) *time.Time {
	return &t
}
