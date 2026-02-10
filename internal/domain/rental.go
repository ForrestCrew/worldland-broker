package domain

import "time"

// RentalSessionState represents the lifecycle state of a GPU rental session
type RentalSessionState string

const (
	// RentalStatePending indicates session created, waiting for blockchain RentalStarted event
	RentalStatePending RentalSessionState = "PENDING"
	// RentalStateRunning indicates RentalStarted confirmed, GPU actively rented
	RentalStateRunning RentalSessionState = "RUNNING"
	// RentalStateStopped indicates RentalStopped confirmed, rental completed successfully
	RentalStateStopped RentalSessionState = "STOPPED"
	// RentalStateFailed indicates timeout or error (Node unresponsive, PENDING timeout)
	RentalStateFailed RentalSessionState = "FAILED"
	// RentalStateCancelled indicates user cancelled while PENDING
	RentalStateCancelled RentalSessionState = "CANCELLED"
)

// allowedTransitions defines valid state transitions for RentalSession
// PENDING can transition to RUNNING (rental started), FAILED (timeout), or CANCELLED (user cancelled)
// RUNNING can transition to STOPPED (normal end) or FAILED (error)
// STOPPED, FAILED, CANCELLED are terminal states with no outgoing transitions
var allowedTransitions = map[RentalSessionState][]RentalSessionState{
	RentalStatePending: {RentalStateRunning, RentalStateFailed, RentalStateCancelled},
	RentalStateRunning: {RentalStateStopped, RentalStateFailed},
	RentalStateStopped:   {},
	RentalStateFailed:    {},
	RentalStateCancelled: {},
}

// RentalSession represents a GPU rental session tracking the lifecycle from request to completion
// This is distinct from the auth Session used for provider authentication
type RentalSession struct {
	ID              string             `json:"id"`                        // UUID
	UserAddress     string             `json:"userAddress"`               // Ethereum address of renter
	ProviderAddress string             `json:"providerAddress"`           // Ethereum address of provider
	NodeID          string             `json:"nodeId"`                    // References Node
	RentalID        *uint64            `json:"rentalId,omitempty"`        // On-chain rental ID, null until RentalStarted
	State           RentalSessionState `json:"state"`                     // Current lifecycle state
	PricePerSecond  string             `json:"pricePerSecond"`            // Decimal string for Wei precision
	StartTime       *time.Time         `json:"startTime,omitempty"`       // Set when state becomes RUNNING
	EndTime         *time.Time         `json:"endTime,omitempty"`         // Set when reaching terminal state
	TxHash          *string            `json:"txHash,omitempty"`          // Blockchain transaction hash
	BlockNumber          *uint64            `json:"blockNumber,omitempty"`          // Block where state was confirmed
	SettledAt            *time.Time         `json:"settledAt,omitempty"`            // Set when settlement processed (04-07)
	SettledAmount        string             `json:"settledAmount,omitempty"`        // Calculated cost in Wei (04-07)
	DeletedAt            *time.Time         `json:"deletedAt,omitempty"`            // Set when soft-deleted (TTL cleanup)
	ExtendedUntil        *time.Time         `json:"extendedUntil,omitempty"`        // Hub-managed expiration time (16-01)
	ExtensionCount       int                `json:"extensionCount"`                 // Number of times extended (16-01)
	TotalExtendedMinutes int                `json:"totalExtendedMinutes"`           // Total extension duration in minutes (16-01)
	DockerImage          string             `json:"dockerImage,omitempty"`          // Container image for GPU workload (24-01)
	// V4: RunPod-style resource selection fields
	GPUCount             int                `json:"gpuCount"`                       // Number of GPUs requested (default 1)
	CPUCores             int                `json:"cpuCores"`                       // CPU cores requested (default 4)
	MemoryGB             int                `json:"memoryGB"`                       // Memory in GB requested (default 16)
	StorageGB            int                `json:"storageGB"`                      // Storage in GB requested (default 20)
	CreatedAt            time.Time          `json:"createdAt"`
	UpdatedAt            time.Time          `json:"updatedAt"`
}

// CanTransitionTo checks if the session can transition from its current state to newState
func (s *RentalSession) CanTransitionTo(newState RentalSessionState) bool {
	allowed, exists := allowedTransitions[s.State]
	if !exists {
		return false
	}
	for _, state := range allowed {
		if state == newState {
			return true
		}
	}
	return false
}

// IsTerminal returns true if the session is in a terminal state
func (s *RentalSession) IsTerminal() bool {
	return s.State == RentalStateStopped ||
		s.State == RentalStateFailed ||
		s.State == RentalStateCancelled
}

// ValidStates returns all valid RentalSessionState values
func ValidStates() []RentalSessionState {
	return []RentalSessionState{
		RentalStatePending,
		RentalStateRunning,
		RentalStateStopped,
		RentalStateFailed,
		RentalStateCancelled,
	}
}
