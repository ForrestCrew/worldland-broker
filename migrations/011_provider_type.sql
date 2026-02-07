-- Phase 3: Provider Type Branching
-- Adds provider_type column to support Docker (individual) and K8s (data center) providers

ALTER TABLE providers ADD COLUMN IF NOT EXISTS provider_type VARCHAR(10) NOT NULL DEFAULT 'docker';
ALTER TABLE providers ADD COLUMN IF NOT EXISTS kubeconfig_data TEXT;
ALTER TABLE providers ADD COLUMN IF NOT EXISTS cluster_host VARCHAR(255);

-- Index for efficient type-based queries
CREATE INDEX IF NOT EXISTS idx_providers_provider_type ON providers(provider_type);
