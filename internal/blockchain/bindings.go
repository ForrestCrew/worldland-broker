// Package blockchain provides smart contract bindings and event parsing
// for the WorldlandRental contract.
package blockchain

import (
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// WorldlandRentalABIJSON is the ABI for the WorldlandRental contract.
// Focused on events needed for Hub session synchronization.
const WorldlandRentalABIJSON = `[
	{
		"anonymous": false,
		"inputs": [
			{"indexed": true, "name": "rentalId", "type": "uint256"},
			{"indexed": true, "name": "user", "type": "address"},
			{"indexed": true, "name": "provider", "type": "address"},
			{"indexed": false, "name": "startTime", "type": "uint256"}
		],
		"name": "RentalStarted",
		"type": "event"
	},
	{
		"anonymous": false,
		"inputs": [
			{"indexed": true, "name": "rentalId", "type": "uint256"},
			{"indexed": false, "name": "endTime", "type": "uint256"},
			{"indexed": false, "name": "cost", "type": "uint256"}
		],
		"name": "RentalStopped",
		"type": "event"
	},
	{
		"anonymous": false,
		"inputs": [
			{"indexed": true, "name": "user", "type": "address"},
			{"indexed": false, "name": "amount", "type": "uint256"}
		],
		"name": "Deposited",
		"type": "event"
	},
	{
		"anonymous": false,
		"inputs": [
			{"indexed": true, "name": "user", "type": "address"},
			{"indexed": false, "name": "amount", "type": "uint256"}
		],
		"name": "Withdrawn",
		"type": "event"
	}
]`

// Event signature hashes (keccak256 of event signature)
// These are computed at init time for efficiency.
var (
	// RentalStartedSig is the keccak256 hash of "RentalStarted(uint256,address,address,uint256)"
	RentalStartedSig common.Hash

	// RentalStoppedSig is the keccak256 hash of "RentalStopped(uint256,uint256,uint256)"
	RentalStoppedSig common.Hash

	// DepositedSig is the keccak256 hash of "Deposited(address,uint256)"
	DepositedSig common.Hash

	// WithdrawnSig is the keccak256 hash of "Withdrawn(address,uint256)"
	WithdrawnSig common.Hash
)

func init() {
	// Compute event signatures using keccak256
	// Event signature = keccak256(EventName(type1,type2,...))
	RentalStartedSig = crypto.Keccak256Hash([]byte("RentalStarted(uint256,address,address,uint256)"))
	RentalStoppedSig = crypto.Keccak256Hash([]byte("RentalStopped(uint256,uint256,uint256)"))
	DepositedSig = crypto.Keccak256Hash([]byte("Deposited(address,uint256)"))
	WithdrawnSig = crypto.Keccak256Hash([]byte("Withdrawn(address,uint256)"))
}

// ContractABI returns the parsed ABI for WorldlandRental.
// The ABI is needed for advanced event parsing and contract interaction.
func ContractABI() (abi.ABI, error) {
	return abi.JSON(strings.NewReader(WorldlandRentalABIJSON))
}

// ContractAddress represents a deployed WorldlandRental contract address.
// This is used by the event listener to filter logs from specific contracts.
type ContractAddress struct {
	Address common.Address
	ChainID uint64
}

// NewContractAddress creates a new ContractAddress from a hex string.
func NewContractAddress(hexAddr string, chainID uint64) ContractAddress {
	return ContractAddress{
		Address: common.HexToAddress(hexAddr),
		ChainID: chainID,
	}
}

// EventSignatures returns all known event signatures for log filtering.
func EventSignatures() []common.Hash {
	return []common.Hash{
		RentalStartedSig,
		RentalStoppedSig,
		DepositedSig,
		WithdrawnSig,
	}
}

// RentalEventSignatures returns only rental-related event signatures.
// These are the primary events the Hub needs to track for session management.
func RentalEventSignatures() []common.Hash {
	return []common.Hash{
		RentalStartedSig,
		RentalStoppedSig,
	}
}

// ReconnectBackoff returns a backoff configuration for WebSocket reconnection.
// Uses exponential backoff with jitter for resilient blockchain connections.
func ReconnectBackoff() backoff.BackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 1 * time.Second
	b.MaxInterval = 30 * time.Second
	b.MaxElapsedTime = 5 * time.Minute
	b.RandomizationFactor = 0.5
	return b
}

// NewPermanentBackoff returns a backoff that never stops retrying.
// Used for critical blockchain connections that must be maintained.
func NewPermanentBackoff() backoff.BackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 1 * time.Second
	b.MaxInterval = 30 * time.Second
	b.MaxElapsedTime = 0 // Never stop
	b.RandomizationFactor = 0.5
	return b
}
