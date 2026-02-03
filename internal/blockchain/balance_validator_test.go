package blockchain

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockContractCaller implements ContractCaller for testing
type mockContractCaller struct {
	returnData []byte
	returnErr  error
	lastCall   ethereum.CallMsg
}

func (m *mockContractCaller) CallContract(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	m.lastCall = call
	if m.returnErr != nil {
		return nil, m.returnErr
	}
	return m.returnData, nil
}

// Helper to encode a uint256 balance as ABI-encoded return data
func encodeBalanceReturn(balance *big.Int) []byte {
	// uint256 is 32 bytes, left-padded with zeros
	result := make([]byte, 32)
	balanceBytes := balance.Bytes()
	copy(result[32-len(balanceBytes):], balanceBytes)
	return result
}

func TestNewBalanceValidator(t *testing.T) {
	caller := &mockContractCaller{}
	contractAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	validator, err := NewBalanceValidator(caller, contractAddr)

	require.NoError(t, err)
	assert.NotNil(t, validator)
	assert.Equal(t, contractAddr, validator.contractAddress)
}

func TestGetDepositBalance_Success(t *testing.T) {
	// Setup mock to return encoded balance of 1000000000000000000 (1 ETH)
	expectedBalance := big.NewInt(1000000000000000000)
	caller := &mockContractCaller{
		returnData: encodeBalanceReturn(expectedBalance),
	}

	contractAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	userAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")

	validator, err := NewBalanceValidator(caller, contractAddr)
	require.NoError(t, err)

	// Test
	balance, err := validator.GetDepositBalance(context.Background(), userAddr)

	// Verify
	require.NoError(t, err)
	assert.Equal(t, expectedBalance, balance)

	// Verify the call was made to the right contract
	assert.Equal(t, &contractAddr, caller.lastCall.To)
}

func TestGetDepositBalance_ZeroBalance(t *testing.T) {
	// Setup mock to return encoded balance of 0
	expectedBalance := big.NewInt(0)
	caller := &mockContractCaller{
		returnData: encodeBalanceReturn(expectedBalance),
	}

	contractAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	userAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")

	validator, err := NewBalanceValidator(caller, contractAddr)
	require.NoError(t, err)

	// Test
	balance, err := validator.GetDepositBalance(context.Background(), userAddr)

	// Verify
	require.NoError(t, err)
	// Use Cmp for big.Int comparison to avoid internal representation differences
	assert.Equal(t, 0, balance.Cmp(expectedBalance))
}

func TestGetDepositBalance_ContractError(t *testing.T) {
	// Setup mock to return an error
	caller := &mockContractCaller{
		returnErr: assert.AnError,
	}

	contractAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	userAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")

	validator, err := NewBalanceValidator(caller, contractAddr)
	require.NoError(t, err)

	// Test
	balance, err := validator.GetDepositBalance(context.Background(), userAddr)

	// Verify
	assert.Error(t, err)
	assert.Nil(t, balance)
	assert.Contains(t, err.Error(), "failed to query deposit balance")
}

func TestValidateDepositBalance_Sufficient(t *testing.T) {
	// User has 2 ETH, needs 1 ETH - should be sufficient
	currentBalance := big.NewInt(2000000000000000000) // 2 ETH
	requiredAmount := big.NewInt(1000000000000000000) // 1 ETH

	caller := &mockContractCaller{
		returnData: encodeBalanceReturn(currentBalance),
	}

	contractAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	userAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")

	validator, err := NewBalanceValidator(caller, contractAddr)
	require.NoError(t, err)

	// Test
	hasSufficient, balance, err := validator.ValidateDepositBalance(context.Background(), userAddr, requiredAmount)

	// Verify
	require.NoError(t, err)
	assert.True(t, hasSufficient)
	assert.Equal(t, currentBalance, balance)
}

