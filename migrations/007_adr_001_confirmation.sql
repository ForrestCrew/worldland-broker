-- 007_adr_001_confirmation.sql
-- Phase 14: ADR-001 confirmation flow support
-- Adds soft delete and tx_hash uniqueness for idempotent confirmation

-- Add soft delete column for TTL cleanup
-- When set, session is considered deleted (filtered out of normal queries)
ALTER TABLE rental_sessions ADD COLUMN deleted_at TIMESTAMPTZ;

-- Create partial unique index on tx_hash
-- Enforces uniqueness only for non-deleted rows (allows re-use of tx_hash after soft delete)
-- tx_hash column already exists from migration 003
CREATE UNIQUE INDEX idx_rental_sessions_tx_hash_unique
ON rental_sessions(tx_hash)
WHERE tx_hash IS NOT NULL AND deleted_at IS NULL;

-- Create index for TTL cleanup queries
-- Optimizes: SELECT * FROM rental_sessions WHERE state = 'PENDING' AND created_at < $1 AND deleted_at IS NULL
CREATE INDEX idx_rental_sessions_ttl_cleanup
ON rental_sessions(state, created_at, deleted_at)
WHERE deleted_at IS NULL;
