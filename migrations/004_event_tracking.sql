-- Migration: 004_event_tracking.sql
-- Purpose: Create tables for blockchain event checkpoint tracking and idempotency

-- Checkpoint table for tracking last processed block
-- Used to resume event listening from the correct block after Hub restart
CREATE TABLE event_checkpoint (
    id VARCHAR(50) PRIMARY KEY,  -- e.g., 'rental_events'
    block_number BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Insert initial checkpoint (will be updated on first event)
INSERT INTO event_checkpoint (id, block_number)
VALUES ('rental_events', 0);

-- Processed events table for idempotency
-- Ensures the same blockchain event is not processed twice
CREATE TABLE processed_events (
    id SERIAL PRIMARY KEY,
    tx_hash VARCHAR(66) NOT NULL UNIQUE,  -- 0x + 64 hex chars
    block_number BIGINT NOT NULL,
    log_index INT NOT NULL,
    event_type VARCHAR(50) NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for efficient duplicate checking
CREATE INDEX idx_processed_events_tx_hash ON processed_events(tx_hash);

-- Dead letter queue for failed events
-- Failed events are saved here for later inspection and retry
CREATE TABLE failed_events (
    id SERIAL PRIMARY KEY,
    tx_hash VARCHAR(66) NOT NULL,
    block_number BIGINT NOT NULL,
    log_index INT NOT NULL,
    event_type VARCHAR(50) NOT NULL,
    raw_data BYTEA NOT NULL,
    error_message TEXT NOT NULL,
    retry_count INT NOT NULL DEFAULT 0,
    failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_retry_at TIMESTAMPTZ
);

-- Index for finding failed events by transaction hash
CREATE INDEX idx_failed_events_tx_hash ON failed_events(tx_hash);
