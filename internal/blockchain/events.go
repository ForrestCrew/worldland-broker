package blockchain

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// RentalStartedEvent represents a parsed RentalStarted event from the blockchain.
// This event is emitted when a user starts a rental session with a provider.
type RentalStartedEvent struct {
	RentalID    uint64         // Rental ID from contract
	User        common.Address // User address who initiated rental
	Provider    common.Address // Provider address supplying GPU
	StartTime   uint64         // Block timestamp when rental started
	TxHash      common.Hash    // Transaction hash
	BlockNumber uint64         // Block number where event was emitted
	LogIndex    uint           // Log index within block
}

// RentalStoppedEvent represents a parsed RentalStopped event from the blockchain.
// This event is emitted when a rental session is terminated and settled.
type RentalStoppedEvent struct {
	RentalID    uint64      // Rental ID from contract
	EndTime     uint64      // Block timestamp when rental stopped
	Cost        *big.Int    // Total cost in wei
	TxHash      common.Hash // Transaction hash
	BlockNumber uint64      // Block number where event was emitted
	LogIndex    uint        // Log index within block
}

// DepositedEvent represents a parsed Deposited event from the blockchain.
// This event is emitted when a user deposits tokens into the contract.
type DepositedEvent struct {
	User        common.Address // User who deposited
	Amount      *big.Int       // Amount deposited in wei
	TxHash      common.Hash    // Transaction hash
	BlockNumber uint64         // Block number
	LogIndex    uint           // Log index
}

// WithdrawnEvent represents a parsed Withdrawn event from the blockchain.
// This event is emitted when a user withdraws tokens from the contract.
type WithdrawnEvent struct {
	User        common.Address // User who withdrew
	Amount      *big.Int       // Amount withdrawn in wei
	TxHash      common.Hash    // Transaction hash
	BlockNumber uint64         // Block number
	LogIndex    uint           // Log index
}

// ParseRentalStarted parses a RentalStarted event from a log entry.
// The log must have the correct event signature in Topics[0].
// Indexed parameters (rentalId, user, provider) are in Topics[1:4].
// Non-indexed parameters (startTime) are in Data.
func ParseRentalStarted(log types.Log) (*RentalStartedEvent, error) {
	// Validate topic count (signature + 3 indexed params)
	if len(log.Topics) < 4 {
		return nil, fmt.Errorf("invalid log: expected 4 topics, got %d", len(log.Topics))
	}

	// Validate event signature
	if log.Topics[0] != RentalStartedSig {
		return nil, fmt.Errorf("invalid event signature: expected %s, got %s",
			RentalStartedSig.Hex(), log.Topics[0].Hex())
	}

	// Parse indexed parameters from Topics[1:4]
	rentalID := new(big.Int).SetBytes(log.Topics[1].Bytes())
	user := common.BytesToAddress(log.Topics[2].Bytes())
	provider := common.BytesToAddress(log.Topics[3].Bytes())

	// Validate data length for non-indexed params (startTime = 32 bytes)
	if len(log.Data) < 32 {
		return nil, fmt.Errorf("invalid log data length: expected at least 32 bytes, got %d", len(log.Data))
	}

	// Parse non-indexed startTime from Data
	startTime := new(big.Int).SetBytes(log.Data[:32])

	return &RentalStartedEvent{
		RentalID:    rentalID.Uint64(),
		User:        user,
		Provider:    provider,
		StartTime:   startTime.Uint64(),
		TxHash:      log.TxHash,
		BlockNumber: log.BlockNumber,
		LogIndex:    log.Index,
	}, nil
}

// ParseRentalStopped parses a RentalStopped event from a log entry.
// The log must have the correct event signature in Topics[0].
// Indexed parameter (rentalId) is in Topics[1].
// Non-indexed parameters (endTime, cost) are in Data.
func ParseRentalStopped(log types.Log) (*RentalStoppedEvent, error) {
	// Validate topic count (signature + 1 indexed param)
	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("invalid log: expected 2 topics, got %d", len(log.Topics))
	}

	// Validate event signature
	if log.Topics[0] != RentalStoppedSig {
		return nil, fmt.Errorf("invalid event signature: expected %s, got %s",
			RentalStoppedSig.Hex(), log.Topics[0].Hex())
	}

	// Parse indexed rentalID from Topics[1]
	rentalID := new(big.Int).SetBytes(log.Topics[1].Bytes())

	// Validate data length for non-indexed params (endTime + cost = 64 bytes)
	if len(log.Data) < 64 {
		return nil, fmt.Errorf("invalid log data length: expected at least 64 bytes, got %d", len(log.Data))
	}

	// Parse non-indexed params from Data
	endTime := new(big.Int).SetBytes(log.Data[:32])
	cost := new(big.Int).SetBytes(log.Data[32:64])

	return &RentalStoppedEvent{
		RentalID:    rentalID.Uint64(),
		EndTime:     endTime.Uint64(),
		Cost:        cost,
		TxHash:      log.TxHash,
		BlockNumber: log.BlockNumber,
		LogIndex:    log.Index,
	}, nil
}

