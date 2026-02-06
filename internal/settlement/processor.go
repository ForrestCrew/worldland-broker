package settlement

import (
	"context"
	"log/slog"
	"math/big"
	"sync"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
)

// ContractTransferer is an interface for blockchain settlement transfers (DEBT-03)
// This allows for easy mocking in tests and optional blockchain integration
type ContractTransferer interface {
	// TransferSettlement transfers funds from user to provider on-chain
	// from: user wallet address (0x...)
	// to: provider wallet address (0x...)
	// amount: settlement amount in wei
	TransferSettlement(ctx context.Context, from, to string, amount *big.Int) error
}

// BatchProcessor runs hourly batch settlement
type BatchProcessor struct {
	calculator     *Calculator
	sessionRepo    domain.RentalSessionRepository
	contractClient ContractTransferer // Optional: nil means blockchain transfer disabled
	logger         *slog.Logger

	interval time.Duration
	ticker   *time.Ticker
	done     chan struct{}
	wg       sync.WaitGroup
}

// NewBatchProcessor creates a batch processor
// contractClient is optional - pass nil to disable blockchain transfers
func NewBatchProcessor(
	calculator *Calculator,
	sessionRepo domain.RentalSessionRepository,
	contractClient ContractTransferer,
	logger *slog.Logger,
) *BatchProcessor {
	return &BatchProcessor{
		calculator:     calculator,
		sessionRepo:    sessionRepo,
		contractClient: contractClient,
		logger:         logger,
		interval:       1 * time.Hour, // Per CONTEXT.md
	}
}

// SetInterval allows changing interval for testing
func (p *BatchProcessor) SetInterval(d time.Duration) {
	p.interval = d
}

// Start begins the background settlement loop
func (p *BatchProcessor) Start(ctx context.Context) {
	p.ticker = time.NewTicker(p.interval)
	p.done = make(chan struct{})

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.logger.Info("batch settlement processor started", "interval", p.interval)

		for {
			select {
			case <-ctx.Done():
				p.ticker.Stop()
				p.logger.Info("batch settlement processor stopping (context cancelled)")
				return
			case <-p.done:
				p.ticker.Stop()
				p.logger.Info("batch settlement processor stopped")
				return
			case <-p.ticker.C:
				if err := p.ProcessBatch(ctx); err != nil {
					p.logger.Error("batch settlement failed", "error", err)
				}
			}
		}
	}()
}

// Stop signals the processor to stop
func (p *BatchProcessor) Stop() {
	if p.done != nil {
		close(p.done)
		p.wg.Wait()
	}
}

// ProcessBatch processes all pending settlements
func (p *BatchProcessor) ProcessBatch(ctx context.Context) error {
	p.logger.Info("processing batch settlement")

	// Find all STOPPED sessions without settlement
	sessions, err := p.sessionRepo.FindAllPendingSettlement(ctx)
	if err != nil {
		return err
	}

	if len(sessions) == 0 {
		p.logger.Debug("no pending settlements")
		return nil
	}

	p.logger.Info("found sessions for settlement", "count", len(sessions))

	var settled, failed int
	for _, session := range sessions {
		if err := p.settleSession(ctx, session); err != nil {
			p.logger.Error("failed to settle session",
				"sessionID", session.ID,
				"error", err,
			)
			failed++
			continue // Continue with other sessions per 03-08 pattern
		}
		settled++
	}

	p.logger.Info("batch settlement complete",
		"settled", settled,
		"failed", failed,
	)

	return nil
}

// settleSession calculates and records settlement for a single session
func (p *BatchProcessor) settleSession(ctx context.Context, session *domain.RentalSession) error {
	if session.StartTime == nil || session.EndTime == nil {
		return nil // Can't settle without times
	}

	// Calculate cost
	pricePerSecond, ok := new(big.Int).SetString(cleanPriceStr(session.PricePerSecond), 10)
	if !ok {
		return nil // Invalid price, skip
	}

	cost := p.calculator.CalculateCost(*session.StartTime, *session.EndTime, pricePerSecond)

	p.logger.Debug("settling session",
		"sessionID", session.ID,
		"duration", session.EndTime.Sub(*session.StartTime),
		"cost", cost.String(),
	)

	// Update session with settlement
	now := time.Now()
	session.SettledAt = &now
	session.SettledAmount = cost.String()

	if err := p.sessionRepo.UpdateSettlement(ctx, session.ID, cost.String(), now); err != nil {
		return err
	}

	// Blockchain settlement transfer (DEBT-03)
	// This is NON-BLOCKING: DB is the source of truth. Blockchain transfer failures
	// are logged but don't fail the settlement. Providers can still be paid via
	// later reconciliation if blockchain transfer fails due to network issues.
	if p.contractClient != nil {
		if err := p.contractClient.TransferSettlement(ctx, session.UserAddress, session.ProviderAddress, cost); err != nil {
			p.logger.Error("blockchain settlement transfer failed",
				"sessionID", session.ID,
				"userAddress", session.UserAddress,
				"providerAddress", session.ProviderAddress,
				"amount", cost.String(),
				"error", err,
			)
			// Do NOT return error - DB settlement succeeded, blockchain is best-effort
		} else {
			p.logger.Info("settlement transferred on-chain",
				"sessionID", session.ID,
				"providerAddress", session.ProviderAddress,
				"amount", cost.String(),
			)
		}
	}

	return nil
}
