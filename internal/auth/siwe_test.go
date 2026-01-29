package auth_test

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	siwe "github.com/spruceid/siwe-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/worldland/worldland-hub/internal/auth"
)

// mockNonceValidator implements NonceValidator for testing
type mockNonceValidator struct {
	nonces map[string]time.Time
}

func (m *mockNonceValidator) ConsumeIfValid(ctx context.Context, nonce string) (bool, error) {
	exp, ok := m.nonces[nonce]
	if !ok || time.Now().After(exp) {
		return false, nil
	}
	delete(m.nonces, nonce)
	return true, nil
}

// Test wallet with known private key (DO NOT USE IN PRODUCTION)
const testPrivateKeyHex = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

// generateTestSignature creates a valid SIWE message and signature for testing
func generateTestSignature(t *testing.T, domain, address, nonce string) (string, string) {
	// Create SIWE message using the library's InitMessage function
	options := map[string]interface{}{
		"statement": "Sign in to Worldland GPU Rental Platform as Provider",
	}

	msg, err := siwe.InitMessage(domain, address, "https://"+domain, nonce, options)
	require.NoError(t, err)

	messageStr := msg.String()

	// Sign the message with test private key
	privateKey, err := crypto.HexToECDSA(testPrivateKeyHex)
	require.NoError(t, err)

	// Prepare message for signing (EIP-191)
	prefixedMessage := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(messageStr), messageStr)
	hash := crypto.Keccak256Hash([]byte(prefixedMessage))

	signature, err := crypto.Sign(hash.Bytes(), privateKey)
	require.NoError(t, err)

	// Adjust V value for Ethereum compatibility
	if signature[64] < 27 {
		signature[64] += 27
	}

	return messageStr, hexutil.Encode(signature)
}

// getTestAddress returns the address for the test private key
func getTestAddress() string {
	privateKey, _ := crypto.HexToECDSA(testPrivateKeyHex)
	publicKey := privateKey.Public()
	publicKeyECDSA, _ := publicKey.(*ecdsa.PublicKey)
	return crypto.PubkeyToAddress(*publicKeyECDSA).Hex()
}

func TestVerifySIWE_ValidSignature(t *testing.T) {
	testAddress := getTestAddress()
	nonce := "testnoncevalid01"

	nonceValidator := &mockNonceValidator{
		nonces: map[string]time.Time{
			nonce: time.Now().Add(5 * time.Minute),
		},
	}
	verifier := auth.NewSIWEVerifier("hub.worldland.io", nonceValidator)

	message, signature := generateTestSignature(t, "hub.worldland.io", testAddress, nonce)

	ctx := context.Background()
	address, err := verifier.Verify(ctx, message, signature)
	require.NoError(t, err)
	assert.Equal(t, testAddress, address)
}

func TestVerifySIWE_InvalidSignature(t *testing.T) {
	testAddress := getTestAddress()
	nonce := "testnonceinvsig1"

	nonceValidator := &mockNonceValidator{
		nonces: map[string]time.Time{
			nonce: time.Now().Add(5 * time.Minute),
		},
	}
	verifier := auth.NewSIWEVerifier("hub.worldland.io", nonceValidator)

	// Generate valid message but use invalid signature
	message, _ := generateTestSignature(t, "hub.worldland.io", testAddress, nonce)
	invalidSignature := "0x0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"

	ctx := context.Background()
	_, err := verifier.Verify(ctx, message, invalidSignature)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "signature verification failed")
}

func TestVerifySIWE_ExpiredNonce(t *testing.T) {
	testAddress := getTestAddress()
	nonce := "expirednonce0001"

	// Nonce is already expired (1 minute in the past)
	nonceValidator := &mockNonceValidator{
		nonces: map[string]time.Time{
			nonce: time.Now().Add(-1 * time.Minute),
		},
	}
	verifier := auth.NewSIWEVerifier("hub.worldland.io", nonceValidator)

	message, signature := generateTestSignature(t, "hub.worldland.io", testAddress, nonce)

	ctx := context.Background()
	_, err := verifier.Verify(ctx, message, signature)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid or expired nonce")
}

func TestVerifySIWE_DomainMismatch(t *testing.T) {
	testAddress := getTestAddress()
	nonce := "testnoncedomain1"

	nonceValidator := &mockNonceValidator{
		nonces: map[string]time.Time{
			nonce: time.Now().Add(5 * time.Minute),
		},
	}
	// Verifier expects hub.worldland.io
	verifier := auth.NewSIWEVerifier("hub.worldland.io", nonceValidator)

	// But message is signed for evil.com
	message, signature := generateTestSignature(t, "evil.com", testAddress, nonce)

	ctx := context.Background()
	_, err := verifier.Verify(ctx, message, signature)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "domain mismatch")
}

func TestVerifySIWE_MalformedMessage(t *testing.T) {
	nonceValidator := &mockNonceValidator{
		nonces: map[string]time.Time{},
	}
	verifier := auth.NewSIWEVerifier("hub.worldland.io", nonceValidator)

	message := "not a valid SIWE message at all"
	signature := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef12"

	ctx := context.Background()
	_, err := verifier.Verify(ctx, message, signature)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid SIWE message")
}

func TestVerifySIWE_MissingNonce(t *testing.T) {
	testAddress := getTestAddress()
	// Nonce doesn't exist in store
	nonceValidator := &mockNonceValidator{
		nonces: map[string]time.Time{},
	}
	verifier := auth.NewSIWEVerifier("hub.worldland.io", nonceValidator)

	// Generate a valid message but don't add nonce to validator
	message, signature := generateTestSignature(t, "hub.worldland.io", testAddress, "missingnonce0001")

	ctx := context.Background()
	_, err := verifier.Verify(ctx, message, signature)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid or expired nonce")
}
