// Package indexer provides IndexerProcessor that implements EventHandler
// for persisting blockchain events to the indexer database tables.
package indexer

import (
	"context"
	"strings"
	"time"

	"github.com/worldland/worldland-hub/internal/blockchain"
)

// IndexerProcessor implements blockchain.EventHandler for storing
// blockchain events in the indexer database tables.
// Unlike the session EventProcessor, this processor persists ALL events
// for historical querying and analytics.
type IndexerProcessor struct {
	repo *IndexerRepository
}

// Compile-time interface verification
var _ blockchain.EventHandler = (*IndexerProcessor)(nil)

// NewIndexerProcessor creates a new IndexerProcessor with the given repository.
func NewIndexerProcessor(repo *IndexerRepository) *IndexerProcessor {
	return &IndexerProcessor{repo: repo}
}

// HandleDeposited processes Deposited events and stores them in deposit_withdraw_history.
// Converts the blockchain event to a DepositWithdrawRecord with event_type "deposit".
func (p *IndexerProcessor) HandleDeposited(ctx context.Context, event *blockchain.DepositedEvent) error {
	record := DepositWithdrawRecord{
		UserAddress:    strings.ToLower(event.User.Hex()),
		EventType:      "deposit",
		Amount:         event.Amount.String(),
		TxHash:         event.TxHash.Hex(),
		BlockNumber:    event.BlockNumber,
		BlockTimestamp: time.Now(), // Approximate; block_number is authoritative for ordering
		LogIndex:       int(event.LogIndex),
	}

	return p.repo.InsertDepositWithdraw(ctx, record)
}

// HandleWithdrawn processes Withdrawn events and stores them in deposit_withdraw_history.
// Converts the blockchain event to a DepositWithdrawRecord with event_type "withdraw".
func (p *IndexerProcessor) HandleWithdrawn(ctx context.Context, event *blockchain.WithdrawnEvent) error {
	record := DepositWithdrawRecord{
		UserAddress:    strings.ToLower(event.User.Hex()),
		EventType:      "withdraw",
		Amount:         event.Amount.String(),
		TxHash:         event.TxHash.Hex(),
		BlockNumber:    event.BlockNumber,
		BlockTimestamp: time.Now(), // Approximate; block_number is authoritative for ordering
		LogIndex:       int(event.LogIndex),
	}

	return p.repo.InsertDepositWithdraw(ctx, record)
}

// HandleRentalStarted processes RentalStarted events and stores them in rental_history.
// Converts the blockchain event to a RentalStartedRecord for the initial rental row.
func (p *IndexerProcessor) HandleRentalStarted(ctx context.Context, event *blockchain.RentalStartedEvent) error {
	record := RentalStartedRecord{
		RentalID:        event.RentalID,
		UserAddress:     strings.ToLower(event.User.Hex()),
		ProviderAddress: strings.ToLower(event.Provider.Hex()),
		StartTime:       time.Unix(int64(event.StartTime), 0),
		TxHash:          event.TxHash.Hex(),
		BlockNumber:     event.BlockNumber,
	}

	return p.repo.InsertRentalStarted(ctx, record)
}

// HandleRentalStopped processes RentalStopped events and updates rental_history.
// Converts the blockchain event to a RentalStoppedRecord to update the existing row.
func (p *IndexerProcessor) HandleRentalStopped(ctx context.Context, event *blockchain.RentalStoppedEvent) error {
	record := RentalStoppedRecord{
		RentalID:    event.RentalID,
		EndTime:     time.Unix(int64(event.EndTime), 0),
		CostWei:     event.Cost.String(),
		TxHash:      event.TxHash.Hex(),
		BlockNumber: event.BlockNumber,
	}

	return p.repo.UpdateRentalStopped(ctx, record)
}
