package sessions

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/worldland/worldland-hub/internal/blockchain"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/images"
)

// ErrInvalidTransition is returned when a state transition is not allowed
var ErrInvalidTransition = errors.New("invalid state transition")

// ErrSessionNotFound is returned when a session is not found
var ErrSessionNotFound = errors.New("session not found")

// ErrNodeNotFound is returned when a node is not found
var ErrNodeNotFound = errors.New("node not found")

// ErrProviderNotFound is returned when a provider is not found
var ErrProviderNotFound = errors.New("provider not found")

// ErrSessionNotRunning is returned when attempting to extend a non-running session
var ErrSessionNotRunning = errors.New("session not running")

// ErrInsufficientBalance is returned when user has insufficient deposit balance
var ErrInsufficientBalance = errors.New("insufficient balance for extension")

// ErrExtensionLimitReached is returned when session has reached maximum extensions
var ErrExtensionLimitReached = errors.New("maximum extension limit reached")

// ErrMinimumDuration is returned when extension duration is below minimum
var ErrMinimumDuration = errors.New("extension duration below minimum")

// ErrInvalidImage is returned when an invalid docker image is specified
var ErrInvalidImage = errors.New("invalid docker image")

// ErrImageNotFound is returned when a preset image ID is not found
var ErrImageNotFound = errors.New("preset image not found")

const (
	// MinExtensionMinutes is the minimum allowed extension duration
	MinExtensionMinutes = 30
	// MaxExtensions is the maximum number of times a session can be extended
	MaxExtensions = 10
)

// ExtensionResult contains the result of a session extension
type ExtensionResult struct {
	NewExpiration    time.Time
	ExtensionMinutes int
	ExtensionCost    *big.Int
	RemainingBalance *big.Int
	ExtensionCount   int
}

// SessionManager handles rental session lifecycle and state transitions
type SessionManager struct {
	sessionRepo      domain.RentalSessionRepository
	nodeRepo         domain.NodeRepository
	providerRepo     domain.ProviderRepository
	balanceValidator blockchain.BalanceValidatorInterface
	// Optional dependencies for image selection (24-03)
	imageRepo      domain.ImageRepository
	imageValidator *images.ImageValidator
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

// WithImageRepository sets the optional ImageRepository for preset image resolution
func (m *SessionManager) WithImageRepository(repo domain.ImageRepository) *SessionManager {
	m.imageRepo = repo
	return m
}

// WithImageValidator sets the optional ImageValidator for custom image validation
func (m *SessionManager) WithImageValidator(validator *images.ImageValidator) *SessionManager {
	m.imageValidator = validator
	return m
}

// CreateSession creates a new rental session in PENDING state
// It looks up the node to get the provider address
// The dockerImage parameter can be:
//   - Empty string: uses domain.DefaultImage
//   - UUID: resolved to docker_image via ImageRepository (preset ID)
//   - Custom image URL: validated by ImageValidator
func (m *SessionManager) CreateSession(ctx context.Context, userAddress, nodeID, pricePerSecond, dockerImage string) (*domain.RentalSession, error) {
	// Resolve docker image (24-03)
	resolvedImage, err := m.resolveDockerImage(ctx, dockerImage)
	if err != nil {
		return nil, err
	}

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
		DockerImage:     resolvedImage,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := m.sessionRepo.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return session, nil
}

// resolveDockerImage resolves the docker image from user input.
// Returns the resolved image reference or error.
func (m *SessionManager) resolveDockerImage(ctx context.Context, dockerImage string) (string, error) {
	// If empty, use default image
	if dockerImage == "" {
		return domain.DefaultImage, nil
	}

	// Check if it's a preset ID (UUID)
	if images.IsPresetID(dockerImage) {
		// Look up preset in image repository
		if m.imageRepo == nil {
			return "", fmt.Errorf("%w: image repository not configured", ErrImageNotFound)
		}
		preset, err := m.imageRepo.GetByID(ctx, dockerImage)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrImageNotFound, err)
		}
		return preset.DockerImage, nil
	}

	// Validate custom image format
	if m.imageValidator != nil {
		if err := m.imageValidator.ValidateFormat(dockerImage); err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidImage, err)
		}
	} else {
		// Fallback to package-level validation if no validator configured
		if err := images.ValidateImageFormat(dockerImage); err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidImage, err)
		}
	}

	return dockerImage, nil
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
	session.SettledAmount = totalCost // Save settlement cost from blockchain event
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

