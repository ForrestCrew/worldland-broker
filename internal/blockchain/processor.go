// Package blockchain provides the EventProcessor that connects blockchain events
// to the session state machine.
package blockchain

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/k8s"
)

// SessionTransitioner defines the interface for session state transitions.
// This allows the EventProcessor to work with either the real SessionManager
// or a mock for testing.
type SessionTransitioner interface {
	TransitionToRunning(ctx context.Context, sessionID string, rentalID uint64, blockNumber uint64, txHash string, startTime time.Time) error
	TransitionToStopped(ctx context.Context, sessionID string, endTime time.Time, totalCost string, txHash string, blockNumber uint64) error
}

// EventProcessor handles blockchain events and updates session state.
// It implements the EventHandler interface to receive events from EventListener.
type EventProcessor struct {
	sessionManager SessionTransitioner
	sessionRepo    domain.RentalSessionRepository
	jobManager     *k8s.JobManager      // K8s job manager for Pod cleanup (can be nil, legacy)
	cleanup        domain.SessionCleanup // Phase 3: provider-type-aware cleanup (can be nil)
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

// WithK8s sets the K8s JobManager for Pod cleanup on rental stop (legacy)
func (p *EventProcessor) WithK8s(jobManager *k8s.JobManager) *EventProcessor {
	p.jobManager = jobManager
	return p
}

// WithCleanup sets the provider-type-aware session cleanup (Phase 3)
func (p *EventProcessor) WithCleanup(cleanup domain.SessionCleanup) *EventProcessor {
	p.cleanup = cleanup
	return p
}

// HandleRentalStarted processes RentalStarted events from the blockchain.
// It logs the event for debugging purposes. The actual state transition to RUNNING
// is handled by ConfirmationWorker, which also creates the K8s Pod.
//
// NOTE: We intentionally do NOT transition to RUNNING here because:
// 1. ConfirmationWorker needs to create the K8s Pod before transitioning
// 2. EventProcessor receiving events faster than ConfirmationWorker would cause
//    sessions to be RUNNING without a Pod, leading to timeout failures
func (p *EventProcessor) HandleRentalStarted(ctx context.Context, event *RentalStartedEvent) error {
	p.logger.Info("received RentalStarted event (ConfirmationWorker will handle transition)",
		"rentalId", event.RentalID,
		"user", event.User.Hex(),
		"provider", event.Provider.Hex(),
		"block", event.BlockNumber,
	)
	// ConfirmationWorker will verify txHash, create K8s Pod, and transition to RUNNING
	return nil
}

// HandleRentalStopped processes RentalStopped events from the blockchain.
// It finds the session by rental_id (set during RentalStarted) and
// transitions it to STOPPED state with the end time and cost.
//
// If no matching session is found, the event is logged and skipped gracefully.
// This can happen if the rental was created outside the Hub.
func (p *EventProcessor) HandleRentalStopped(ctx context.Context, event *RentalStoppedEvent) error {
	p.logger.Info("processing RentalStopped",
		"rentalId", event.RentalID,
		"endTime", event.EndTime,
		"cost", event.Cost.String(),
		"block", event.BlockNumber,
	)

	// Find session by rental_id (set during RentalStarted)
	session, err := p.sessionRepo.GetByRentalID(ctx, event.RentalID)
	if err != nil {
		p.logger.Warn("no session found for RentalStopped",
			"rentalId", event.RentalID,
			"error", err,
		)
		return nil // Don't error - rental may have been created outside Hub
	}

	// Transition to STOPPED with end time and cost
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

	// Phase 3: Delete container via provider-type-aware cleanup
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
	} else if p.jobManager != nil {
		// Legacy: Delete K8s Pod (idempotent - ok if already deleted)
		if err := p.jobManager.DeleteGPUSession(ctx, session.UserAddress, session.ID); err != nil {
			p.logger.Warn("failed to delete K8s pod on rental stop",
				"sessionId", session.ID,
				"error", err,
			)
		} else {
			p.logger.Info("deleted K8s pod on rental stop",
				"sessionId", session.ID,
			)
		}
	}

	return nil
}

// HandleDeposited processes Deposited events from the blockchain.
// The EventProcessor for session management does not handle deposit events,
// as they don't affect session state. This is a no-op implementation.
// The IndexerProcessor in internal/indexer handles deposit event storage.
func (p *EventProcessor) HandleDeposited(ctx context.Context, event *DepositedEvent) error {
	p.logger.Debug("ignoring Deposited event in session processor",
		"user", event.User.Hex(),
		"amount", event.Amount.String(),
		"block", event.BlockNumber,
	)
	return nil
}

// HandleWithdrawn processes Withdrawn events from the blockchain.
// The EventProcessor for session management does not handle withdraw events,
// as they don't affect session state. This is a no-op implementation.
// The IndexerProcessor in internal/indexer handles withdraw event storage.
func (p *EventProcessor) HandleWithdrawn(ctx context.Context, event *WithdrawnEvent) error {
	p.logger.Debug("ignoring Withdrawn event in session processor",
		"user", event.User.Hex(),
		"amount", event.Amount.String(),
		"block", event.BlockNumber,
	)
	return nil
}

// findPendingSession finds a PENDING session matching user and provider addresses.
// Returns the first matching session or an error if none found.
func (p *EventProcessor) findPendingSession(ctx context.Context, userAddr, providerAddr string) (*domain.RentalSession, error) {
	// Get recent PENDING sessions for this user
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
