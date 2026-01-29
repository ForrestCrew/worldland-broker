package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/worldland/worldland-hub/internal/auth"
	"github.com/worldland/worldland-hub/internal/domain"
)

// mockSessionRepo implements domain.SessionRepository for testing
type mockSessionRepo struct {
	sessions map[string]*domain.Session
}

func newMockSessionRepo() *mockSessionRepo {
	return &mockSessionRepo{
		sessions: make(map[string]*domain.Session),
	}
}

func (m *mockSessionRepo) Create(ctx context.Context, session *domain.Session) error {
	m.sessions[session.Token] = session
	return nil
}

func (m *mockSessionRepo) GetByToken(ctx context.Context, token string) (*domain.Session, error) {
	session, ok := m.sessions[token]
	if !ok {
		return nil, errors.New("session not found")
	}
	return session, nil
}

func (m *mockSessionRepo) Delete(ctx context.Context, id string) error {
	for token, session := range m.sessions {
		if session.ID == id {
			delete(m.sessions, token)
			return nil
		}
	}
	return nil
}

func (m *mockSessionRepo) DeleteExpired(ctx context.Context) error {
	now := time.Now()
	for token, session := range m.sessions {
		if now.After(session.ExpiresAt) {
			delete(m.sessions, token)
		}
	}
	return nil
}

func TestSessionManager_CreateSession(t *testing.T) {
	repo := newMockSessionRepo()
	manager := auth.NewSessionManager(repo, 24*time.Hour)

	ctx := context.Background()
	token, err := manager.Create(ctx, "provider-123")
	require.NoError(t, err)
	assert.Len(t, token, 64) // 32 bytes hex encoded

	// Token should be stored in repo
	session, err := repo.GetByToken(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "provider-123", session.ProviderID)
	assert.Equal(t, token, session.Token)
}

func TestSessionManager_CreateSession_UniqueTokens(t *testing.T) {
	repo := newMockSessionRepo()
	manager := auth.NewSessionManager(repo, 24*time.Hour)

	ctx := context.Background()
	tokens := make(map[string]bool)

	// Create multiple sessions and ensure tokens are unique
	for i := 0; i < 100; i++ {
		token, err := manager.Create(ctx, "provider-123")
		require.NoError(t, err)
		assert.False(t, tokens[token], "Token should be unique")
		tokens[token] = true
	}
}

func TestSessionManager_ValidateSession_Valid(t *testing.T) {
	repo := newMockSessionRepo()
	manager := auth.NewSessionManager(repo, 24*time.Hour)

	ctx := context.Background()
	token, err := manager.Create(ctx, "provider-123")
	require.NoError(t, err)

	// Validate the session
	session, err := manager.Validate(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, "provider-123", session.ProviderID)
	assert.Equal(t, token, session.Token)
}

func TestSessionManager_ValidateSession_Expired(t *testing.T) {
	repo := newMockSessionRepo()
	// Use very short TTL
	manager := auth.NewSessionManager(repo, 1*time.Millisecond)

	ctx := context.Background()
	token, err := manager.Create(ctx, "provider-123")
	require.NoError(t, err)

	// Wait for session to expire
	time.Sleep(10 * time.Millisecond)

	// Validate should fail
	_, err = manager.Validate(ctx, token)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session expired")
}

func TestSessionManager_ValidateSession_NotFound(t *testing.T) {
	repo := newMockSessionRepo()
	manager := auth.NewSessionManager(repo, 24*time.Hour)

	ctx := context.Background()

	// Validate non-existent token
	_, err := manager.Validate(ctx, "nonexistenttoken1234567890abcdef1234567890abcdef12345678901234")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestSessionManager_ValidateSession_EmptyToken(t *testing.T) {
	repo := newMockSessionRepo()
	manager := auth.NewSessionManager(repo, 24*time.Hour)

	ctx := context.Background()

	// Validate empty token
	_, err := manager.Validate(ctx, "")
	assert.Error(t, err)
}

func TestSessionManager_TTL(t *testing.T) {
	repo := newMockSessionRepo()
	ttl := 1 * time.Hour
	manager := auth.NewSessionManager(repo, ttl)

	ctx := context.Background()
	token, err := manager.Create(ctx, "provider-123")
	require.NoError(t, err)

	// Check that expiration is set correctly
	session, err := repo.GetByToken(ctx, token)
	require.NoError(t, err)

	// ExpiresAt should be approximately TTL from now
	expectedExpiry := time.Now().Add(ttl)
	diff := session.ExpiresAt.Sub(expectedExpiry)
	assert.True(t, diff < time.Second && diff > -time.Second,
		"ExpiresAt should be approximately TTL from now")
}
