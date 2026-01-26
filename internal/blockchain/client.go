// Package blockchain provides Ethereum/BSC blockchain integration.
package blockchain

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Client wraps the Ethereum client for blockchain interactions.
type Client struct {
	client       *ethclient.Client
	chainID      *big.Int
	vaultAddress common.Address
	vaultABI     abi.ABI
	privateKey   *ecdsa.PrivateKey
}

// Config holds blockchain client configuration.
type Config struct {
	RPCURL       string // BSC RPC URL
	ChainID      int64  // 56 for BSC Mainnet, 97 for Testnet
	VaultAddress string // GPUVault contract address
	PrivateKey   string // Backend signer private key (for relaying txs)
}

// GPUVaultABI is the ABI for the GPUVault contract (minimal for our needs).
const GPUVaultABI = `[
	{"inputs":[{"internalType":"address","name":"","type":"address"}],"name":"deposits","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},
	{"inputs":[{"internalType":"address","name":"sessionKey","type":"address"}],"name":"getSessionKey","outputs":[{"components":[{"internalType":"address","name":"mainWallet","type":"address"},{"internalType":"uint256","name":"spendLimit","type":"uint256"},{"internalType":"uint256","name":"spentAmount","type":"uint256"},{"internalType":"uint256","name":"expiry","type":"uint256"},{"internalType":"bool","name":"isActive","type":"bool"}],"internalType":"struct GPUVault.SessionKey","name":"","type":"tuple"}],"stateMutability":"view","type":"function"},
	{"inputs":[{"internalType":"address","name":"user","type":"address"}],"name":"getAvailableBalance","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},
	{"inputs":[{"internalType":"uint256","name":"rentalId","type":"uint256"}],"name":"getRental","outputs":[{"components":[{"internalType":"address","name":"renter","type":"address"},{"internalType":"address","name":"provider","type":"address"},{"internalType":"uint256","name":"pricePerSecond","type":"uint256"},{"internalType":"uint256","name":"startTime","type":"uint256"},{"internalType":"bool","name":"isActive","type":"bool"},{"internalType":"string","name":"jobId","type":"string"}],"internalType":"struct GPUVault.Rental","name":"","type":"tuple"}],"stateMutability":"view","type":"function"},
	{"inputs":[{"internalType":"uint256","name":"rentalId","type":"uint256"}],"name":"calculateRentalCost","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},
	{"inputs":[{"internalType":"address","name":"sessionKey","type":"address"},{"internalType":"address","name":"provider","type":"address"},{"internalType":"uint256","name":"pricePerSecond","type":"uint256"},{"internalType":"string","name":"jobId","type":"string"}],"name":"startRentalWithSessionKey","outputs":[{"internalType":"uint256","name":"rentalId","type":"uint256"}],"stateMutability":"nonpayable","type":"function"},
	{"inputs":[{"internalType":"uint256","name":"rentalId","type":"uint256"}],"name":"endRental","outputs":[],"stateMutability":"nonpayable","type":"function"},
	{"inputs":[],"name":"rentalCount","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"}
]`

// SessionKey represents a session key from the contract.
type SessionKey struct {
	MainWallet  common.Address
	SpendLimit  *big.Int
	SpentAmount *big.Int
	Expiry      *big.Int
	IsActive    bool
}

// Rental represents a rental from the contract.
type Rental struct {
	Renter         common.Address
	Provider       common.Address
	PricePerSecond *big.Int
	StartTime      *big.Int
	IsActive       bool
	JobID          string
}

