// Package wallet provides wallet-based authentication with EIP-712 signatures.
package wallet

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// EIP712Domain represents the domain separator for EIP-712.
type EIP712Domain struct {
	Name              string
	Version           string
	ChainID           *big.Int
	VerifyingContract common.Address
}

// SessionKeyRequest represents a session key registration request.
type SessionKeyRequest struct {
	MainWallet string  `json:"main_wallet"`
	SessionKey string  `json:"session_key"`
	SpendLimit float64 `json:"spend_limit"` // In USDT
	Duration   int64   `json:"duration"`    // In seconds
	Signature  string  `json:"signature"`   // EIP-712 signature
}

// LoginRequest represents a wallet login request.
type LoginRequest struct {
	WalletAddress string `json:"wallet_address"`
	Message       string `json:"message"`
	Signature     string `json:"signature"`
	Timestamp     int64  `json:"timestamp"`
}

// SessionKeyData represents validated session key data.
type SessionKeyData struct {
	MainWallet  common.Address
	SessionKey  common.Address
	SpendLimit  *big.Int
	SpentAmount *big.Int
	ExpiresAt   time.Time
	IsActive    bool
}

// Verifier provides signature verification functionality.
type Verifier struct {
	domain EIP712Domain
}

// NewVerifier creates a new signature verifier.
func NewVerifier(contractName, version string, chainID int64, contractAddress string) *Verifier {
	return &Verifier{
		domain: EIP712Domain{
			Name:              contractName,
			Version:           version,
			ChainID:           big.NewInt(chainID),
			VerifyingContract: common.HexToAddress(contractAddress),
		},
	}
}

// VerifyLoginSignature verifies a simple login signature (personal_sign).
func (v *Verifier) VerifyLoginSignature(req *LoginRequest) (bool, error) {
	// Check timestamp (within 5 minutes)
	now := time.Now().Unix()
	if now-req.Timestamp > 300 || req.Timestamp-now > 60 {
		return false, fmt.Errorf("signature expired or timestamp in future")
	}

	// Construct expected message
	expectedMessage := fmt.Sprintf("Sign in to GPU Rental Platform\nWallet: %s\nTimestamp: %d",
		strings.ToLower(req.WalletAddress), req.Timestamp)

	if req.Message != expectedMessage {
		return false, fmt.Errorf("message mismatch")
	}

	// Ethereum signed message prefix
	prefixedMessage := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(req.Message), req.Message)
	hash := crypto.Keccak256Hash([]byte(prefixedMessage))

	// Decode signature
	sig, err := hexToBytes(req.Signature)
	if err != nil {
		return false, fmt.Errorf("invalid signature format: %w", err)
	}

	// Adjust v value (Ethereum uses 27/28, go-ethereum uses 0/1)
	if sig[64] >= 27 {
		sig[64] -= 27
	}

	// Recover public key
	pubKey, err := crypto.SigToPub(hash.Bytes(), sig)
	if err != nil {
		return false, fmt.Errorf("failed to recover public key: %w", err)
	}

	// Get address from public key
	recoveredAddr := crypto.PubkeyToAddress(*pubKey)
	expectedAddr := common.HexToAddress(req.WalletAddress)

	return recoveredAddr == expectedAddr, nil
}

