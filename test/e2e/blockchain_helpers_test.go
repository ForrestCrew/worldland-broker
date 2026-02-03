//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/sha3"
)

// ContractAddresses holds deployed contract addresses from deployment-localhost.json
type ContractAddresses struct {
	WorldlandRental string `json:"worldlandRental"`
	PaymentToken    string `json:"paymentToken"`
}

// DeploymentInfo contains the full deployment information
type DeploymentInfo struct {
	Network   string            `json:"network"`
	ChainID   string            `json:"chainId"`
	Deployer  string            `json:"deployer"`
	Contracts ContractAddresses `json:"contracts"`
	Timestamp string            `json:"timestamp"`
}

// LoadContractAddresses loads deployed contract addresses from the deployment file
// Returns the contract addresses and the deployer address
func LoadContractAddresses(ctx context.Context) (*ContractAddresses, string, error) {
	// Check multiple possible locations for deployment.json
	possiblePaths := []string{
		// E2E environment (run from e2e/ directory)
		filepath.Join("..", "..", "..", "e2e", "deployment.json"),
		// Run from worldland-hub/test/e2e/
		filepath.Join("..", "..", "worldland-contracts", "deployment-localhost.json"),
		// Direct path for E2E tests
		"/home/ahwlsqja/worldland-backend/e2e/deployment.json",
		// Environment variable override
		os.Getenv("DEPLOYMENT_JSON_PATH"),
	}

	var data []byte
	var err error
	var usedPath string

	for _, path := range possiblePaths {
		if path == "" {
			continue
		}
		data, err = os.ReadFile(path)
		if err == nil {
			usedPath = path
			break
		}
	}

	if data == nil {
		return nil, "", fmt.Errorf("failed to read deployment file from any path: %v", possiblePaths)
	}

	var deployment DeploymentInfo
	if err := json.Unmarshal(data, &deployment); err != nil {
		return nil, "", fmt.Errorf("failed to parse deployment file %s: %w", usedPath, err)
	}

	return &deployment.Contracts, deployment.Deployer, nil
}

// BlockchainHelper provides utilities for blockchain operations in E2E tests
type BlockchainHelper struct {
	client        *HardhatClient
	snapshotID    string
	hasSnapshot   bool
}

// NewBlockchainHelper creates a new blockchain helper
func NewBlockchainHelper(hardhatURL string) *BlockchainHelper {
	return &BlockchainHelper{
		client: NewHardhatClient(hardhatURL),
	}
}

// Snapshot creates an EVM snapshot that can be reverted later
// Useful for test isolation - each test can revert to clean state
func (b *BlockchainHelper) Snapshot(ctx context.Context) error {
	var snapshotID string
	if err := b.client.Call(ctx, "evm_snapshot", []interface{}{}, &snapshotID); err != nil {
		return fmt.Errorf("failed to create snapshot: %w", err)
	}
	b.snapshotID = snapshotID
	b.hasSnapshot = true
	return nil
}

// Revert reverts to the last snapshot
func (b *BlockchainHelper) Revert(ctx context.Context) error {
	if !b.hasSnapshot {
		return fmt.Errorf("no snapshot to revert to")
	}

	var success bool
	if err := b.client.Call(ctx, "evm_revert", []interface{}{b.snapshotID}, &success); err != nil {
		return fmt.Errorf("failed to revert snapshot: %w", err)
	}

	if !success {
		return fmt.Errorf("snapshot revert returned false")
	}

	b.hasSnapshot = false
	b.snapshotID = ""
	return nil
}

// MineBlocks mines N blocks on the Hardhat node
func (b *BlockchainHelper) MineBlocks(ctx context.Context, count int) error {
	for i := 0; i < count; i++ {
		if err := b.client.MineBlock(ctx); err != nil {
			return fmt.Errorf("failed to mine block %d: %w", i+1, err)
		}
	}
	return nil
}

// IncreaseTime increases the EVM time by the specified duration
// Mines a new block to apply the time change
func (b *BlockchainHelper) IncreaseTime(ctx context.Context, duration time.Duration) error {
	seconds := int64(duration.Seconds())

	var result string
	if err := b.client.Call(ctx, "evm_increaseTime", []interface{}{seconds}, &result); err != nil {
		return fmt.Errorf("failed to increase time: %w", err)
	}

	// Mine a block to apply the time change
	if err := b.client.MineBlock(ctx); err != nil {
		return fmt.Errorf("failed to mine block after time increase: %w", err)
	}

	return nil
}

