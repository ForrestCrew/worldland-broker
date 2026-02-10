-- 013: RunPod-style resource selection for sessions + node capacity tracking
-- Sessions: User-selected resource specs (GPU count, CPU, memory, storage)
-- Nodes: Capacity fields for matching and availability tracking

-- Session resource specs (user-selected at rental creation)
ALTER TABLE rental_sessions ADD COLUMN IF NOT EXISTS gpu_count INTEGER DEFAULT 1;
ALTER TABLE rental_sessions ADD COLUMN IF NOT EXISTS cpu_cores INTEGER DEFAULT 4;
ALTER TABLE rental_sessions ADD COLUMN IF NOT EXISTS memory_gb_req INTEGER DEFAULT 16;
ALTER TABLE rental_sessions ADD COLUMN IF NOT EXISTS storage_gb INTEGER DEFAULT 50;

-- Node capacity fields (populated from K8s cluster discovery)
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS total_gpus INTEGER DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS available_gpus INTEGER DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS total_cpu_cores INTEGER DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS total_memory_gb INTEGER DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS k8s_node_name TEXT;
