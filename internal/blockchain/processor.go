// Package blockchain provides the EventProcessor that connects blockchain events
// to the session state machine.
package blockchain

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/sessions"
)

// EventProcessor handles blockchain events and updates session state.
// It implements the EventHandler interface to receive events from EventListener.
type EventProcessor struct {
	sessionManager *sessions.SessionManager
	sessionRepo    domain.RentalSessionRepository
	logger         *slog.Logger
}

// Compile-time interface verification
var _ EventHandler = (*EventProcessor)(nil)

// NewEventProcessor creates a new event processor with the required dependencies.
func NewEventProcessor(
	sessionManager *sessions.SessionManager,
	sessionRepo domain.RentalSessionRepository,
	logger *slog.Logger,
) *EventProcessor {
	return &EventProcessor{
		sessionManager: sessionManager,
		sessionRepo:    sessionRepo,
		logger:         logger,
	}
}

// HandleRentalStarted processes RentalStarted events from the blockchain.
// It finds the matching PENDING session by user+provider addresses and
// transitions it to RUNNING state with the blockchain data.
//
// If no matching session is found, the event is logged and skipped gracefully.
// This can happen if the rental was created outside the Hub.
func (p *EventProcessor) HandleRentalStarted(ctx context.Context, event *RentalStartedEvent) error {
	p.logger.Info("processing RentalStarted",
		"rentalId", event.RentalID,
		"user", event.User.Hex(),
		"provider", event.Provider.Hex(),
		"block", event.BlockNumber,
	)

	// Find session by user + provider in PENDING state
	// Note: We match by addresses since rental_id isn't set until this event
	session, err := p.findPendingSession(ctx, event.User.Hex(), event.Provider.Hex())
	if err != nil {
		p.logger.Warn("no pending session found for RentalStarted",
			"user", event.User.Hex(),
			"provider", event.Provider.Hex(),
			"error", err,
		)
		return nil // Don't error - event may be for a session created outside Hub
	}

	// Transition to RUNNING with blockchain data
	startTime := time.Unix(int64(event.StartTime), 0)
	err = p.sessionManager.TransitionToRunning(
		ctx,
		session.ID,
		event.RentalID,
		event.BlockNumber,
		event.TxHash.Hex(),
		startTime,
	)
	if err != nil {
		return fmt.Errorf("transition to running: %w", err)
	}

	p.logger.Info("session transitioned to RUNNING",
		"sessionId", session.ID,
		"rentalId", event.RentalID,
	)
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
