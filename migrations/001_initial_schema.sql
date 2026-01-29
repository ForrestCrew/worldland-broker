-- 001_initial_schema.sql
-- Phase 2: Provider Infrastructure tables

-- Providers table (PROV-01: wallet-based onboarding)
CREATE TABLE IF NOT EXISTS providers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_address VARCHAR(42) NOT NULL UNIQUE,  -- Ethereum address (0x + 40 hex chars)
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_providers_wallet ON providers(wallet_address);

-- Nodes table (PROV-02: GPU node registration)
CREATE TABLE IF NOT EXISTS nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id UUID NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    gpu_uuid VARCHAR(64) NOT NULL,
    gpu_type VARCHAR(100) NOT NULL,
    memory_gb INTEGER NOT NULL,
    price_per_second NUMERIC(18, 8) NOT NULL,  -- Wei precision for pricing
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'offline')),
    certificate_expiry TIMESTAMPTZ,  -- mTLS cert expiry for auto-renewal tracking
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(provider_id, gpu_uuid)  -- One registration per GPU per provider
);

CREATE INDEX idx_nodes_provider ON nodes(provider_id);
CREATE INDEX idx_nodes_status ON nodes(status);

-- Sessions table (PROV-01: session token storage)
CREATE TABLE IF NOT EXISTS sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id UUID NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    token VARCHAR(64) NOT NULL UNIQUE,  -- Session token (cryptographically random)
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sessions_token ON sessions(token);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

-- Nonces table (PROV-01: SIWE replay attack prevention)
CREATE TABLE IF NOT EXISTS nonces (
    nonce VARCHAR(32) PRIMARY KEY,  -- SIWE nonce (alphanumeric)
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_nonces_expires ON nonces(expires_at);

-- Updated_at trigger function
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Apply updated_at trigger to relevant tables
CREATE TRIGGER update_providers_updated_at
    BEFORE UPDATE ON providers
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_nodes_updated_at
    BEFORE UPDATE ON nodes
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
