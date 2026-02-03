// Package blockchain provides smart contract interaction and verification
// for the WorldlandRental contract.
package blockchain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// VerificationStatus represents the outcome of transaction verification.
type VerificationStatus string

const (
	// StatusPending indicates the transaction is not yet mined (receipt not found or BlockNumber == 0)
	StatusPending VerificationStatus = "pending"
	// StatusFailed indicates the transaction was mined but reverted
	StatusFailed VerificationStatus = "failed"
	// StatusInvalid indicates the transaction exists but is invalid (wrong function, user, or provider)
	StatusInvalid VerificationStatus = "invalid"
	// StatusConfirmed indicates the transaction is valid with matching user/provider
	StatusConfirmed VerificationStatus = "confirmed"
)

// VerificationResult contains the outcome of transaction verification.
type VerificationResult struct {
	Status      VerificationStatus
	Reason      string    // For failed/invalid status, explains why
	RentalID    uint64    // For confirmed status, the parsed rental ID
	StartTime   time.Time // For confirmed status, the rental start time
	BlockNumber uint64    // For confirmed status, the block number
}

// ReceiptClient defines the interface for retrieving transaction receipts.
// This minimal interface allows for easy mocking in tests.
// Note: ethclient.Client from go-ethereum implements this interface.
type ReceiptClient interface {
	// TransactionReceipt returns the receipt of a transaction by transaction hash.
	// Returns ethereum.NotFound if the transaction is not yet mined.
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
}

// TransactionVerifier validates blockchain transactions for rental confirmation.
// It checks that a transaction exists on-chain, succeeded, contains a RentalStarted event,
// and that the event parameters match the expected user and provider.
type TransactionVerifier struct {
	client          ReceiptClient
	contractAddress common.Address
}

// NewTransactionVerifier creates a new TransactionVerifier.
//
// Parameters:
//   - client: Ethereum client for RPC calls (must implement TransactionReceipt)
//   - contractAddress: The address of the WorldlandRental contract to verify events from
func NewTransactionVerifier(client ReceiptClient, contractAddress common.Address) *TransactionVerifier {
	return &TransactionVerifier{
		client:          client,
		contractAddress: contractAddress,
	}
}

// VerifyTransaction validates a blockchain transaction for rental confirmation.
//
// It performs the following checks:
// 1. Retrieves the transaction receipt (handles NotFound as pending)
// 2. Checks BlockNumber > 0 (pending if not mined)
// 3. Checks transaction status (failed if reverted)
// 4. Finds RentalStarted event from the contract address
// 5. Validates user and provider addresses match expectations
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - txHash: The transaction hash to verify
//   - expectedUser: The expected user address in the RentalStarted event
//   - expectedProvider: The expected provider address in the RentalStarted event
//
// Returns:
//   - VerificationResult with status and parsed event data (if confirmed)
//   - error for RPC failures (not for verification failures, which are returned as status)
func (v *TransactionVerifier) VerifyTransaction(
	ctx context.Context,
	txHash common.Hash,
	expectedUser common.Address,
	expectedProvider common.Address,
) (VerificationResult, error) {
	// Step 1: Get transaction receipt
	receipt, err := v.client.TransactionReceipt(ctx, txHash)
	if err != nil {
		// NotFound means transaction is not yet mined
		if errors.Is(err, ethereum.NotFound) {
			return VerificationResult{Status: StatusPending}, nil
		}
		return VerificationResult{}, fmt.Errorf("failed to get transaction receipt: %w", err)
	}

	// Step 2: Check block number is non-zero (transaction is mined)
	// Some RPC providers return receipts for pending transactions with BlockNumber=0
	if receipt.BlockNumber == nil || receipt.BlockNumber.Uint64() == 0 {
		return VerificationResult{Status: StatusPending}, nil
	}

	// Step 3: Check transaction didn't revert (Status == 1 means success)
	if receipt.Status != types.ReceiptStatusSuccessful {
		return VerificationResult{
			Status: StatusFailed,
			Reason: "transaction reverted",
		}, nil
	}

	// Step 4: Find RentalStarted event from our contract
	for _, log := range receipt.Logs {
		// Skip logs from other contracts
		if log.Address != v.contractAddress {
			continue
		}

		// Check if this is a RentalStarted event
		if len(log.Topics) > 0 && log.Topics[0] == RentalStartedSig {
			// Parse the event
			event, err := ParseRentalStarted(*log)
			if err != nil {
				return VerificationResult{}, fmt.Errorf("failed to parse RentalStarted event: %w", err)
			}

			// Step 5: Validate user matches
			if event.User != expectedUser {
				return VerificationResult{
					Status: StatusInvalid,
					Reason: fmt.Sprintf("user mismatch: expected %s, got %s",
						expectedUser.Hex(), event.User.Hex()),
				}, nil
			}

			// Step 5: Validate provider matches
			if event.Provider != expectedProvider {
				return VerificationResult{
					Status: StatusInvalid,
					Reason: fmt.Sprintf("provider mismatch: expected %s, got %s",
						expectedProvider.Hex(), event.Provider.Hex()),
				}, nil
			}

			// All checks passed - return confirmed status with parsed data
			return VerificationResult{
				Status:      StatusConfirmed,
				RentalID:    event.RentalID,
				StartTime:   time.Unix(int64(event.StartTime), 0),
				BlockNumber: receipt.BlockNumber.Uint64(),
			}, nil
		}
	}

	// No RentalStarted event found in transaction
	return VerificationResult{
		Status: StatusInvalid,
		Reason: "no RentalStarted event found in transaction",
	}, nil
}
