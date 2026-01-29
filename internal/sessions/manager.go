package sessions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/worldland/worldland-hub/internal/domain"
)

// ErrInvalidTransition is returned when a state transition is not allowed
var ErrInvalidTransition = errors.New("invalid state transition")

// ErrSessionNotFound is returned when a session is not found
var ErrSessionNotFound = errors.New("session not found")

// ErrNodeNotFound is returned when a node is not found
var ErrNodeNotFound = errors.New("node not found")

// ErrProviderNotFound is returned when a provider is not found
var ErrProviderNotFound = errors.New("provider not found")

// SessionManager handles rental session lifecycle and state transitions
type SessionManager struct {
	sessionRepo  domain.RentalSessionRepository
	nodeRepo     domain.NodeRepository
	providerRepo domain.ProviderRepository
}

// NewSessionManager creates a new SessionManager with the required dependencies
func NewSessionManager(
	sessionRepo domain.RentalSessionRepository,
	nodeRepo domain.NodeRepository,
	providerRepo domain.ProviderRepository,
) *SessionManager {
	return &SessionManager{
		sessionRepo:  sessionRepo,
		nodeRepo:     nodeRepo,
		providerRepo: providerRepo,
	}
}

// CreateSession creates a new rental session in PENDING state
// It looks up the node to get the provider address
func (m *SessionManager) CreateSession(ctx context.Context, userAddress, nodeID, pricePerSecond string) (*domain.RentalSession, error) {
	// Look up node to get provider ID
	node, err := m.nodeRepo.GetByID(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNodeNotFound, err)
	}

	// Look up provider to get wallet address
	provider, err := m.providerRepo.GetByID(ctx, node.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderNotFound, err)
	}

	now := time.Now()
	session := &domain.RentalSession{
		ID:              uuid.New().String(),
		UserAddress:     userAddress,
		ProviderAddress: provider.WalletAddress,
		NodeID:          nodeID,
		State:           domain.RentalStatePending,
		PricePerSecond:  pricePerSecond,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := m.sessionRepo.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return session, nil
}

// loadAndValidateTransition loads a session and validates the requested state transition
// Returns the session if transition is valid, error otherwise
func (m *SessionManager) loadAndValidateTransition(ctx context.Context, sessionID string, targetState domain.RentalSessionState) (*domain.RentalSession, error) {
	session, err := m.sessionRepo.GetByID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSessionNotFound, err)
	}

	if !session.CanTransitionTo(targetState) {
		return nil, fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidTransition, session.State, targetState)
	}

	return session, nil
}

// TransitionToRunning transitions a session from PENDING to RUNNING
// Requires rentalID, blockNumber, txHash, and startTime from the RentalStarted blockchain event
func (m *SessionManager) TransitionToRunning(ctx context.Context, sessionID string, rentalID uint64, blockNumber uint64, txHash string, startTime time.Time) error {
	session, err := m.loadAndValidateTransition(ctx, sessionID, domain.RentalStateRunning)
	if err != nil {
		return err
	}

	// Apply transition with required metadata
	session.State = domain.RentalStateRunning
	session.RentalID = &rentalID
	session.BlockNumber = &blockNumber
	session.TxHash = &txHash
	session.StartTime = &startTime
	session.UpdatedAt = time.Now()

	if err := m.sessionRepo.Update(ctx, session); err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	return nil
}

// TransitionToStopped transitions a session from RUNNING to STOPPED
// Called when RentalStopped blockchain event is received
// Includes endTime, totalCost, txHash, and blockNumber from the blockchain event
func (m *SessionManager) TransitionToStopped(ctx context.Context, sessionID string, endTime time.Time, totalCost string, txHash string, blockNumber uint64) error {
	session, err := m.loadAndValidateTransition(ctx, sessionID, domain.RentalStateStopped)
	if err != nil {
		return err
	}

	// Apply transition
	session.State = domain.RentalStateStopped
	session.EndTime = &endTime
	session.TxHash = &txHash
	session.BlockNumber = &blockNumber
	session.UpdatedAt = time.Now()

	if err := m.sessionRepo.Update(ctx, session); err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	return nil
}

// TransitionToFailed transitions a session from PENDING or RUNNING to FAILED
// Called on timeout (PENDING) or node unresponsive (RUNNING)
func (m *SessionManager) TransitionToFailed(ctx context.Context, sessionID string, reason string) error {
	session, err := m.loadAndValidateTransition(ctx, sessionID, domain.RentalStateFailed)
	if err != nil {
		return err
	}

	// Apply transition
	now := time.Now()
	session.State = domain.RentalStateFailed
	session.EndTime = &now
	session.UpdatedAt = now

	if err := m.sessionRepo.Update(ctx, session); err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	return nil
}

// TransitionToCancelled transitions a session from PENDING to CANCELLED
// Only allowed from PENDING state (user cancellation)
func (m *SessionManager) TransitionToCancelled(ctx context.Context, sessionID string) error {
	session, err := m.loadAndValidateTransition(ctx, sessionID, domain.RentalStateCancelled)
	if err != nil {
		return err
	}

	// Apply transition
	now := time.Now()
	session.State = domain.RentalStateCancelled
	session.EndTime = &now
	session.UpdatedAt = now

	if err := m.sessionRepo.Update(ctx, session); err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	return nil
}
