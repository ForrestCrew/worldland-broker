package blockchain

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockVerifierClient implements ReceiptClient interface for verifier testing
type mockVerifierClient struct {
	receipt *types.Receipt
	err     error
}

func (m *mockVerifierClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.receipt, nil
}

// createVerifierLog creates a valid RentalStarted log for verifier testing
func createVerifierLog(contractAddr common.Address, rentalID uint64, user, provider common.Address, startTime uint64) types.Log {
	// Topics: [0]=event sig, [1]=rentalID, [2]=user, [3]=provider
	topics := []common.Hash{
		RentalStartedSig,
		common.BigToHash(big.NewInt(int64(rentalID))),
		common.BytesToHash(user.Bytes()),
		common.BytesToHash(provider.Bytes()),
	}

	// Data: startTime (32 bytes)
	data := common.LeftPadBytes(big.NewInt(int64(startTime)).Bytes(), 32)

	return types.Log{
		Address:     contractAddr,
		Topics:      topics,
		Data:        data,
		BlockNumber: 12345,
		TxHash:      common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
		Index:       0,
	}
}

func TestVerifyTransaction_Pending_NotFound(t *testing.T) {
	// Test case 1: Transaction not found (not yet mined) should return StatusPending
	client := &mockVerifierClient{
		err: ethereum.NotFound,
	}
	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	verifier := NewTransactionVerifier(client, contractAddr)

	ctx := context.Background()
	txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	user := common.HexToAddress("0xuser000000000000000000000000000000000001")
	provider := common.HexToAddress("0xprov000000000000000000000000000000000001")

	result, err := verifier.VerifyTransaction(ctx, txHash, user, provider)

	require.NoError(t, err)
	assert.Equal(t, StatusPending, result.Status)
}

func TestVerifyTransaction_Pending_ZeroBlock(t *testing.T) {
	// Test case 2: Receipt with BlockNumber == 0 or nil should return StatusPending
	client := &mockVerifierClient{
		receipt: &types.Receipt{
			BlockNumber: big.NewInt(0),
			Status:      types.ReceiptStatusSuccessful,
		},
	}
	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	verifier := NewTransactionVerifier(client, contractAddr)

	ctx := context.Background()
	txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	user := common.HexToAddress("0xuser000000000000000000000000000000000001")
	provider := common.HexToAddress("0xprov000000000000000000000000000000000001")

	result, err := verifier.VerifyTransaction(ctx, txHash, user, provider)

	require.NoError(t, err)
	assert.Equal(t, StatusPending, result.Status)
}

func TestVerifyTransaction_Failed_Reverted(t *testing.T) {
	// Test case 3: Transaction reverted should return StatusFailed with reason
	client := &mockVerifierClient{
		receipt: &types.Receipt{
			BlockNumber: big.NewInt(12345),
			Status:      types.ReceiptStatusFailed,
		},
	}
	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	verifier := NewTransactionVerifier(client, contractAddr)

	ctx := context.Background()
	txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	user := common.HexToAddress("0xuser000000000000000000000000000000000001")
	provider := common.HexToAddress("0xprov000000000000000000000000000000000001")

	result, err := verifier.VerifyTransaction(ctx, txHash, user, provider)

	require.NoError(t, err)
	assert.Equal(t, StatusFailed, result.Status)
	assert.Contains(t, result.Reason, "reverted")
}

func TestVerifyTransaction_Invalid_NoRentalEvent(t *testing.T) {
	// Test case 4: Transaction succeeded but no RentalStarted event found
	client := &mockVerifierClient{
		receipt: &types.Receipt{
			BlockNumber: big.NewInt(12345),
			Status:      types.ReceiptStatusSuccessful,
			Logs:        []*types.Log{}, // No logs
		},
	}
	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	verifier := NewTransactionVerifier(client, contractAddr)

	ctx := context.Background()
	txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	user := common.HexToAddress("0xuser000000000000000000000000000000000001")
	provider := common.HexToAddress("0xprov000000000000000000000000000000000001")

	result, err := verifier.VerifyTransaction(ctx, txHash, user, provider)

	require.NoError(t, err)
	assert.Equal(t, StatusInvalid, result.Status)
	assert.Contains(t, result.Reason, "no RentalStarted event")
}

func TestVerifyTransaction_Invalid_UserMismatch(t *testing.T) {
	// Test case 5: Event user doesn't match expected user
	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	actualUser := common.HexToAddress("0xactualuser00000000000000000000000000001")
	expectedUser := common.HexToAddress("0xexpecteduser00000000000000000000000001")
	provider := common.HexToAddress("0xprov000000000000000000000000000000000001")

	log := createVerifierLog(contractAddr, 42, actualUser, provider, uint64(time.Now().Unix()))
	client := &mockVerifierClient{
		receipt: &types.Receipt{
			BlockNumber: big.NewInt(12345),
			Status:      types.ReceiptStatusSuccessful,
			Logs:        []*types.Log{&log},
		},
	}
	verifier := NewTransactionVerifier(client, contractAddr)

	ctx := context.Background()
	txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")

	result, err := verifier.VerifyTransaction(ctx, txHash, expectedUser, provider)

	require.NoError(t, err)
	assert.Equal(t, StatusInvalid, result.Status)
	assert.Contains(t, result.Reason, "user mismatch")
}