// GetBalance gets the ETH balance of an address
func (b *BlockchainHelper) GetBalance(ctx context.Context, address string) (*big.Int, error) {
	var hexBalance string
	if err := b.client.Call(ctx, "eth_getBalance", []interface{}{address, "latest"}, &hexBalance); err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}

	balance := new(big.Int)
	balance.SetString(hexBalance[2:], 16) // Remove "0x" prefix
	return balance, nil
}

// WaitForReceipt waits for a transaction receipt with timeout
// Returns the receipt or error if transaction failed/timed out
func (b *BlockchainHelper) WaitForReceipt(ctx context.Context, txHash string, timeout time.Duration) (map[string]interface{}, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		var receipt map[string]interface{}
		err := b.client.Call(ctx, "eth_getTransactionReceipt", []interface{}{txHash}, &receipt)
		if err != nil {
			return nil, fmt.Errorf("failed to get transaction receipt: %w", err)
		}

		if receipt != nil {
			// Check if transaction succeeded
			status, ok := receipt["status"].(string)
			if !ok {
				return nil, fmt.Errorf("receipt missing status field")
			}

			if status == "0x0" {
				return nil, fmt.Errorf("transaction failed (status 0x0)")
			}

			return receipt, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
			// Continue polling
		}
	}

	return nil, fmt.Errorf("transaction receipt not found within timeout")
}

// SetBalance sets the ETH balance of an address (Hardhat specific)
// Useful for funding test accounts
func (b *BlockchainHelper) SetBalance(ctx context.Context, address string, balance *big.Int) error {
	// Convert balance to hex
	hexBalance := fmt.Sprintf("0x%x", balance)

	var result bool
	if err := b.client.Call(ctx, "hardhat_setBalance", []interface{}{address, hexBalance}, &result); err != nil {
		return fmt.Errorf("failed to set balance: %w", err)
	}

	return nil
}

// GetBlockNumber gets the current block number
func (b *BlockchainHelper) GetBlockNumber(ctx context.Context) (uint64, error) {
	return b.client.GetBlockNumber(ctx)
}

// ========================================
// Contract Helper - Real Blockchain Calls
// ========================================

// Hardhat test accounts (pre-funded with 10000 ETH each)
const (
	// Account #0 - Deployer (has all minted tokens initially)
	HardhatAccount0Address    = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
	HardhatAccount0PrivateKey = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

	// Account #1 - Test User (renter)
	HardhatAccount1Address    = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
	HardhatAccount1PrivateKey = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"

	// Account #2 - Provider
	HardhatAccount2Address    = "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"
	HardhatAccount2PrivateKey = "5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
)

// ContractHelper provides methods to interact with deployed contracts
type ContractHelper struct {
	blockchain   *BlockchainHelper
	rentalAddr   common.Address
	tokenAddr    common.Address
	chainID      *big.Int
}

// NewContractHelper creates a new contract helper
func NewContractHelper(blockchain *BlockchainHelper, rentalAddr, tokenAddr string) *ContractHelper {
	return &ContractHelper{
		blockchain: blockchain,
		rentalAddr: common.HexToAddress(rentalAddr),
		tokenAddr:  common.HexToAddress(tokenAddr),
		chainID:    big.NewInt(31337), // Hardhat default chain ID
	}
}

// NewContractHelperFromDeployment loads contract addresses from deployment.json
func NewContractHelperFromDeployment(blockchain *BlockchainHelper) (*ContractHelper, error) {
	contracts, _, err := LoadContractAddresses(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to load contract addresses: %w", err)
	}

	return NewContractHelper(blockchain, contracts.WorldlandRental, contracts.PaymentToken), nil
}

// privateKeyFromHex converts a hex string to an ECDSA private key
func privateKeyFromHex(hexKey string) (*ecdsa.PrivateKey, error) {
	// Remove 0x prefix if present
	hexKey = strings.TrimPrefix(hexKey, "0x")
	return crypto.HexToECDSA(hexKey)
}

// getNonce gets the current nonce for an address
func (c *ContractHelper) getNonce(ctx context.Context, address common.Address) (uint64, error) {
	var hexNonce string
	if err := c.blockchain.client.Call(ctx, "eth_getTransactionCount", []interface{}{address.Hex(), "pending"}, &hexNonce); err != nil {
		return 0, fmt.Errorf("failed to get nonce: %w", err)
	}

	nonce := new(big.Int)
	nonce.SetString(strings.TrimPrefix(hexNonce, "0x"), 16)
	return nonce.Uint64(), nil
}

