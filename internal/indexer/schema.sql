-- Indexer Database Schema
-- Tables for storing indexed blockchain events (deposits, withdrawals, rentals)
-- Following Hub's checkpoint table pattern

-- =============================================================================
-- Deposit/Withdraw History Table
-- Stores Deposited and Withdrawn events from WorldlandRental contract
-- =============================================================================
CREATE TABLE IF NOT EXISTS deposit_withdraw_history (
    id BIGSERIAL PRIMARY KEY,
    user_address VARCHAR(42) NOT NULL,           -- Lowercase, no checksum (e.g., 0x...)
    event_type VARCHAR(10) NOT NULL,             -- 'deposit' or 'withdraw'
    amount NUMERIC(78, 0) NOT NULL,              -- Wei amount (stores full uint256 range)
    tx_hash VARCHAR(66) NOT NULL,                -- Transaction hash (e.g., 0x...)
    block_number BIGINT NOT NULL,                -- Block number where event occurred
    block_timestamp TIMESTAMPTZ NOT NULL,        -- Block timestamp for time-based queries
    log_index INT NOT NULL,                      -- Log index within transaction

    -- Idempotency: prevent duplicate inserts from reorgs/retries
    UNIQUE(tx_hash, log_index),

    -- Constraint: event_type must be valid
    CONSTRAINT chk_event_type CHECK (event_type IN ('deposit', 'withdraw'))
);

-- Composite index for address-based queries with cursor pagination
-- Supports: SELECT ... WHERE user_address = $1 ORDER BY block_timestamp DESC, id DESC
CREATE INDEX IF NOT EXISTS idx_deposit_withdraw_user_time
    ON deposit_withdraw_history(user_address, block_timestamp DESC, id DESC);

-- Index for block number queries (useful for backfill/reorg detection)
CREATE INDEX IF NOT EXISTS idx_deposit_withdraw_block
    ON deposit_withdraw_history(block_number);

-- =============================================================================
-- Rental History Table
-- Stores RentalStarted and RentalStopped events (upsert pattern)
-- RentalStarted creates row, RentalStopped updates with end time and cost
-- =============================================================================
CREATE TABLE IF NOT EXISTS rental_history (
    rental_id BIGINT PRIMARY KEY,                -- Rental ID from contract (unique per rental)
    user_address VARCHAR(42) NOT NULL,           -- Lowercase user address
    provider_address VARCHAR(42) NOT NULL,       -- Lowercase provider address
    start_time TIMESTAMPTZ NOT NULL,             -- When rental started
    end_time TIMESTAMPTZ,                        -- When rental ended (NULL if active)
    duration_seconds BIGINT,                     -- Computed duration (NULL if active)
    cost_wei NUMERIC(78, 0),                     -- Final cost in wei (NULL if active)
    start_tx_hash VARCHAR(66) NOT NULL,          -- Transaction that started rental
    stop_tx_hash VARCHAR(66),                    -- Transaction that stopped rental (NULL if active)
    start_block_number BIGINT NOT NULL,          -- Block where rental started
    stop_block_number BIGINT                     -- Block where rental stopped (NULL if active)
);

-- Index for user address queries, ordered by start time
CREATE INDEX IF NOT EXISTS idx_rental_user_start
    ON rental_history(user_address, start_time DESC);

-- Index for provider address queries, ordered by start time
CREATE INDEX IF NOT EXISTS idx_rental_provider_start
    ON rental_history(provider_address, start_time DESC);

-- Partial index for active rentals (end_time IS NULL)
-- Enables fast lookup: "Find all active rentals for user"
CREATE INDEX IF NOT EXISTS idx_rental_active
    ON rental_history(user_address) WHERE end_time IS NULL;

-- Index for block number queries (useful for backfill/reorg detection)
CREATE INDEX IF NOT EXISTS idx_rental_start_block
    ON rental_history(start_block_number);

-- =============================================================================
-- Indexer Checkpoint Table
-- Tracks last processed block for indexer resumption
-- Follows Hub's event_checkpoint pattern
-- =============================================================================
CREATE TABLE IF NOT EXISTS indexer_checkpoint (
    id VARCHAR(50) PRIMARY KEY,                  -- Checkpoint identifier (e.g., 'indexer_events')
    block_number BIGINT NOT NULL DEFAULT 0,      -- Last successfully processed block
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW() -- When checkpoint was last updated
);

-- Insert initial checkpoint row for indexer
INSERT INTO indexer_checkpoint (id, block_number, updated_at)
VALUES ('indexer_events', 0, NOW())
ON CONFLICT (id) DO NOTHING;