// ExtendSession extends a running session by the specified duration
// Validates state, balance, extension limits, and duration before extending
func (m *SessionManager) ExtendSession(
	ctx context.Context,
	sessionID string,
	extensionMinutes int,
	idempotencyKey string,
	userAddress string,
) (*ExtensionResult, error) {
	// 1. Validate duration
	if extensionMinutes < MinExtensionMinutes {
		return nil, fmt.Errorf("%w: minimum is %d minutes", ErrMinimumDuration, MinExtensionMinutes)
	}

	// 2. Load session
	session, err := m.sessionRepo.GetByID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSessionNotFound, err)
	}

	// 3. Validate state
	if session.State != domain.RentalStateRunning {
		return nil, fmt.Errorf("%w: current state is %s", ErrSessionNotRunning, session.State)
	}

	// 4. Validate extension limit
	if session.ExtensionCount >= MaxExtensions {
		return nil, fmt.Errorf("%w: limit is %d", ErrExtensionLimitReached, MaxExtensions)
	}

	// 5. Calculate costs
	pricePerSecond, ok := new(big.Int).SetString(cleanPrice(session.PricePerSecond), 10)
	if !ok {
		return nil, fmt.Errorf("invalid price per second: %s", session.PricePerSecond)
	}
	extensionSeconds := big.NewInt(int64(extensionMinutes * 60))
	extensionCost := new(big.Int).Mul(pricePerSecond, extensionSeconds)

	// Calculate current running cost
	var currentCost *big.Int
	if session.StartTime != nil {
		runningSeconds := big.NewInt(int64(time.Since(*session.StartTime).Seconds()))
		currentCost = new(big.Int).Mul(pricePerSecond, runningSeconds)
	} else {
		currentCost = big.NewInt(0)
	}
	totalRequired := new(big.Int).Add(currentCost, extensionCost)

	// 6. Validate balance
	var remainingBalance *big.Int
	if m.balanceValidator != nil {
		userAddr := common.HexToAddress(userAddress)
		hasSufficient, balance, err := m.balanceValidator.ValidateDepositBalance(ctx, userAddr, totalRequired)
		if err != nil {
			return nil, fmt.Errorf("failed to check balance: %w", err)
		}
		remainingBalance = balance
		if !hasSufficient {
			shortfall := new(big.Int).Sub(totalRequired, balance)
			return nil, fmt.Errorf("%w: need %s more", ErrInsufficientBalance, shortfall.String())
		}
	}

	// 7. Calculate new expiration (add to existing, not current time)
	baseTime := time.Now()
	if session.ExtendedUntil != nil && session.ExtendedUntil.After(baseTime) {
		baseTime = *session.ExtendedUntil
	}
	newExpiration := baseTime.Add(time.Duration(extensionMinutes) * time.Minute)

	// Store current count before update (UpdateExtension increments it atomically)
	newExtensionCount := session.ExtensionCount + 1

	// 8. Update session (atomic with optimistic locking)
	err = m.sessionRepo.UpdateExtension(ctx, sessionID, newExpiration, extensionMinutes)
	if err != nil {
		return nil, fmt.Errorf("failed to update extension: %w", err)
	}

	// 9. Create audit record
	if idempotencyKey != "" {
		_, _ = m.sessionRepo.CreateExtensionRecord(ctx, sessionID, extensionMinutes, extensionCost.String(), idempotencyKey)
	}

	return &ExtensionResult{
		NewExpiration:    newExpiration,
		ExtensionMinutes: extensionMinutes,
		ExtensionCost:    extensionCost,
		RemainingBalance: remainingBalance,
		ExtensionCount:   newExtensionCount,
	}, nil
}
