-- Migration 010: Fix CUDA image default to match proxy
-- Proxy default: nvidia/cuda:12.0.0-devel-ubuntu22.04

-- Update the default value for rental_sessions.docker_image
ALTER TABLE rental_sessions
ALTER COLUMN docker_image SET DEFAULT 'nvidia/cuda:12.0.0-devel-ubuntu22.04';

-- Also update any existing sessions with old image tags
UPDATE rental_sessions
SET docker_image = 'nvidia/cuda:12.0.0-devel-ubuntu22.04'
WHERE docker_image IN ('nvidia/cuda:12.1-runtime-ubuntu22.04', 'nvidia/cuda:12.1.1-runtime-ubuntu22.04');
