package http

import (
	"math/big"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/worldland/worldland-hub/internal/domain"
	"github.com/worldland/worldland-hub/internal/settlement"
)

// BalanceResponse for GET /api/v1/balance
type BalanceResponse struct {
	UserAddress               string `json:"userAddress"`
	TotalDeposit              string `json:"totalDeposit"`                        // From blockchain
	TotalSettled              string `json:"totalSettled"`                        // Already settled
	PendingSettlement         string `json:"pendingSettlement"`                   // RUNNING + STOPPED unsettled
	AvailableBalance          string `json:"availableBalance"`                    // Deposit - Settled - Pending
	EstimatedMinutesRemaining int64  `json:"estimatedMinutesRemaining,omitempty"` // If active rental
	ActiveSessionCount        int    `json:"activeSessionCount"`
}

// BalanceHandler handles balance-related endpoints
type BalanceHandler struct {
	calculator   *settlement.Calculator
	sessionRepo  domain.RentalSessionRepository
	providerRepo domain.ProviderRepository
	// depositService for blockchain deposit lookup (mock for now)
}

// NewBalanceHandler creates a balance handler
func NewBalanceHandler(
	calculator *settlement.Calculator,
	sessionRepo domain.RentalSessionRepository,
	providerRepo domain.ProviderRepository,
) *BalanceHandler {
	return &BalanceHandler{
		calculator:   calculator,
		sessionRepo:  sessionRepo,
		providerRepo: providerRepo,
	}
}

// GetBalance handles GET /api/v1/balance
func (h *BalanceHandler) GetBalance(c *gin.Context) {
	// Get authenticated user
	providerID, exists := c.Get("provider_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	provider, err := h.providerRepo.GetByID(c.Request.Context(), providerID.(string))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "provider not found"})
		return
	}

	userAddress := provider.WalletAddress

	// Get deposit from blockchain (mock for now - will integrate with contract)
	// TODO: Call contract to get actual deposit
	totalDeposit := new(big.Int)
	totalDeposit.SetString("10000000000000000000", 10) // Mock: 10 ETH

	// Get total settled amount (sum of settled sessions)
	totalSettled := big.NewInt(0) // TODO: Query from settled sessions

	// Calculate pending settlement
	pendingSettlement, err := h.calculator.CalculatePendingSettlement(c.Request.Context(), userAddress)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to calculate pending settlement"})
		return
	}

	// Calculate available balance
	availableBalance := new(big.Int).Sub(totalDeposit, totalSettled)
	availableBalance.Sub(availableBalance, pendingSettlement)

	// Count active sessions
	runningSessions, _ := h.sessionRepo.FindByUserAndState(c.Request.Context(), userAddress, domain.RentalStateRunning)
	activeCount := len(runningSessions)

	// Estimate remaining time if active rental
	var estimatedMinutes int64
	if activeCount > 0 && len(runningSessions) > 0 {
		// Use rate from first active session
		pricePerSecond, _ := new(big.Int).SetString(runningSessions[0].PricePerSecond, 10)
		estimatedMinutes = h.calculator.CalculateEstimatedMinutesRemaining(availableBalance, pricePerSecond)
	}

	c.JSON(http.StatusOK, BalanceResponse{
		UserAddress:               userAddress,
		TotalDeposit:              totalDeposit.String(),
		TotalSettled:              totalSettled.String(),
		PendingSettlement:         pendingSettlement.String(),
		AvailableBalance:          availableBalance.String(),
		EstimatedMinutesRemaining: estimatedMinutes,
		ActiveSessionCount:        activeCount,
	})
}
