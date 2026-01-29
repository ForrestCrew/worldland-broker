package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/worldland/worldland-hub/internal/domain"
)

// SessionManager handles session creation and validation
type SessionManager struct {
	repo domain.SessionRepository
	ttl  time.Duration
}

// NewSessionManager creates a new session manager
func NewSessionManager(repo domain.SessionRepository, ttl time.Duration) *SessionManager {
	return &SessionManager{
		repo: repo,
		ttl:  ttl,
	}
}

// Create creates a new session for the given provider
// Returns the session token (64 hex characters = 32 bytes)
func (m *SessionManager) Create(ctx context.Context, providerID string) (string, error) {
	// Generate cryptographically random token (32 bytes)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	// Generate session ID
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", fmt.Errorf("failed to generate session ID: %w", err)
	}
	sessionID := hex.EncodeToString(idBytes)

	session := &domain.Session{
		ID:         sessionID,
		ProviderID: providerID,
		Token:      token,
		ExpiresAt:  time.Now().Add(m.ttl),
		CreatedAt:  time.Now(),
	}

	if err := m.repo.Create(ctx, session); err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	return token, nil
}

// Validate validates a session token and returns the session if valid
func (m *SessionManager) Validate(ctx context.Context, token string) (*domain.Session, error) {
	if token == "" {
		return nil, fmt.Errorf("session not found")
	}

	session, err := m.repo.GetByToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("session not found")
	}

	if time.Now().After(session.ExpiresAt) {
		return nil, fmt.Errorf("session expired")
	}

	return session, nil
}
