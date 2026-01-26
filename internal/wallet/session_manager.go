// Package wallet provides session key management.
package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// SessionManager manages session keys with Redis backend.
type SessionManager struct {
	redis    *redis.Client
	verifier *Verifier
	mu       sync.RWMutex
	// In-memory cache for frequently accessed sessions
	cache map[string]*Session
}

// Session represents an active session.
type Session struct {
	MainWallet  string    `json:"main_wallet"`
	SessionKey  string    `json:"session_key"`
	SpendLimit  float64   `json:"spend_limit"`
	SpentAmount float64   `json:"spent_amount"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
	Permissions []string  `json:"permissions"`
	IsActive    bool      `json:"is_active"`
}

// SessionManagerConfig holds configuration for session manager.
type SessionManagerConfig struct {
	ContractName    string
	ContractVersion string
	ChainID         int64
	ContractAddress string
}

// NewSessionManager creates a new session manager.
func NewSessionManager(redisClient *redis.Client, cfg *SessionManagerConfig) *SessionManager {
	return &SessionManager{
		redis:    redisClient,
		verifier: NewVerifier(cfg.ContractName, cfg.ContractVersion, cfg.ChainID, cfg.ContractAddress),
		cache:    make(map[string]*Session),
	}
}

// RegisterSession registers a new session key.
func (sm *SessionManager) RegisterSession(ctx context.Context, req *SessionKeyRequest) (*Session, error) {
	// Get current nonce for the wallet
	nonce, err := sm.getNonce(ctx, req.MainWallet)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	// Verify signature
	valid, err := sm.verifier.VerifySessionKeySignature(req, nonce)
	if err != nil {
		return nil, fmt.Errorf("signature verification failed: %w", err)
	}
	if !valid {
		return nil, fmt.Errorf("invalid signature")
	}

	// Increment nonce
	if err := sm.incrementNonce(ctx, req.MainWallet); err != nil {
		return nil, fmt.Errorf("failed to increment nonce: %w", err)
	}

	// Create session
	session := &Session{
		MainWallet:  req.MainWallet,
		SessionKey:  req.SessionKey,
		SpendLimit:  req.SpendLimit,
		SpentAmount: 0,
		ExpiresAt:   time.Now().Add(time.Duration(req.Duration) * time.Second),
		CreatedAt:   time.Now(),
		LastUsedAt:  time.Now(),
		Permissions: []string{"CREATE_JOB", "TERMINATE_JOB", "VIEW_JOBS"},
		IsActive:    true,
	}

	// Store in Redis
	if err := sm.storeSession(ctx, session); err != nil {
		return nil, fmt.Errorf("failed to store session: %w", err)
	}

	// Update cache
	sm.mu.Lock()
	sm.cache[req.SessionKey] = session
	sm.mu.Unlock()

	return session, nil
}

// ValidateSession validates a session key and returns the session.
func (sm *SessionManager) ValidateSession(ctx context.Context, sessionKey string) (*Session, error) {
	// Check cache first
	sm.mu.RLock()
	if session, ok := sm.cache[sessionKey]; ok {
		sm.mu.RUnlock()
		if session.IsActive && time.Now().Before(session.ExpiresAt) {
			return session, nil
		}
	} else {
		sm.mu.RUnlock()
	}

	// Load from Redis
	session, err := sm.loadSession(ctx, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}

	// Validate
	if !session.IsActive {
		return nil, fmt.Errorf("session is revoked")
	}
	if time.Now().After(session.ExpiresAt) {
		return nil, fmt.Errorf("session expired")
	}

	// Update cache
	sm.mu.Lock()
	sm.cache[sessionKey] = session
	sm.mu.Unlock()

	return session, nil
}

// ValidateAndCharge validates session and checks if a charge is allowed.
func (sm *SessionManager) ValidateAndCharge(ctx context.Context, sessionKey string, amount float64) (*Session, error) {
	session, err := sm.ValidateSession(ctx, sessionKey)
	if err != nil {
		return nil, err
	}

	// Check spend limit
	if session.SpentAmount+amount > session.SpendLimit {
		return nil, fmt.Errorf("spend limit exceeded: limit=%f, spent=%f, requested=%f",
			session.SpendLimit, session.SpentAmount, amount)
	}

	// Update spent amount
	session.SpentAmount += amount
	session.LastUsedAt = time.Now()

	// Store updated session
	if err := sm.storeSession(ctx, session); err != nil {
		return nil, fmt.Errorf("failed to update session: %w", err)
	}

	// Update cache
	sm.mu.Lock()
	sm.cache[sessionKey] = session
	sm.mu.Unlock()

	return session, nil
}

// RevokeSession revokes a session key.
func (sm *SessionManager) RevokeSession(ctx context.Context, mainWallet, sessionKey string) error {
	session, err := sm.loadSession(ctx, sessionKey)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	if session.MainWallet != mainWallet {
		return fmt.Errorf("unauthorized: not session owner")
	}

	session.IsActive = false

	if err := sm.storeSession(ctx, session); err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	// Remove from cache
	sm.mu.Lock()
	delete(sm.cache, sessionKey)
	sm.mu.Unlock()

	return nil
}

// GetSessionsByWallet returns all sessions for a main wallet.
func (sm *SessionManager) GetSessionsByWallet(ctx context.Context, mainWallet string) ([]*Session, error) {
	pattern := fmt.Sprintf("session:*")
	keys, err := sm.redis.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, err
	}

	var sessions []*Session
	for _, key := range keys {
		data, err := sm.redis.Get(ctx, key).Bytes()
		if err != nil {
			continue
		}

		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}

		if session.MainWallet == mainWallet {
			sessions = append(sessions, &session)
		}
	}

	return sessions, nil
}

// GetRemainingQuota returns the remaining spend quota for a session.
func (sm *SessionManager) GetRemainingQuota(ctx context.Context, sessionKey string) (float64, error) {
	session, err := sm.ValidateSession(ctx, sessionKey)
	if err != nil {
		return 0, err
	}

	return session.SpendLimit - session.SpentAmount, nil
}

// Helper methods

func (sm *SessionManager) storeSession(ctx context.Context, session *Session) error {
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("session:%s", session.SessionKey)
	ttl := time.Until(session.ExpiresAt)
	if ttl <= 0 {
		ttl = time.Hour // Minimum TTL for cleanup
	}

	return sm.redis.Set(ctx, key, data, ttl).Err()
}

func (sm *SessionManager) loadSession(ctx context.Context, sessionKey string) (*Session, error) {
	key := fmt.Sprintf("session:%s", sessionKey)
	data, err := sm.redis.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}

	return &session, nil
}

func (sm *SessionManager) getNonce(ctx context.Context, wallet string) (uint64, error) {
	key := fmt.Sprintf("nonce:%s", wallet)
	val, err := sm.redis.Get(ctx, key).Uint64()
	if err == redis.Nil {
		return 0, nil
	}
	return val, err
}

func (sm *SessionManager) incrementNonce(ctx context.Context, wallet string) error {
	key := fmt.Sprintf("nonce:%s", wallet)
	return sm.redis.Incr(ctx, key).Err()
}

// SpendLimitToWei converts a float spend limit to big.Int (wei).
func SpendLimitToWei(amount float64) *big.Int {
	// Convert to 18 decimals
	amountBig := new(big.Float).SetFloat64(amount)
	multiplier := new(big.Float).SetInt(big.NewInt(1e18))
	amountBig.Mul(amountBig, multiplier)

	result := new(big.Int)
	amountBig.Int(result)
	return result
}
