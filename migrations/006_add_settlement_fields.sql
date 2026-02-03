-- Migration 006: Add settlement tracking fields to rental_sessions table
-- Phase 04-07: Batch Settlement Processor

-- Add settled_at timestamp to track when settlement was processed
ALTER TABLE rental_sessions
ADD COLUMN settled_at TIMESTAMPTZ;

-- Add settled_amount to store calculated cost in Wei
ALTER TABLE rental_sessions
ADD COLUMN settled_amount TEXT DEFAULT '';

-- Create index on settled_at for efficient pending settlement queries
-- Used by batch processor to find STOPPED sessions without settlement
CREATE INDEX idx_rental_sessions_settled_at ON rental_sessions(state, settled_at)
WHERE state = 'STOPPED' AND settled_at IS NULL;

-- Comments for documentation
COMMENT ON COLUMN rental_sessions.settled_at IS 'Timestamp when settlement was processed by batch processor (04-07)';
COMMENT ON COLUMN rental_sessions.settled_amount IS 'Calculated rental cost in Wei (decimal string) - blockchain transfer deferred to Phase 5';
