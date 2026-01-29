package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/worldland/worldland-hub/internal/auth"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/middleware"
)

type mockSessionRepo struct {
	sessions map[string]*domain.Session
}

func newMockSessionRepo() *mockSessionRepo {
	return &mockSessionRepo{sessions: make(map[string]*domain.Session)}
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
	return nil
}

func (m *mockSessionRepo) DeleteExpired(ctx context.Context) error {
	return nil
}

func setupTestRouter(sessionManager *auth.SessionManager) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	protected := router.Group("/protected")
	protected.Use(middleware.AuthMiddleware(sessionManager))
	protected.GET("/resource", func(c *gin.Context) {
		providerID := c.GetString("provider_id")
		c.JSON(http.StatusOK, gin.H{"provider_id": providerID})
	})

	return router
}

func TestAuthMiddleware_MissingHeader_Returns401(t *testing.T) {
	sessionRepo := newMockSessionRepo()
	sessionManager := auth.NewSessionManager(sessionRepo, 24*time.Hour)
	router := setupTestRouter(sessionManager)

	req := httptest.NewRequest("GET", "/protected/resource", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_InvalidFormat_Returns401(t *testing.T) {
	sessionRepo := newMockSessionRepo()
	sessionManager := auth.NewSessionManager(sessionRepo, 24*time.Hour)
	router := setupTestRouter(sessionManager)

	req := httptest.NewRequest("GET", "/protected/resource", nil)
	req.Header.Set("Authorization", "Basic abc123")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_InvalidToken_Returns401(t *testing.T) {
	sessionRepo := newMockSessionRepo()
	sessionManager := auth.NewSessionManager(sessionRepo, 24*time.Hour)
	router := setupTestRouter(sessionManager)

	req := httptest.NewRequest("GET", "/protected/resource", nil)
	req.Header.Set("Authorization", "Bearer invalidtoken")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_ValidToken_SetsProviderID(t *testing.T) {
	sessionRepo := newMockSessionRepo()
	sessionManager := auth.NewSessionManager(sessionRepo, 24*time.Hour)

	// Create a valid session
	ctx := context.Background()
	token, _ := sessionManager.Create(ctx, "provider-123")

	router := setupTestRouter(sessionManager)

	req := httptest.NewRequest("GET", "/protected/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "provider-123")
}

func TestAuthMiddleware_ExpiredToken_Returns401(t *testing.T) {
	sessionRepo := newMockSessionRepo()
	// Very short TTL
	sessionManager := auth.NewSessionManager(sessionRepo, 1*time.Millisecond)

	// Create a session that will expire immediately
	ctx := context.Background()
	token, _ := sessionManager.Create(ctx, "provider-123")

	// Wait for expiry
	time.Sleep(10 * time.Millisecond)

	router := setupTestRouter(sessionManager)

	req := httptest.NewRequest("GET", "/protected/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
