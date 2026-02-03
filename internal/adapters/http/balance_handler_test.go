package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/settlement"
)

func TestGetBalance_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockProviderRepo := &mockBalanceProviderRepo{
		provider: &domain.Provider{
			ID:            "provider-1",
			WalletAddress: "0xUser123",
		},
	}
	mockSessionRepo := &mockBalanceSessionRepo{
		runningSessions:  []*domain.RentalSession{},
		pendingSessions:  []*domain.RentalSession{},
	}
	calculator := settlement.NewCalculator(mockSessionRepo)
	handler := NewBalanceHandler(calculator, mockSessionRepo, mockProviderRepo)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("provider_id", "provider-1")
	})
	router.GET("/balance", handler.GetBalance)

	req := httptest.NewRequest(http.MethodGet, "/balance", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp BalanceResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "0xUser123", resp.UserAddress)
	assert.Equal(t, "10000000000000000000", resp.TotalDeposit) // 10 ETH mock
	assert.Equal(t, "0", resp.PendingSettlement) // No active sessions
	assert.Equal(t, 0, resp.ActiveSessionCount)
}

func TestGetBalance_WithActiveRental(t *testing.T) {
	gin.SetMode(gin.TestMode)

	start := time.Now().Add(-10 * time.Minute)
	mockProviderRepo := &mockBalanceProviderRepo{
		provider: &domain.Provider{
			ID:            "provider-1",
			WalletAddress: "0xUser123",
		},
	}
	mockSessionRepo := &mockBalanceSessionRepo{
		runningSessions: []*domain.RentalSession{
			{
				ID:             "session-1",
				State:          domain.RentalStateRunning,
				StartTime:      &start,
				PricePerSecond: "1000000000000000", // 0.001 ETH/sec
			},
		},
		pendingSessions: []*domain.RentalSession{},
	}
	calculator := settlement.NewCalculator(mockSessionRepo)
	handler := NewBalanceHandler(calculator, mockSessionRepo, mockProviderRepo)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("provider_id", "provider-1")
	})
	router.GET("/balance", handler.GetBalance)

	req := httptest.NewRequest(http.MethodGet, "/balance", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp BalanceResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "0xUser123", resp.UserAddress)
	assert.Equal(t, 1, resp.ActiveSessionCount)
	assert.NotEqual(t, "0", resp.PendingSettlement) // Should have pending cost
	assert.Greater(t, resp.EstimatedMinutesRemaining, int64(0)) // Should have time remaining
}

func TestGetBalance_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockProviderRepo := &mockBalanceProviderRepo{}
	mockSessionRepo := &mockBalanceSessionRepo{}
	calculator := settlement.NewCalculator(mockSessionRepo)
	handler := NewBalanceHandler(calculator, mockSessionRepo, mockProviderRepo)

	router := gin.New()
	// Don't set provider_id in context
	router.GET("/balance", handler.GetBalance)

	req := httptest.NewRequest(http.MethodGet, "/balance", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// mockBalanceProviderRepo implements domain.ProviderRepository for testing
type mockBalanceProviderRepo struct {
	domain.ProviderRepository
	provider *domain.Provider
}

func (m *mockBalanceProviderRepo) GetByID(ctx context.Context, id string) (*domain.Provider, error) {
	if m.provider != nil && m.provider.ID == id {
		return m.provider, nil
	}
	return nil, assert.AnError
}

// mockBalanceSessionRepo implements domain.RentalSessionRepository for testing
type mockBalanceSessionRepo struct {
	domain.RentalSessionRepository
	runningSessions []*domain.RentalSession
	pendingSessions []*domain.RentalSession
}

func (m *mockBalanceSessionRepo) FindByUserAndState(ctx context.Context, user string, state domain.RentalSessionState) ([]*domain.RentalSession, error) {
	if state == domain.RentalStateRunning {
		return m.runningSessions, nil
	}
	return nil, nil
}

func (m *mockBalanceSessionRepo) FindPendingSettlement(ctx context.Context, user string) ([]*domain.RentalSession, error) {
	return m.pendingSessions, nil
}
