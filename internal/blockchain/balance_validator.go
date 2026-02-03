// Package blockchain provides smart contract bindings and balance validation
// for the WorldlandRental contract.
package blockchain

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// depositsABI is the ABI for the deposits(address) view function
const depositsABI = `[{
	"constant": true,
	"inputs": [{"name": "user", "type": "address"}],
	"name": "deposits",
	"outputs": [{"name": "", "type": "uint256"}],
	"payable": false,
	"stateMutability": "view",
	"type": "function"
}]`

// ContractCaller is an interface for calling contract methods
// This allows for easy mocking in tests
type ContractCaller interface {
	CallContract(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
}

// BalanceValidator queries on-chain deposit balances from the WorldlandRental contract
type BalanceValidator struct {
	caller          ContractCaller
	contractAddress common.Address
	abi             abi.ABI
}

// NewBalanceValidator creates a new balance validator
func NewBalanceValidator(caller ContractCaller, contractAddress common.Address) (*BalanceValidator, error) {
	parsedABI, err := abi.JSON(strings.NewReader(depositsABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse deposits ABI: %w", err)
	}

	return &BalanceValidator{
		caller:          caller,
		contractAddress: contractAddress,
		abi:             parsedABI,
	}, nil
}

// GetDepositBalance queries the deposits(address) mapping on the contract
func (v *BalanceValidator) GetDepositBalance(ctx context.Context, userAddress common.Address) (*big.Int, error) {
	// Pack the function call data
	data, err := v.abi.Pack("deposits", userAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to pack deposits call: %w", err)
	}

	// Create the call message
	msg := ethereum.CallMsg{
		To:   &v.contractAddress,
		Data: data,
	}

	// Execute the call
	result, err := v.caller.CallContract(ctx, msg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query deposit balance: %w", err)
	}

	// Unpack the result
	var balance *big.Int
	err = v.abi.UnpackIntoInterface(&balance, "deposits", result)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack deposit balance: %w", err)
	}

	return balance, nil
}

// ValidateDepositBalance checks if user has sufficient deposit for a given amount
// Returns: (hasSufficient, currentBalance, error)
func (v *BalanceValidator) ValidateDepositBalance(ctx context.Context, userAddress common.Address, requiredAmount *big.Int) (bool, *big.Int, error) {
	balance, err := v.GetDepositBalance(ctx, userAddress)
	if err != nil {
		return false, nil, err
	}

	// Check if balance >= required amount
	hasSufficient := balance.Cmp(requiredAmount) >= 0
	return hasSufficient, balance, nil
}

// BalanceValidatorInterface defines the interface for balance validation
// This allows for easy mocking in tests
type BalanceValidatorInterface interface {
	GetDepositBalance(ctx context.Context, userAddress common.Address) (*big.Int, error)
	ValidateDepositBalance(ctx context.Context, userAddress common.Address, requiredAmount *big.Int) (bool, *big.Int, error)
}

// Ensure BalanceValidator implements BalanceValidatorInterface
var _ BalanceValidatorInterface = (*BalanceValidator)(nil)
