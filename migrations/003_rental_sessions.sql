-- 003_rental_sessions.sql
-- Phase 3: Rental session tracking tables

-- Create enum type for rental session state
-- 5 states: PENDING -> RUNNING -> STOPPED/FAILED/CANCELLED
CREATE TYPE rental_session_state AS ENUM (
    'PENDING',
    'RUNNING',
    'STOPPED',
    'FAILED',
    'CANCELLED'
);

-- Create rental_sessions table
-- Tracks GPU rental lifecycle from request to completion
CREATE TABLE rental_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_address VARCHAR(42) NOT NULL,       -- Renter's Ethereum address
    provider_address VARCHAR(42) NOT NULL,   -- Provider's Ethereum address
    node_id UUID NOT NULL REFERENCES nodes(id),
    rental_id BIGINT UNIQUE,                 -- NULL until blockchain confirms, unique when set
    state rental_session_state NOT NULL DEFAULT 'PENDING',
    price_per_second NUMERIC(78, 18) NOT NULL,  -- Wei precision (uint256 max = 78 digits)
    start_time TIMESTAMPTZ,                  -- Set when RUNNING
    end_time TIMESTAMPTZ,                    -- Set when terminal state
    tx_hash VARCHAR(66),                     -- 0x + 64 hex chars
    block_number BIGINT,                     -- Block where state confirmed
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes for common queries
CREATE INDEX idx_rental_sessions_user ON rental_sessions(user_address);
CREATE INDEX idx_rental_sessions_provider ON rental_sessions(provider_address);
CREATE INDEX idx_rental_sessions_node ON rental_sessions(node_id);
CREATE INDEX idx_rental_sessions_state ON rental_sessions(state);
-- Composite index for stale session queries (timeout enforcement)
CREATE INDEX idx_rental_sessions_state_created ON rental_sessions(state, created_at);

-- Apply updated_at trigger
CREATE TRIGGER update_rental_sessions_updated_at
    BEFORE UPDATE ON rental_sessions
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
