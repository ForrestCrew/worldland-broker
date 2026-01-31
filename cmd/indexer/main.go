// Package main provides the entry point for the standalone indexer binary.
// The indexer continuously syncs blockchain events (Deposit, Withdraw, RentalStarted, RentalStopped)
// and stores them in the database for historical querying and analytics.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/worldland/worldland-hub/internal/blockchain"
	"github.com/worldland/worldland-hub/internal/indexer"
)

func main() {
	// Setup JSON logger for production (structured logging)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("Worldland Indexer starting...")

	// Load config
	cfg := indexer.LoadIndexerConfig()
	if cfg.ContractAddress == "" {
		logger.Error("CONTRACT_ADDRESS is required")
		os.Exit(1)
	}

	// Setup context with cancellation for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals (SIGINT, SIGTERM)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("received shutdown signal", "signal", sig)
		cancel()
	}()

	// Connect to database
	dbpool, err := pgxpool.New(ctx, cfg.DatabaseURL())
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbpool.Close()
	logger.Info("connected to database", "host", cfg.DBHost, "database", cfg.DBName)

	// Initialize checkpoint store with indexer-specific checkpoint ID
	// This allows Hub and Indexer to maintain independent checkpoints
	checkpointStore := blockchain.NewCheckpointStore(dbpool, "indexer_events")

	// Initialize indexer repository and processor
	repo := indexer.NewIndexerRepository(dbpool)
	processor := indexer.NewIndexerProcessor(repo)

	// Initialize event listener with IndexerProcessor as the handler
	contractAddr := common.HexToAddress(cfg.ContractAddress)
	listener := blockchain.NewEventListener(
		cfg.RPCEndpoints,
		contractAddr,
		checkpointStore,
		processor,
		logger,
	)

	// Start indexing
	logger.Info("starting indexer",
		"contract", cfg.ContractAddress,
		"rpc_endpoints", cfg.RPCEndpoints,
		"deployment_block", cfg.DeploymentBlock,
		"finality_blocks", cfg.FinalityBlocks,
	)

	if err := listener.Start(ctx); err != nil {
		if ctx.Err() == context.Canceled {
			logger.Info("indexer stopped gracefully")
		} else {
			logger.Error("indexer stopped with error", "error", err)
			os.Exit(1)
		}
	}

	logger.Info("indexer shutdown complete")
}