// VerifySessionKeySignature verifies an EIP-712 signature for session key registration.
func (v *Verifier) VerifySessionKeySignature(req *SessionKeyRequest, nonce uint64) (bool, error) {
	// Build typed data
	typedData := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": []apitypes.Type{
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"RegisterSessionKey": []apitypes.Type{
				{Name: "mainWallet", Type: "address"},
				{Name: "sessionKey", Type: "address"},
				{Name: "spendLimit", Type: "uint256"},
				{Name: "expiry", Type: "uint256"},
				{Name: "nonce", Type: "uint256"},
			},
		},
		PrimaryType: "RegisterSessionKey",
		Domain: apitypes.TypedDataDomain{
			Name:              v.domain.Name,
			Version:           v.domain.Version,
			ChainId:           (*math.HexOrDecimal256)(v.domain.ChainID),
			VerifyingContract: v.domain.VerifyingContract.Hex(),
		},
		Message: apitypes.TypedDataMessage{
			"mainWallet": req.MainWallet,
			"sessionKey": req.SessionKey,
			"spendLimit": toWei(req.SpendLimit).String(),
			"expiry":     fmt.Sprintf("%d", time.Now().Unix()+req.Duration),
			"nonce":      fmt.Sprintf("%d", nonce),
		},
	}

	// Hash the typed data
	domainSeparator, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return false, fmt.Errorf("failed to hash domain: %w", err)
	}

	messageHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return false, fmt.Errorf("failed to hash message: %w", err)
	}

	// Final hash: keccak256("\x19\x01" ‖ domainSeparator ‖ messageHash)
	rawData := []byte{0x19, 0x01}
	rawData = append(rawData, domainSeparator...)
	rawData = append(rawData, messageHash...)
	hash := crypto.Keccak256Hash(rawData)

	// Decode signature
	sig, err := hexToBytes(req.Signature)
	if err != nil {
		return false, fmt.Errorf("invalid signature format: %w", err)
	}

	// Adjust v value
	if sig[64] >= 27 {
		sig[64] -= 27
	}

	// Recover public key
	pubKey, err := crypto.SigToPub(hash.Bytes(), sig)
	if err != nil {
		return false, fmt.Errorf("failed to recover public key: %w", err)
	}

	// Verify recovered address matches main wallet
	recoveredAddr := crypto.PubkeyToAddress(*pubKey)
	expectedAddr := common.HexToAddress(req.MainWallet)

	return recoveredAddr == expectedAddr, nil
}

// GenerateSessionKey generates a new ephemeral session key pair.
func GenerateSessionKey() (privateKey string, publicAddress string, err error) {
	key, err := crypto.GenerateKey()
	if err != nil {
		return "", "", err
	}

	privateKeyBytes := crypto.FromECDSA(key)
	privateKey = hex.EncodeToString(privateKeyBytes)

	publicAddress = crypto.PubkeyToAddress(key.PublicKey).Hex()

	return privateKey, publicAddress, nil
}

// SignMessage signs a message with the given private key (for session key operations).
func SignMessage(privateKeyHex string, message []byte) (string, error) {
	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return "", fmt.Errorf("invalid private key: %w", err)
	}

	// Ethereum signed message prefix
	prefixedMessage := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	hash := crypto.Keccak256Hash([]byte(prefixedMessage))

	sig, err := crypto.Sign(hash.Bytes(), privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign: %w", err)
	}

	// Adjust v value for Ethereum compatibility
	sig[64] += 27

	return "0x" + hex.EncodeToString(sig), nil
}

// GetAddressFromPrivateKey derives the address from a private key.
func GetAddressFromPrivateKey(privateKeyHex string) (string, error) {
	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return "", fmt.Errorf("invalid private key: %w", err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("failed to get public key")
	}

	return crypto.PubkeyToAddress(*publicKeyECDSA).Hex(), nil
}

// Helper functions

func hexToBytes(hexStr string) ([]byte, error) {
	hexStr = strings.TrimPrefix(hexStr, "0x")
	return hex.DecodeString(hexStr)
}

func toWei(amount float64) *big.Int {
	// Convert to 18 decimals (wei)
	amountBig := new(big.Float).SetFloat64(amount)
	multiplier := new(big.Float).SetInt(big.NewInt(1e18))
	amountBig.Mul(amountBig, multiplier)

	result := new(big.Int)
	amountBig.Int(result)
	return result
}

// FromWei converts wei to float (18 decimals).
func FromWei(wei *big.Int) float64 {
	if wei == nil {
		return 0
	}
	weiBig := new(big.Float).SetInt(wei)
	divisor := new(big.Float).SetInt(big.NewInt(1e18))
	result, _ := new(big.Float).Quo(weiBig, divisor).Float64()
	return result
}
