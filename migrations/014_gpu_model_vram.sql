-- Migration 014: Add GPU model, VRAM, and driver version columns to nodes table
-- Enables RunPod-style GPU marketplace with real model names and VRAM info

ALTER TABLE nodes ADD COLUMN IF NOT EXISTS gpu_model TEXT DEFAULT '';
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS vram_mb INTEGER DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS driver_version TEXT DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_nodes_gpu_model ON nodes(gpu_model);

-- Backfill known GPU nodes with Tesla T4 specs
UPDATE nodes SET gpu_model = 'Tesla T4', vram_mb = 15360
WHERE k8s_node_name IN ('worldland-gpu-node-1', 'demo-gpu-node') AND gpu_model = '';

UPDATE nodes SET gpu_model = 'Tesla T4', vram_mb = 15360
WHERE k8s_node_name = 'worldland-server' AND gpu_model = '';