// ParseDeposited parses a Deposited event from a log entry.
func ParseDeposited(log types.Log) (*DepositedEvent, error) {
	// Validate topic count (signature + 1 indexed param)
	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("invalid log: expected 2 topics, got %d", len(log.Topics))
	}

	// Validate event signature
	if log.Topics[0] != DepositedSig {
		return nil, fmt.Errorf("invalid event signature: expected %s, got %s",
			DepositedSig.Hex(), log.Topics[0].Hex())
	}

	// Parse indexed user from Topics[1]
	user := common.BytesToAddress(log.Topics[1].Bytes())

	// Validate data length for amount (32 bytes)
	if len(log.Data) < 32 {
		return nil, fmt.Errorf("invalid log data length: expected at least 32 bytes, got %d", len(log.Data))
	}

	// Parse amount from Data
	amount := new(big.Int).SetBytes(log.Data[:32])

	return &DepositedEvent{
		User:        user,
		Amount:      amount,
		TxHash:      log.TxHash,
		BlockNumber: log.BlockNumber,
		LogIndex:    log.Index,
	}, nil
}

// ParseWithdrawn parses a Withdrawn event from a log entry.
func ParseWithdrawn(log types.Log) (*WithdrawnEvent, error) {
	// Validate topic count (signature + 1 indexed param)
	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("invalid log: expected 2 topics, got %d", len(log.Topics))
	}

	// Validate event signature
	if log.Topics[0] != WithdrawnSig {
		return nil, fmt.Errorf("invalid event signature: expected %s, got %s",
			WithdrawnSig.Hex(), log.Topics[0].Hex())
	}

	// Parse indexed user from Topics[1]
	user := common.BytesToAddress(log.Topics[1].Bytes())

	// Validate data length for amount (32 bytes)
	if len(log.Data) < 32 {
		return nil, fmt.Errorf("invalid log data length: expected at least 32 bytes, got %d", len(log.Data))
	}

	// Parse amount from Data
	amount := new(big.Int).SetBytes(log.Data[:32])

	return &WithdrawnEvent{
		User:        user,
		Amount:      amount,
		TxHash:      log.TxHash,
		BlockNumber: log.BlockNumber,
		LogIndex:    log.Index,
	}, nil
}

// EventType represents the type of blockchain event.
type EventType string

const (
	EventTypeRentalStarted EventType = "RentalStarted"
	EventTypeRentalStopped EventType = "RentalStopped"
	EventTypeDeposited     EventType = "Deposited"
	EventTypeWithdrawn     EventType = "Withdrawn"
	EventTypeUnknown       EventType = "unknown"
)

// IdentifyEvent returns the event type based on the topic signature.
// Returns EventTypeUnknown if the signature is not recognized.
func IdentifyEvent(log types.Log) EventType {
	if len(log.Topics) == 0 {
		return EventTypeUnknown
	}

	switch log.Topics[0] {
	case RentalStartedSig:
		return EventTypeRentalStarted
	case RentalStoppedSig:
		return EventTypeRentalStopped
	case DepositedSig:
		return EventTypeDeposited
	case WithdrawnSig:
		return EventTypeWithdrawn
	default:
		return EventTypeUnknown
	}
}

// IsRentalEvent checks if a log is a rental-related event (Started or Stopped).
func IsRentalEvent(log types.Log) bool {
	if len(log.Topics) == 0 {
		return false
	}
	sig := log.Topics[0]
	return sig == RentalStartedSig || sig == RentalStoppedSig
}

// ParseEvent parses any known event from a log entry.
// Returns the parsed event and its type, or an error if parsing fails.
func ParseEvent(log types.Log) (interface{}, EventType, error) {
	eventType := IdentifyEvent(log)

	switch eventType {
	case EventTypeRentalStarted:
		event, err := ParseRentalStarted(log)
		return event, eventType, err
	case EventTypeRentalStopped:
		event, err := ParseRentalStopped(log)
		return event, eventType, err
	case EventTypeDeposited:
		event, err := ParseDeposited(log)
		return event, eventType, err
	case EventTypeWithdrawn:
		event, err := ParseWithdrawn(log)
		return event, eventType, err
	default:
		return nil, EventTypeUnknown, fmt.Errorf("unknown event type")
	}
}
