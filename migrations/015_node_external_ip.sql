-- Migration 015: Add external_ip column to nodes for SSH access
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS external_ip TEXT DEFAULT '';
