// Package blockchain provides event listener for blockchain event subscription.
package blockchain

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	// FinalityBlocks is the number of blocks to wait for BNB Chain finality.
	// BNB Chain uses PoSA consensus with ~15-21 block finality.
	FinalityBlocks = 15

	// BackfillBatchSize is the maximum number of blocks to fetch in a single backfill query.
	// Prevents RPC timeouts on large ranges.
	BackfillBatchSize = 1000
)

// EventHandler processes parsed blockchain events.
// Implementations handle business logic for rental lifecycle events.
type EventHandler interface {
	HandleRentalStarted(ctx context.Context, event *RentalStartedEvent) error
	HandleRentalStopped(ctx context.Context, event *RentalStoppedEvent) error
}

// EthClient defines the interface for Ethereum client operations.
// This abstraction allows for testing with mock clients.
type EthClient interface {
	BlockNumber(ctx context.Context) (uint64, error)
	FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error)
	SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error)
	Close()
}

// DialFunc is a function type for dialing Ethereum clients.
// Can be replaced in tests to inject mock clients.
type DialFunc func(ctx context.Context, url string) (EthClient, error)

// DefaultDialFunc connects to a real Ethereum node via ethclient.
func DefaultDialFunc(ctx context.Context, url string) (EthClient, error) {
	return ethclient.DialContext(ctx, url)
}

// EventListener listens for WorldlandRental contract events via WebSocket.
// It supports auto-reconnection with exponential backoff and checkpoint-based
// event replay on restart.
type EventListener struct {
	rpcURLs         []string
	contractAddress common.Address
	checkpoint      *CheckpointStore
	handler         EventHandler
	logger          *slog.Logger
	dialFunc        DialFunc
}

// NewEventListener creates a new event listener.
// rpcURLs should contain one or more WebSocket RPC endpoints for failover.
func NewEventListener(
	rpcURLs []string,
	contractAddress common.Address,
	checkpoint *CheckpointStore,
	handler EventHandler,
	logger *slog.Logger,
) *EventListener {
	return &EventListener{
		rpcURLs:         rpcURLs,
		contractAddress: contractAddress,
		checkpoint:      checkpoint,
		handler:         handler,
		logger:          logger,
		dialFunc:        DefaultDialFunc,
	}
}

// SetDialFunc sets a custom dial function for testing.
func (l *EventListener) SetDialFunc(fn DialFunc) {
	l.dialFunc = fn
}

// Start begins listening for events with auto-reconnection.
// Uses exponential backoff for resilient operation across network failures.
// Returns when context is canceled.
func (l *EventListener) Start(ctx context.Context) error {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 1 * time.Second
	bo.MaxInterval = 60 * time.Second
	bo.MaxElapsedTime = 0 // Never stop retrying

	return backoff.Retry(func() error {
		err := l.subscribe(ctx)
		if ctx.Err() != nil {
			return backoff.Permanent(ctx.Err())
		}
		l.logger.Warn("subscription disconnected, reconnecting", "error", err)
		return err
	}, backoff.WithContext(bo, ctx))
}