// getGasPrice gets the current gas price
func (c *ContractHelper) getGasPrice(ctx context.Context) (*big.Int, error) {
	var hexGasPrice string
	if err := c.blockchain.client.Call(ctx, "eth_gasPrice", []interface{}{}, &hexGasPrice); err != nil {
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}

	gasPrice := new(big.Int)
	gasPrice.SetString(strings.TrimPrefix(hexGasPrice, "0x"), 16)
	return gasPrice, nil
}

// sendTransaction signs and sends a transaction
func (c *ContractHelper) sendTransaction(ctx context.Context, privateKeyHex string, to common.Address, data []byte, value *big.Int) (string, error) {
	privateKey, err := privateKeyFromHex(privateKeyHex)
	if err != nil {
		return "", fmt.Errorf("invalid private key: %w", err)
	}

	fromAddr := crypto.PubkeyToAddress(privateKey.PublicKey)

	nonce, err := c.getNonce(ctx, fromAddr)
	if err != nil {
		return "", err
	}

	gasPrice, err := c.getGasPrice(ctx)
	if err != nil {
		return "", err
	}

	// Estimate gas
	gasLimit := uint64(200000) // Conservative estimate

	if value == nil {
		value = big.NewInt(0)
	}

	tx := types.NewTransaction(nonce, to, value, gasLimit, gasPrice, data)

	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(c.chainID), privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Encode signed transaction
	txBytes, err := signedTx.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("failed to encode transaction: %w", err)
	}

	// Send raw transaction
	var txHash string
	if err := c.blockchain.client.Call(ctx, "eth_sendRawTransaction", []interface{}{"0x" + hex.EncodeToString(txBytes)}, &txHash); err != nil {
		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	return txHash, nil
}

// keccak256 computes the Keccak256 hash of a byte slice
func keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

// encodeFunction encodes a function call with arguments
// methodSig is like "approve(address,uint256)"
func encodeFunction(methodSig string, args ...interface{}) ([]byte, error) {
	// Get method ID (first 4 bytes of keccak256 of signature)
	methodID := keccak256([]byte(methodSig))[:4]

	// Encode arguments (each padded to 32 bytes)
	var encodedArgs []byte
	for _, arg := range args {
		switch v := arg.(type) {
		case common.Address:
			// Address padded to 32 bytes
			paddedAddr := common.LeftPadBytes(v.Bytes(), 32)
			encodedArgs = append(encodedArgs, paddedAddr...)
		case *big.Int:
			// uint256 padded to 32 bytes
			paddedInt := common.LeftPadBytes(v.Bytes(), 32)
			encodedArgs = append(encodedArgs, paddedInt...)
		case uint64:
			bigVal := new(big.Int).SetUint64(v)
			paddedInt := common.LeftPadBytes(bigVal.Bytes(), 32)
			encodedArgs = append(encodedArgs, paddedInt...)
		default:
			return nil, fmt.Errorf("unsupported argument type: %T", arg)
		}
	}

	return append(methodID, encodedArgs...), nil
}

// MintTokens mints tokens to an address (only deployer can call)
// Uses Account #0 (deployer) to mint tokens
func (c *ContractHelper) MintTokens(ctx context.Context, toAddress string, amount *big.Int) (string, error) {
	to := common.HexToAddress(toAddress)

	// Encode: mint(address,uint256)
	data, err := encodeFunction("mint(address,uint256)", to, amount)
	if err != nil {
		return "", fmt.Errorf("failed to encode mint: %w", err)
	}

	txHash, err := c.sendTransaction(ctx, HardhatAccount0PrivateKey, c.tokenAddr, data, nil)
	if err != nil {
		return "", fmt.Errorf("mint failed: %w", err)
	}

	// Wait for receipt
	_, err = c.blockchain.WaitForReceipt(ctx, txHash, 30*time.Second)
	if err != nil {
		return "", fmt.Errorf("mint transaction failed: %w", err)
	}

	return txHash, nil
}

// ApproveToken approves the rental contract to spend tokens
func (c *ContractHelper) ApproveToken(ctx context.Context, privateKeyHex string, amount *big.Int) (string, error) {
	// Encode: approve(address,uint256)
	data, err := encodeFunction("approve(address,uint256)", c.rentalAddr, amount)
	if err != nil {
		return "", fmt.Errorf("failed to encode approve: %w", err)
	}

	txHash, err := c.sendTransaction(ctx, privateKeyHex, c.tokenAddr, data, nil)
	if err != nil {
		return "", fmt.Errorf("approve failed: %w", err)
	}

	// Wait for receipt
	_, err = c.blockchain.WaitForReceipt(ctx, txHash, 30*time.Second)
	if err != nil {
		return "", fmt.Errorf("approve transaction failed: %w", err)
	}

	return txHash, nil
}