func TestVerifyTransaction_Invalid_ProviderMismatch(t *testing.T) {
	// Test case 6: Event provider doesn't match expected provider
	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	user := common.HexToAddress("0xuser000000000000000000000000000000000001")
	actualProvider := common.HexToAddress("0xactualprovider000000000000000000000001")
	expectedProvider := common.HexToAddress("0xexpectedprovider000000000000000000001")

	log := createVerifierLog(contractAddr, 42, user, actualProvider, uint64(time.Now().Unix()))
	client := &mockVerifierClient{
		receipt: &types.Receipt{
			BlockNumber: big.NewInt(12345),
			Status:      types.ReceiptStatusSuccessful,
			Logs:        []*types.Log{&log},
		},
	}
	verifier := NewTransactionVerifier(client, contractAddr)

	ctx := context.Background()
	txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")

	result, err := verifier.VerifyTransaction(ctx, txHash, user, expectedProvider)

	require.NoError(t, err)
	assert.Equal(t, StatusInvalid, result.Status)
	assert.Contains(t, result.Reason, "provider mismatch")
}

func TestVerifyTransaction_Confirmed_Valid(t *testing.T) {
	// Test case 7: Valid transaction with matching user/provider returns StatusConfirmed
	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	user := common.HexToAddress("0xuser000000000000000000000000000000000001")
	provider := common.HexToAddress("0xprov000000000000000000000000000000000001")
	rentalID := uint64(42)
	startTime := uint64(1706800000) // Fixed timestamp for assertion

	log := createVerifierLog(contractAddr, rentalID, user, provider, startTime)
	client := &mockVerifierClient{
		receipt: &types.Receipt{
			BlockNumber: big.NewInt(12345),
			Status:      types.ReceiptStatusSuccessful,
			Logs:        []*types.Log{&log},
		},
	}
	verifier := NewTransactionVerifier(client, contractAddr)

	ctx := context.Background()
	txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")

	result, err := verifier.VerifyTransaction(ctx, txHash, user, provider)

	require.NoError(t, err)
	assert.Equal(t, StatusConfirmed, result.Status)
	assert.Equal(t, rentalID, result.RentalID)
	assert.Equal(t, time.Unix(int64(startTime), 0), result.StartTime)
	assert.Equal(t, uint64(12345), result.BlockNumber)
}

func TestVerifyTransaction_IgnoresOtherContractLogs(t *testing.T) {
	// Additional test: Logs from other contracts should be ignored
	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	otherContract := common.HexToAddress("0xother00000000000000000000000000000000001")
	user := common.HexToAddress("0xuser000000000000000000000000000000000001")
	provider := common.HexToAddress("0xprov000000000000000000000000000000000001")

	// Log from wrong contract address
	log := createVerifierLog(otherContract, 42, user, provider, uint64(time.Now().Unix()))
	client := &mockVerifierClient{
		receipt: &types.Receipt{
			BlockNumber: big.NewInt(12345),
			Status:      types.ReceiptStatusSuccessful,
			Logs:        []*types.Log{&log},
		},
	}
	verifier := NewTransactionVerifier(client, contractAddr)

	ctx := context.Background()
	txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")

	result, err := verifier.VerifyTransaction(ctx, txHash, user, provider)

	require.NoError(t, err)
	assert.Equal(t, StatusInvalid, result.Status)
	assert.Contains(t, result.Reason, "no RentalStarted event")
}

func TestVerifyTransaction_RPCError(t *testing.T) {
	// Test RPC error (not NotFound) should return error
	rpcErr := errors.New("connection refused")
	client := &mockVerifierClient{
		err: rpcErr,
	}
	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	verifier := NewTransactionVerifier(client, contractAddr)

	ctx := context.Background()
	txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	user := common.HexToAddress("0xuser000000000000000000000000000000000001")
	provider := common.HexToAddress("0xprov000000000000000000000000000000000001")

	_, err := verifier.VerifyTransaction(ctx, txHash, user, provider)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestVerifyTransaction_NilBlockNumber(t *testing.T) {
	// Test nil BlockNumber should return StatusPending
	client := &mockVerifierClient{
		receipt: &types.Receipt{
			BlockNumber: nil,
			Status:      types.ReceiptStatusSuccessful,
		},
	}
	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	verifier := NewTransactionVerifier(client, contractAddr)

	ctx := context.Background()
	txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	user := common.HexToAddress("0xuser000000000000000000000000000000000001")
	provider := common.HexToAddress("0xprov000000000000000000000000000000000001")

	result, err := verifier.VerifyTransaction(ctx, txHash, user, provider)

	require.NoError(t, err)
	assert.Equal(t, StatusPending, result.Status)
}
