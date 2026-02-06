package settlement

import (
	"context"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
)

func cleanPriceStr(price string) string {
	if idx := strings.Index(price, "."); idx != -1 {
		return price[:idx]
	}
	return price
}

// Calculator handles settlement cost calculations
type Calculator struct {
	sessionRepo domain.RentalSessionRepository
}

// NewCalculator creates a new settlement calculator
func NewCalculator(sessionRepo domain.RentalSessionRepository) *Calculator {
	return &Calculator{sessionRepo: sessionRepo}
}

// CalculateCost calculates the total cost for a rental session
// Uses per-minute billing granularity, rounded up per CONTEXT.md
// Returns cost in Wei (as *big.Int)
func (c *Calculator) CalculateCost(startTime, endTime time.Time, pricePerSecond *big.Int) *big.Int {
	duration := endTime.Sub(startTime)
	if duration <= 0 {
		return big.NewInt(0)
	}

	// Round up to nearest minute per CONTEXT.md
	minutes := int64(math.Ceil(duration.Minutes()))

	// Cost = minutes * 60 * pricePerSecond (convert rate to per-minute)
	secondsInMinutes := big.NewInt(minutes * 60)
	cost := new(big.Int).Mul(secondsInMinutes, pricePerSecond)

	return cost
}

// CalculatePendingSettlement calculates total pending settlement for a user
// Includes:
// - RUNNING sessions: cost from startTime to now
// - STOPPED sessions without settled_at: full session cost
func (c *Calculator) CalculatePendingSettlement(ctx context.Context, userAddress string) (*big.Int, error) {
	// Get RUNNING sessions
	runningSessions, err := c.sessionRepo.FindByUserAndState(ctx, userAddress, domain.RentalStateRunning)
	if err != nil {
		return nil, err
	}

	// Get STOPPED sessions without settlement
	stoppedSessions, err := c.sessionRepo.FindPendingSettlement(ctx, userAddress)
	if err != nil {
		return nil, err
	}

	total := big.NewInt(0)
	now := time.Now()

	// Calculate cost for RUNNING sessions (ongoing)
	for _, s := range runningSessions {
		if s.StartTime == nil {
			continue // Shouldn't happen, but be safe
		}
		pricePerSecond, ok := new(big.Int).SetString(cleanPriceStr(s.PricePerSecond), 10)
		if !ok {
			continue
		}
		cost := c.CalculateCost(*s.StartTime, now, pricePerSecond)
		total.Add(total, cost)
	}

	// Calculate cost for STOPPED sessions pending settlement
	for _, s := range stoppedSessions {
		if s.StartTime == nil || s.EndTime == nil {
			continue
		}
		pricePerSecond, ok := new(big.Int).SetString(cleanPriceStr(s.PricePerSecond), 10)
		if !ok {
			continue
		}
		cost := c.CalculateCost(*s.StartTime, *s.EndTime, pricePerSecond)
		total.Add(total, cost)
	}

	return total, nil
}

// CalculateEstimatedMinutesRemaining estimates how long a user can continue
// based on available balance and current rental rate
func (c *Calculator) CalculateEstimatedMinutesRemaining(availableBalance *big.Int, pricePerSecond *big.Int) int64 {
	if pricePerSecond.Sign() <= 0 {
		return 0
	}

	// Cost per minute = pricePerSecond * 60
	costPerMinute := new(big.Int).Mul(pricePerSecond, big.NewInt(60))

	// Minutes = availableBalance / costPerMinute
	minutes := new(big.Int).Div(availableBalance, costPerMinute)

	return minutes.Int64()
}

// SessionCostBreakdown provides detailed cost information for a session
type SessionCostBreakdown struct {
	SessionID    string
	StartTime    time.Time
	EndTime      *time.Time // nil if still running
	DurationMins int64
	RatePerSec   string
	TotalCost    string // Wei as string
}

// CalculateSessionCost returns breakdown for a single session
func (c *Calculator) CalculateSessionCost(session *domain.RentalSession) *SessionCostBreakdown {
	if session.StartTime == nil {
		return nil
	}

	endTime := session.EndTime
	var end time.Time
	if endTime != nil {
		end = *endTime
	} else {
		end = time.Now()
	}

	duration := end.Sub(*session.StartTime)
	minutes := int64(math.Ceil(duration.Minutes()))

	pricePerSecond, _ := new(big.Int).SetString(cleanPriceStr(session.PricePerSecond), 10)
	cost := c.CalculateCost(*session.StartTime, end, pricePerSecond)

	return &SessionCostBreakdown{
		SessionID:    session.ID,
		StartTime:    *session.StartTime,
		EndTime:      endTime,
		DurationMins: minutes,
		RatePerSec:   session.PricePerSecond,
		TotalCost:    cost.String(),
	}
}