// DepositTokens deposits tokens to the rental contract
func (c *ContractHelper) DepositTokens(ctx context.Context, privateKeyHex string, amount *big.Int) (string, error) {
	// Encode: deposit(uint256)
	data, err := encodeFunction("deposit(uint256)", amount)
	if err != nil {
		return "", fmt.Errorf("failed to encode deposit: %w", err)
	}

	txHash, err := c.sendTransaction(ctx, privateKeyHex, c.rentalAddr, data, nil)
	if err != nil {
		return "", fmt.Errorf("deposit failed: %w", err)
	}

	// Wait for receipt
	_, err = c.blockchain.WaitForReceipt(ctx, txHash, 30*time.Second)
	if err != nil {
		return "", fmt.Errorf("deposit transaction failed: %w", err)
	}

	return txHash, nil
}

// StartRental calls the startRental function on the rental contract
// Returns the transaction hash (which is what we need for ConfirmSession)
func (c *ContractHelper) StartRental(ctx context.Context, privateKeyHex string, providerAddress string, pricePerSecond *big.Int) (string, error) {
	provider := common.HexToAddress(providerAddress)

	// Encode: startRental(address,uint256)
	data, err := encodeFunction("startRental(address,uint256)", provider, pricePerSecond)
	if err != nil {
		return "", fmt.Errorf("failed to encode startRental: %w", err)
	}

	txHash, err := c.sendTransaction(ctx, privateKeyHex, c.rentalAddr, data, nil)
	if err != nil {
		return "", fmt.Errorf("startRental failed: %w", err)
	}

	// Wait for receipt
	_, err = c.blockchain.WaitForReceipt(ctx, txHash, 30*time.Second)
	if err != nil {
		return "", fmt.Errorf("startRental transaction failed: %w", err)
	}

	return txHash, nil
}

// GetTokenBalance gets the token balance of an address
func (c *ContractHelper) GetTokenBalance(ctx context.Context, address string) (*big.Int, error) {
	addr := common.HexToAddress(address)

	// Encode: balanceOf(address)
	data, err := encodeFunction("balanceOf(address)", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to encode balanceOf: %w", err)
	}

	// Call (read-only)
	callData := map[string]interface{}{
		"to":   c.tokenAddr.Hex(),
		"data": "0x" + hex.EncodeToString(data),
	}

	var result string
	if err := c.blockchain.client.Call(ctx, "eth_call", []interface{}{callData, "latest"}, &result); err != nil {
		return nil, fmt.Errorf("balanceOf call failed: %w", err)
	}

	balance := new(big.Int)
	balance.SetString(strings.TrimPrefix(result, "0x"), 16)
	return balance, nil
}

// GetContractDeposit gets the deposit balance in the rental contract
func (c *ContractHelper) GetContractDeposit(ctx context.Context, address string) (*big.Int, error) {
	addr := common.HexToAddress(address)

	// Encode: deposits(address)
	data, err := encodeFunction("deposits(address)", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to encode deposits: %w", err)
	}

	// Call (read-only)
	callData := map[string]interface{}{
		"to":   c.rentalAddr.Hex(),
		"data": "0x" + hex.EncodeToString(data),
	}

	var result string
	if err := c.blockchain.client.Call(ctx, "eth_call", []interface{}{callData, "latest"}, &result); err != nil {
		return nil, fmt.Errorf("deposits call failed: %w", err)
	}

	deposit := new(big.Int)
	deposit.SetString(strings.TrimPrefix(result, "0x"), 16)
	return deposit, nil
}

// SetupTestUser sets up a test user with tokens and deposits
// 1. Mints tokens to user
// 2. User approves rental contract
// 3. User deposits tokens
func (c *ContractHelper) SetupTestUser(ctx context.Context, userPrivateKey, userAddress string, tokenAmount *big.Int) error {
	// 1. Mint tokens to user
	_, err := c.MintTokens(ctx, userAddress, tokenAmount)
	if err != nil {
		return fmt.Errorf("failed to mint tokens: %w", err)
	}

	// 2. Approve rental contract
	_, err = c.ApproveToken(ctx, userPrivateKey, tokenAmount)
	if err != nil {
		return fmt.Errorf("failed to approve tokens: %w", err)
	}

	// 3. Deposit to rental contract
	_, err = c.DepositTokens(ctx, userPrivateKey, tokenAmount)
	if err != nil {
		return fmt.Errorf("failed to deposit tokens: %w", err)
	}

	return nil
}
