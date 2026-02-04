-- Migration 010: Fix CUDA image tag
-- The original image tag nvidia/cuda:12.1-runtime-ubuntu22.04 does not exist
-- NVIDIA uses full version numbers like 12.1.1

-- Update the default value for rental_sessions.docker_image
ALTER TABLE rental_sessions
ALTER COLUMN docker_image SET DEFAULT 'nvidia/cuda:12.1.1-runtime-ubuntu22.04';

-- Also update any existing sessions that have the invalid image
UPDATE rental_sessions
SET docker_image = 'nvidia/cuda:12.1.1-runtime-ubuntu22.04'
WHERE docker_image = 'nvidia/cuda:12.1-runtime-ubuntu22.04';