// subscribe creates WebSocket subscription and processes events.
func (l *EventListener) subscribe(ctx context.Context) error {
	// Connect with failover
	client, err := l.dialWithFailover(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	// Backfill missed events first
	if err := l.backfill(ctx, client); err != nil {
		l.logger.Error("backfill failed", "error", err)
		// Continue to subscription even if backfill fails
	}

	// Create subscription query for contract events
	query := ethereum.FilterQuery{
		Addresses: []common.Address{l.contractAddress},
	}

	logs := make(chan types.Log)
	sub, err := client.SubscribeFilterLogs(ctx, query, logs)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	l.logger.Info("subscribed to contract events", "address", l.contractAddress.Hex())

	for {
		select {
		case err := <-sub.Err():
			return fmt.Errorf("subscription error: %w", err)
		case vLog := <-logs:
			if err := l.processLog(ctx, vLog); err != nil {
				l.logger.Error("process log failed", "error", err, "txHash", vLog.TxHash.Hex())
				// Continue processing other events
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// dialWithFailover tries multiple RPC endpoints until one succeeds.
// Returns the first successful connection or an error if all fail.
func (l *EventListener) dialWithFailover(ctx context.Context) (EthClient, error) {
	for _, rpc := range l.rpcURLs {
		client, err := l.dialFunc(ctx, rpc)
		if err == nil {
			l.logger.Info("connected to RPC", "url", rpc)
			return client, nil
		}
		l.logger.Warn("RPC connection failed", "url", rpc, "error", err)
	}
	return nil, fmt.Errorf("all RPC endpoints failed")
}

// backfill fetches missed events from last checkpoint.
// Processes events in batches to avoid RPC timeouts.
func (l *EventListener) backfill(ctx context.Context, client EthClient) error {
	lastBlock, err := l.checkpoint.GetLastProcessedBlock(ctx)
	if err != nil {
		return err
	}

	currentBlock, err := client.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("get block number: %w", err)
	}

	// Nothing to backfill
	if lastBlock >= currentBlock {
		l.logger.Info("checkpoint up to date", "block", lastBlock)
		return nil
	}

	l.logger.Info("starting backfill", "from", lastBlock+1, "to", currentBlock)

	// Process in batches to avoid RPC timeouts
	for fromBlock := lastBlock + 1; fromBlock <= currentBlock; fromBlock += BackfillBatchSize {
		toBlock := fromBlock + BackfillBatchSize - 1
		if toBlock > currentBlock {
			toBlock = currentBlock
		}

		query := ethereum.FilterQuery{
			Addresses: []common.Address{l.contractAddress},
			FromBlock: new(big.Int).SetUint64(fromBlock),
			ToBlock:   new(big.Int).SetUint64(toBlock),
		}

		logs, err := client.FilterLogs(ctx, query)
		if err != nil {
			return fmt.Errorf("filter logs: %w", err)
		}

		for _, vLog := range logs {
			if err := l.processLog(ctx, vLog); err != nil {
				l.logger.Error("backfill process failed", "error", err, "txHash", vLog.TxHash.Hex())
				// Continue with other logs
			}
		}

		l.logger.Info("backfill progress", "from", fromBlock, "to", toBlock, "events", len(logs))
	}

	return nil
}

// processLog parses and handles a single log entry.
// Implements idempotency check, event parsing, handler dispatch, and checkpoint update.
func (l *EventListener) processLog(ctx context.Context, vLog types.Log) error {
	txHash := vLog.TxHash.Hex()

	// Idempotency check - skip if already processed
	processed, err := l.checkpoint.IsProcessed(ctx, txHash)
	if err != nil {
		return fmt.Errorf("check processed: %w", err)
	}
	if processed {
		l.logger.Debug("skipping duplicate event", "txHash", txHash)
		return nil // Already processed, skip
	}

	eventType := IdentifyEvent(vLog)
	var processErr error

	switch eventType {
	case EventTypeRentalStarted:
		event, err := ParseRentalStarted(vLog)
		if err != nil {
			processErr = fmt.Errorf("parse RentalStarted: %w", err)
		} else {
			processErr = l.handler.HandleRentalStarted(ctx, event)
		}

	case EventTypeRentalStopped:
		event, err := ParseRentalStopped(vLog)
		if err != nil {
			processErr = fmt.Errorf("parse RentalStopped: %w", err)
		} else {
			processErr = l.handler.HandleRentalStopped(ctx, event)
		}

	default:
		// Unknown or non-rental event, skip silently
		return nil
	}

	if processErr != nil {
		// Save to dead letter queue for later inspection/retry
		saveErr := l.checkpoint.SaveFailedEvent(ctx, txHash, vLog.BlockNumber, int(vLog.Index), string(eventType), vLog.Data, processErr.Error())
		if saveErr != nil {
			l.logger.Error("failed to save failed event", "error", saveErr)
		}
		return processErr
	}

	// Mark as processed for idempotency
	if err := l.checkpoint.MarkProcessed(ctx, txHash, vLog.BlockNumber, int(vLog.Index), string(eventType)); err != nil {
		return fmt.Errorf("mark processed: %w", err)
	}

	// Update checkpoint to this block
	// Note: In production, should wait for finality (current - FinalityBlocks)
	if err := l.checkpoint.UpdateCheckpoint(ctx, vLog.BlockNumber); err != nil {
		l.logger.Warn("update checkpoint failed", "error", err)
	}

	l.logger.Info("processed event", "type", eventType, "txHash", txHash, "block", vLog.BlockNumber)
	return nil
}
