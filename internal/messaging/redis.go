// Package messaging provides Redis Streams based messaging functionality.
package messaging

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	client     *redis.Client
	clientOnce sync.Once
	clientErr  error
)

// Config holds Redis connection configuration.
type Config struct {
	Host     string
	Port     int
	Password string
	DB       int
}

// DefaultConfig returns default Redis configuration.
func DefaultConfig() *Config {
	return &Config{
		Host:     "localhost",
		Port:     6379,
		Password: "",
		DB:       0,
	}
}

// GetClient returns a singleton Redis client.
func GetClient(cfg *Config) (*redis.Client, error) {
	clientOnce.Do(func() {
		client = redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			Password: cfg.Password,
			DB:       cfg.DB,
		})

		// Test connection
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, clientErr = client.Ping(ctx).Result()
		if clientErr != nil {
			clientErr = fmt.Errorf("failed to connect to Redis: %w", clientErr)
		}
	})

	return client, clientErr
}

// ResetClient resets the singleton for testing purposes.
func ResetClient() {
	clientOnce = sync.Once{}
	if client != nil {
		_ = client.Close()
	}
	client = nil
	clientErr = nil
}