func TestValidateDepositBalance_ExactlyEqual(t *testing.T) {
	// User has exactly what's needed - should be sufficient
	amount := big.NewInt(1000000000000000000) // 1 ETH

	caller := &mockContractCaller{
		returnData: encodeBalanceReturn(amount),
	}

	contractAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	userAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")

	validator, err := NewBalanceValidator(caller, contractAddr)
	require.NoError(t, err)

	// Test
	hasSufficient, balance, err := validator.ValidateDepositBalance(context.Background(), userAddr, amount)

	// Verify
	require.NoError(t, err)
	assert.True(t, hasSufficient)
	assert.Equal(t, amount, balance)
}

func TestValidateDepositBalance_Insufficient(t *testing.T) {
	// User has 0.5 ETH, needs 1 ETH - should be insufficient
	currentBalance := big.NewInt(500000000000000000)  // 0.5 ETH
	requiredAmount := big.NewInt(1000000000000000000) // 1 ETH

	caller := &mockContractCaller{
		returnData: encodeBalanceReturn(currentBalance),
	}

	contractAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	userAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")

	validator, err := NewBalanceValidator(caller, contractAddr)
	require.NoError(t, err)

	// Test
	hasSufficient, balance, err := validator.ValidateDepositBalance(context.Background(), userAddr, requiredAmount)

	// Verify
	require.NoError(t, err)
	assert.False(t, hasSufficient)
	assert.Equal(t, currentBalance, balance)
}

func TestValidateDepositBalance_ZeroBalance(t *testing.T) {
	// User has 0, needs 1 ETH - should be insufficient
	currentBalance := big.NewInt(0)
	requiredAmount := big.NewInt(1000000000000000000) // 1 ETH

	caller := &mockContractCaller{
		returnData: encodeBalanceReturn(currentBalance),
	}

	contractAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	userAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")

	validator, err := NewBalanceValidator(caller, contractAddr)
	require.NoError(t, err)

	// Test
	hasSufficient, balance, err := validator.ValidateDepositBalance(context.Background(), userAddr, requiredAmount)

	// Verify
	require.NoError(t, err)
	assert.False(t, hasSufficient)
	// Use Cmp for big.Int comparison to avoid internal representation differences
	assert.Equal(t, 0, balance.Cmp(currentBalance))
}

func TestValidateDepositBalance_ContractError(t *testing.T) {
	// Setup mock to return an error
	caller := &mockContractCaller{
		returnErr: assert.AnError,
	}

	contractAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	userAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")

	validator, err := NewBalanceValidator(caller, contractAddr)
	require.NoError(t, err)

	requiredAmount := big.NewInt(1000000000000000000)

	// Test
	hasSufficient, balance, err := validator.ValidateDepositBalance(context.Background(), userAddr, requiredAmount)

	// Verify
	assert.Error(t, err)
	assert.False(t, hasSufficient)
	assert.Nil(t, balance)
}

func TestValidateDepositBalance_LargeNumbers(t *testing.T) {
	// Test with very large numbers (common in crypto)
	// User has 1000 ETH, needs 500 ETH
	currentBalance := new(big.Int)
	currentBalance.SetString("1000000000000000000000", 10) // 1000 ETH

	requiredAmount := new(big.Int)
	requiredAmount.SetString("500000000000000000000", 10) // 500 ETH

	caller := &mockContractCaller{
		returnData: encodeBalanceReturn(currentBalance),
	}

	contractAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	userAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")

	validator, err := NewBalanceValidator(caller, contractAddr)
	require.NoError(t, err)

	// Test
	hasSufficient, balance, err := validator.ValidateDepositBalance(context.Background(), userAddr, requiredAmount)

	// Verify
	require.NoError(t, err)
	assert.True(t, hasSufficient)
	assert.Equal(t, currentBalance, balance)
}

func TestDepositsABIPacking(t *testing.T) {
	// Test that the ABI can be parsed and used correctly
	parsedABI, err := abi.JSON(strings.NewReader(depositsABI))
	require.NoError(t, err)

	userAddr := common.HexToAddress("0xabcdef1234567890abcdef1234567890abcdef12")

	// Test packing
	data, err := parsedABI.Pack("deposits", userAddr)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// First 4 bytes should be function selector
	// keccak256("deposits(address)")[:4]
	assert.Len(t, data, 4+32) // 4 bytes selector + 32 bytes address param
}
