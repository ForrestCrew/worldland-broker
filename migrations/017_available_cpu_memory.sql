-- Migration 017: Track available CPU cores and memory (like available_gpus)
-- Used for multi-tenant node sharing: show remaining resources after other sessions

ALTER TABLE nodes ADD COLUMN IF NOT EXISTS available_cpu_cores INTEGER DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS available_memory_gb INTEGER DEFAULT 0;

-- Initialize available = total for existing nodes
UPDATE nodes SET available_cpu_cores = total_cpu_cores WHERE available_cpu_cores = 0 AND total_cpu_cores > 0;
UPDATE nodes SET available_memory_gb = total_memory_gb WHERE available_memory_gb = 0 AND total_memory_gb > 0;
