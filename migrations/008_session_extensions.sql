-- Migration 008: Add session extension tracking
-- Phase 16-01: Session Extension Backend
-- Adds extension fields to rental_sessions and creates audit table for extension history

-- Add extension tracking fields to rental_sessions
ALTER TABLE rental_sessions
ADD COLUMN extended_until TIMESTAMPTZ,
ADD COLUMN extension_count INTEGER DEFAULT 0,
ADD COLUMN total_extended_minutes INTEGER DEFAULT 0;

-- Create partial index for expiration worker queries
-- Finds RUNNING sessions with extended_until approaching expiration
CREATE INDEX idx_rental_sessions_expiration ON rental_sessions(extended_until)
WHERE state = 'RUNNING' AND extended_until IS NOT NULL;

-- Create audit table for extension history
-- Tracks each extension request for debugging and fraud detection
CREATE TABLE session_extensions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES rental_sessions(id) ON DELETE CASCADE,
    extended_by_minutes INTEGER NOT NULL,
    extended_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    cost_estimate TEXT NOT NULL,
    idempotency_key VARCHAR(255),

    CONSTRAINT fk_session FOREIGN KEY (session_id)
        REFERENCES rental_sessions(id) ON DELETE CASCADE
);

-- Create indexes on session_extensions
CREATE INDEX idx_session_extensions_session ON session_extensions(session_id, extended_at DESC);
CREATE UNIQUE INDEX idx_session_extensions_idempotency ON session_extensions(idempotency_key)
WHERE idempotency_key IS NOT NULL;

-- Comments for documentation
COMMENT ON COLUMN rental_sessions.extended_until IS 'Hub-managed expiration time (null until first extension, then tracks cumulative extensions)';
COMMENT ON COLUMN rental_sessions.extension_count IS 'Number of times session has been extended';
COMMENT ON COLUMN rental_sessions.total_extended_minutes IS 'Total minutes added via all extensions';
COMMENT ON TABLE session_extensions IS 'Audit log of session extension requests with idempotency tracking';
