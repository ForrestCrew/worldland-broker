package auth

import (
	"context"
	"fmt"

	siwe "github.com/spruceid/siwe-go"
)

// NonceValidator defines the interface for nonce validation
// This allows dependency injection for testing and different storage backends
type NonceValidator interface {
	ConsumeIfValid(ctx context.Context, nonce string) (bool, error)
}

// SIWEVerifier handles SIWE (Sign-In with Ethereum) message verification
// It validates signatures, domains, nonces, and timestamps according to EIP-4361
type SIWEVerifier struct {
	expectedDomain string
	nonceValidator NonceValidator
}

// NewSIWEVerifier creates a new SIWE verifier with the specified domain and nonce validator
func NewSIWEVerifier(domain string, nonceValidator NonceValidator) *SIWEVerifier {
	return &SIWEVerifier{
		expectedDomain: domain,
		nonceValidator: nonceValidator,
	}
}

// Verify validates a SIWE message and signature
// Returns the wallet address on success, error on failure
//
// Validation steps:
// 1. Parse SIWE message (EIP-4361 format)
// 2. Validate domain matches expected domain
// 3. Validate and consume nonce atomically (prevents replay attacks)
// 4. Verify cryptographic signature
func (v *SIWEVerifier) Verify(ctx context.Context, message, signature string) (string, error) {
	// Parse SIWE message according to EIP-4361 specification
	parsedMessage, err := siwe.ParseMessage(message)
	if err != nil {
		return "", fmt.Errorf("invalid SIWE message: %w", err)
	}

	// Validate domain to prevent phishing attacks
	if parsedMessage.GetDomain() != v.expectedDomain {
		return "", fmt.Errorf("domain mismatch: expected %s, got %s",
			v.expectedDomain, parsedMessage.GetDomain())
	}

	// Validate and atomically consume nonce to prevent replay attacks
	// The nonce can only be used once, and ConsumeIfValid handles this atomically
	consumed, err := v.nonceValidator.ConsumeIfValid(ctx, parsedMessage.GetNonce())
	if err != nil {
		return "", fmt.Errorf("nonce validation error: %w", err)
	}
	if !consumed {
		return "", fmt.Errorf("invalid or expired nonce")
	}

	// Verify the cryptographic signature
	// This recovers the public key from the signature and verifies it matches the address
	_, err = parsedMessage.Verify(signature, &v.expectedDomain, nil, nil)
	if err != nil {
		return "", fmt.Errorf("signature verification failed: %w", err)
	}

	// Return the Ethereum address that signed the message
	return parsedMessage.GetAddress().String(), nil
}
