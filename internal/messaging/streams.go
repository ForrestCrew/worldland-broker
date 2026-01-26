// Package messaging provides Redis Streams functionality for provider communication.
package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Producer publishes messages to Redis Streams.
type Producer struct {
	client *redis.Client
}

// NewProducer creates a new Producer with the given Redis client.
func NewProducer(client *redis.Client) *Producer {
	return &Producer{client: client}
}

// Publish publishes a message to a Redis stream.
func (p *Producer) Publish(ctx context.Context, stream string, data interface{}) (string, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal message: %w", err)
	}

	result, err := p.client.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		Values: map[string]interface{}{
			"data":      string(payload),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		},
	}).Result()

	if err != nil {
		return "", fmt.Errorf("failed to publish to stream %s: %w", stream, err)
	}

	return result, nil
}

// Consumer consumes messages from Redis Streams using consumer groups.
type Consumer struct {
	client        *redis.Client
	stream        string
	group         string
	consumer      string
	blockDuration time.Duration
}

// ConsumerConfig holds configuration for creating a Consumer.
type ConsumerConfig struct {
	Stream        string
	Group         string
	Consumer      string
	BlockDuration time.Duration
}

// NewConsumer creates a new Consumer.
func NewConsumer(client *redis.Client, cfg *ConsumerConfig) (*Consumer, error) {
	c := &Consumer{
		client:        client,
		stream:        cfg.Stream,
		group:         cfg.Group,
		consumer:      cfg.Consumer,
		blockDuration: cfg.BlockDuration,
	}

	if c.blockDuration == 0 {
		c.blockDuration = 5 * time.Second
	}

	// Create consumer group if it doesn't exist
	ctx := context.Background()
	err := client.XGroupCreateMkStream(ctx, cfg.Stream, cfg.Group, "$").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return nil, fmt.Errorf("failed to create consumer group: %w", err)
	}

	return c, nil
}

// Message represents a message from Redis Streams.
type Message struct {
	ID        string
	Stream    string
	Data      []byte
	Timestamp time.Time
}

// ReadMessages reads messages from the stream. Returns nil if no messages (timeout).
func (c *Consumer) ReadMessages(ctx context.Context, count int64) ([]Message, error) {
	streams, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    c.group,
		Consumer: c.consumer,
		Streams:  []string{c.stream, ">"},
		Count:    count,
		Block:    c.blockDuration,
	}).Result()

	if err != nil {
		if err == redis.Nil {
			return nil, nil // No messages
		}
		return nil, fmt.Errorf("failed to read from stream: %w", err)
	}

	var messages []Message
	for _, stream := range streams {
		for _, msg := range stream.Messages {
			data, ok := msg.Values["data"].(string)
			if !ok {
				continue
			}

			timestamp := time.Now()
			if ts, ok := msg.Values["timestamp"].(string); ok {
				if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
					timestamp = parsed
				}
			}

			messages = append(messages, Message{
				ID:        msg.ID,
				Stream:    stream.Stream,
				Data:      []byte(data),
				Timestamp: timestamp,
			})
		}
	}

	return messages, nil
}

// Ack acknowledges a message has been processed.
func (c *Consumer) Ack(ctx context.Context, messageID string) error {
	return c.client.XAck(ctx, c.stream, c.group, messageID).Err()
}

// Unmarshal unmarshals message data into the target interface.
func (m *Message) Unmarshal(target interface{}) error {
	return json.Unmarshal(m.Data, target)
}
