-- 005_add_node_api_endpoint.sql
-- Phase 4: Add API endpoint field for Hub-to-Node communication

ALTER TABLE nodes ADD COLUMN IF NOT EXISTS api_endpoint VARCHAR(255);

-- Index for endpoint lookup
CREATE INDEX IF NOT EXISTS idx_nodes_api_endpoint ON nodes(api_endpoint);

-- Comment for documentation
COMMENT ON COLUMN nodes.api_endpoint IS 'mTLS HTTPS endpoint URL for Hub-to-Node communication (e.g., https://node.example.com:8443)';