// NewClient creates a new blockchain client.
func NewClient(cfg *Config) (*Client, error) {
	client, err := ethclient.Dial(cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RPC: %w", err)
	}

	parsedABI, err := abi.JSON(strings.NewReader(GPUVaultABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %w", err)
	}

	var privateKey *ecdsa.PrivateKey
	if cfg.PrivateKey != "" {
		privateKey, err = crypto.HexToECDSA(strings.TrimPrefix(cfg.PrivateKey, "0x"))
		if err != nil {
			return nil, fmt.Errorf("invalid private key: %w", err)
		}
	}

	return &Client{
		client:       client,
		chainID:      big.NewInt(cfg.ChainID),
		vaultAddress: common.HexToAddress(cfg.VaultAddress),
		vaultABI:     parsedABI,
		privateKey:   privateKey,
	}, nil
}

// Close closes the client connection.
func (c *Client) Close() {
	c.client.Close()
}

// GetDeposit returns the deposit balance for a user.
func (c *Client) GetDeposit(ctx context.Context, userAddress string) (*big.Int, error) {
	data, err := c.vaultABI.Pack("deposits", common.HexToAddress(userAddress))
	if err != nil {
		return nil, err
	}

	result, err := c.client.CallContract(ctx, ethereum.CallMsg{
		To:   &c.vaultAddress,
		Data: data,
	}, nil)
	if err != nil {
		return nil, err
	}

	var balance *big.Int
	err = c.vaultABI.UnpackIntoInterface(&balance, "deposits", result)
	if err != nil {
		return nil, err
	}

	return balance, nil
}

// GetSessionKey returns the session key info from the contract.
func (c *Client) GetSessionKey(ctx context.Context, sessionKeyAddr string) (*SessionKey, error) {
	data, err := c.vaultABI.Pack("getSessionKey", common.HexToAddress(sessionKeyAddr))
	if err != nil {
		return nil, err
	}

	result, err := c.client.CallContract(ctx, ethereum.CallMsg{
		To:   &c.vaultAddress,
		Data: data,
	}, nil)
	if err != nil {
		return nil, err
	}

	// Unpack the tuple
	unpacked, err := c.vaultABI.Unpack("getSessionKey", result)
	if err != nil {
		return nil, err
	}

	if len(unpacked) == 0 {
		return nil, fmt.Errorf("no data returned")
	}

	// The result is a struct
	skData := unpacked[0].(struct {
		MainWallet  common.Address `json:"mainWallet"`
		SpendLimit  *big.Int       `json:"spendLimit"`
		SpentAmount *big.Int       `json:"spentAmount"`
		Expiry      *big.Int       `json:"expiry"`
		IsActive    bool           `json:"isActive"`
	})

	return &SessionKey{
		MainWallet:  skData.MainWallet,
		SpendLimit:  skData.SpendLimit,
		SpentAmount: skData.SpentAmount,
		Expiry:      skData.Expiry,
		IsActive:    skData.IsActive,
	}, nil
}

// GetAvailableBalance returns the available balance for a user.
func (c *Client) GetAvailableBalance(ctx context.Context, userAddress string) (*big.Int, error) {
	data, err := c.vaultABI.Pack("getAvailableBalance", common.HexToAddress(userAddress))
	if err != nil {
		return nil, err
	}

	result, err := c.client.CallContract(ctx, ethereum.CallMsg{
		To:   &c.vaultAddress,
		Data: data,
	}, nil)
	if err != nil {
		return nil, err
	}

	var balance *big.Int
	err = c.vaultABI.UnpackIntoInterface(&balance, "getAvailableBalance", result)
	if err != nil {
		return nil, err
	}

	return balance, nil
}

// StartRentalWithSessionKey starts a rental using a session key.
func (c *Client) StartRentalWithSessionKey(
	ctx context.Context,
	sessionKeyAddr string,
	providerAddr string,
	pricePerSecond *big.Int,
	jobID string,
) (*types.Transaction, error) {
	if c.privateKey == nil {
		return nil, fmt.Errorf("private key not configured")
	}

	nonce, err := c.client.PendingNonceAt(ctx, crypto.PubkeyToAddress(c.privateKey.PublicKey))
	if err != nil {
		return nil, err
	}

	gasPrice, err := c.client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, err
	}

	auth, err := bind.NewKeyedTransactorWithChainID(c.privateKey, c.chainID)
	if err != nil {
		return nil, err
	}
	auth.Nonce = big.NewInt(int64(nonce))
	auth.GasPrice = gasPrice
	auth.GasLimit = uint64(300000)

	data, err := c.vaultABI.Pack(
		"startRentalWithSessionKey",
		common.HexToAddress(sessionKeyAddr),
		common.HexToAddress(providerAddr),
		pricePerSecond,
		jobID,
	)
	if err != nil {
		return nil, err
	}

	tx := types.NewTransaction(
		nonce,
		c.vaultAddress,
		big.NewInt(0),
		auth.GasLimit,
		gasPrice,
		data,
	)

	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(c.chainID), c.privateKey)
	if err != nil {
		return nil, err
	}

	err = c.client.SendTransaction(ctx, signedTx)
	if err != nil {
		return nil, err
	}

	return signedTx, nil
}

// EndRental ends a rental and triggers settlement.
func (c *Client) EndRental(ctx context.Context, rentalID *big.Int) (*types.Transaction, error) {
	if c.privateKey == nil {
		return nil, fmt.Errorf("private key not configured")
	}

	nonce, err := c.client.PendingNonceAt(ctx, crypto.PubkeyToAddress(c.privateKey.PublicKey))
	if err != nil {
		return nil, err
	}

	gasPrice, err := c.client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, err
	}

	data, err := c.vaultABI.Pack("endRental", rentalID)
	if err != nil {
		return nil, err
	}

	tx := types.NewTransaction(
		nonce,
		c.vaultAddress,
		big.NewInt(0),
		uint64(300000),
		gasPrice,
		data,
	)

	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(c.chainID), c.privateKey)
	if err != nil {
		return nil, err
	}

	err = c.client.SendTransaction(ctx, signedTx)
	if err != nil {
		return nil, err
	}

	return signedTx, nil
}

// CalculateRentalCost calculates the current cost of a rental.
func (c *Client) CalculateRentalCost(ctx context.Context, rentalID *big.Int) (*big.Int, error) {
	data, err := c.vaultABI.Pack("calculateRentalCost", rentalID)
	if err != nil {
		return nil, err
	}

	result, err := c.client.CallContract(ctx, ethereum.CallMsg{
		To:   &c.vaultAddress,
		Data: data,
	}, nil)
	if err != nil {
		return nil, err
	}

	var cost *big.Int
	err = c.vaultABI.UnpackIntoInterface(&cost, "calculateRentalCost", result)
	if err != nil {
		return nil, err
	}

	return cost, nil
}
