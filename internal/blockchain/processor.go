// Package blockchain provides the EventProcessor that connects blockchain events
// to the session state machine.
package blockchain

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
)

// SessionTransitioner defines the interface for session state transitions.
type SessionTransitioner interface {
	TransitionToRunning(ctx context.Context, sessionID string, rentalID uint64, blockNumber uint64, txHash string, startTime time.Time) error
	TransitionToStopped(ctx context.Context, sessionID string, endTime time.Time, totalCost string, txHash string, blockNumber uint64) error
}

// EventProcessor handles blockchain events and updates session state.
// V4: Uses domain.SessionCleanup for K8s-only cleanup.
type EventProcessor struct {
	sessionManager SessionTransitioner
	sessionRepo    domain.RentalSessionRepository
	cleanup        domain.SessionCleanup // K8s cleanup (can be nil)
	logger         *slog.Logger
}

// Compile-time interface verification
var _ EventHandler = (*EventProcessor)(nil)

// NewEventProcessor creates a new event processor with the required dependencies.
func NewEventProcessor(
	sessionManager SessionTransitioner,
	sessionRepo domain.RentalSessionRepository,
	logger *slog.Logger,
) *EventProcessor {
	return &EventProcessor{
		sessionManager: sessionManager,
		sessionRepo:    sessionRepo,
		logger:         logger,
	}
}

// WithCleanup sets the session cleanup handler for Pod deletion on rental stop
func (p *EventProcessor) WithCleanup(cleanup domain.SessionCleanup) *EventProcessor {
	p.cleanup = cleanup
	return p
}

// HandleRentalStarted processes RentalStarted events from the blockchain.
// The actual state transition to RUNNING is handled by ConfirmationWorker.
func (p *EventProcessor) HandleRentalStarted(ctx context.Context, event *RentalStartedEvent) error {
	p.logger.Info("received RentalStarted event (ConfirmationWorker will handle transition)",
		"rentalId", event.RentalID,
		"user", event.User.Hex(),
		"provider", event.Provider.Hex(),
		"block", event.BlockNumber,
	)
	return nil
}

// HandleRentalStopped processes RentalStopped events from the blockchain.
func (p *EventProcessor) HandleRentalStopped(ctx context.Context, event *RentalStoppedEvent) error {
	p.logger.Info("processing RentalStopped",
		"rentalId", event.RentalID,
		"endTime", event.EndTime,
		"cost", event.Cost.String(),
		"block", event.BlockNumber,
	)

	session, err := p.sessionRepo.GetByRentalID(ctx, event.RentalID)
	if err != nil {
		p.logger.Warn("no session found for RentalStopped",
			"rentalId", event.RentalID,
			"error", err,
		)
		return nil
	}

	endTime := time.Unix(int64(event.EndTime), 0)
	err = p.sessionManager.TransitionToStopped(
		ctx,
		session.ID,
		endTime,
		event.Cost.String(),
		event.TxHash.Hex(),
		event.BlockNumber,
	)
	if err != nil {
		return fmt.Errorf("transition to stopped: %w", err)
	}

	p.logger.Info("session transitioned to STOPPED",
		"sessionId", session.ID,
		"rentalId", event.RentalID,
		"cost", event.Cost.String(),
	)

	// Delete K8s Pod via cleanup handler
	if p.cleanup != nil {
		if err := p.cleanup.DeleteSessionContainer(ctx, session); err != nil {
			p.logger.Warn("failed to delete container on rental stop",
				"sessionId", session.ID,
				"error", err,
			)
		} else {
			p.logger.Info("deleted container on rental stop",
				"sessionId", session.ID,
			)
		}
	}

	return nil
}

// HandleDeposited processes Deposited events (no-op for session management).
func (p *EventProcessor) HandleDeposited(ctx context.Context, event *DepositedEvent) error {
	p.logger.Debug("ignoring Deposited event in session processor",
		"user", event.User.Hex(),
		"amount", event.Amount.String(),
		"block", event.BlockNumber,
	)
	return nil
}

// HandleWithdrawn processes Withdrawn events (no-op for session management).
func (p *EventProcessor) HandleWithdrawn(ctx context.Context, event *WithdrawnEvent) error {
	p.logger.Debug("ignoring Withdrawn event in session processor",
		"user", event.User.Hex(),
		"amount", event.Amount.String(),
		"block", event.BlockNumber,
	)
	return nil
}

// findPendingSession finds a PENDING session matching user and provider addresses.
func (p *EventProcessor) findPendingSession(ctx context.Context, userAddr, providerAddr string) (*domain.RentalSession, error) {
	sessionList, err := p.sessionRepo.ListByUser(ctx, userAddr, 10, 0)
	if err != nil {
		return nil, err
	}

	for _, s := range sessionList {
		if s.State == domain.RentalStatePending && s.ProviderAddress == providerAddr {
			return s, nil
		}
	}

	return nil, fmt.Errorf("no pending session found for user %s provider %s", userAddr, providerAddr)
}
