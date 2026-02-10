-- Migration 009: Add base images catalog and session docker image tracking
-- Phase 24-01: Base Image Selection
-- Creates base_images table for preset GPU container images and adds docker_image column to rental_sessions

-- Create base_images table for preset GPU container images
CREATE TABLE base_images (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL UNIQUE,          -- Human-readable name like "PyTorch 2.6 CUDA 12.6"
    docker_image VARCHAR(500) NOT NULL,          -- Full image reference (e.g., pytorch/pytorch:2.6.0-cuda12.6-cudnn9-devel)
    category VARCHAR(50) NOT NULL,               -- One of: 'pytorch', 'tensorflow', 'cuda'
    gpu_required BOOLEAN DEFAULT true,           -- Whether this image requires GPU
    description TEXT,                            -- Optional description of the image
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Create index on category for efficient filtering
CREATE INDEX idx_base_images_category ON base_images(category);

-- Add docker_image column to rental_sessions
-- Tracks which container image the user selected for their GPU workload
ALTER TABLE rental_sessions
ADD COLUMN docker_image VARCHAR(500) NOT NULL DEFAULT 'nvidia/cuda:12.0.0-devel-ubuntu22.04';

-- Comments for documentation
COMMENT ON TABLE base_images IS 'Catalog of preset GPU container images (PyTorch, TensorFlow, CUDA) that users can select during rental creation';
COMMENT ON COLUMN base_images.name IS 'Human-readable name displayed to users (e.g., PyTorch 2.1 CUDA 12.1)';
COMMENT ON COLUMN base_images.docker_image IS 'Full Docker image reference including registry, repository, and tag';
COMMENT ON COLUMN base_images.category IS 'Image category: pytorch, tensorflow, or cuda';
COMMENT ON COLUMN base_images.gpu_required IS 'Whether this image requires GPU hardware to run';
COMMENT ON COLUMN rental_sessions.docker_image IS 'Container image selected by user for GPU workload (preset or custom)';

-- Insert preset base images (matching proxy defaults)
-- Default CUDA: nvidia/cuda:12.0.0-devel-ubuntu22.04 (proxy default)
INSERT INTO base_images (name, docker_image, category, gpu_required, description) VALUES
    ('CUDA 12.0 Development', 'nvidia/cuda:12.0.0-devel-ubuntu22.04', 'cuda', true, 'NVIDIA CUDA 12.0 development environment on Ubuntu 22.04. Default base image for GPU workloads.'),
    ('PyTorch 2.1 CUDA 12.1', 'pytorch/pytorch:2.1.0-cuda12.1-cudnn8-devel', 'pytorch', true, 'PyTorch 2.1.0 with CUDA 12.1 and cuDNN 8. Stable ML framework with conda pre-installed.'),
    ('PyTorch 2.3 CUDA 12.1', 'pytorch/pytorch:2.3.0-cuda12.1-cudnn8-devel', 'pytorch', true, 'PyTorch 2.3.0 with CUDA 12.1 and cuDNN 8. Latest stable with conda and development tools.'),
    ('TensorFlow 2.15 GPU', 'tensorflow/tensorflow:2.15.0-gpu', 'tensorflow', true, 'TensorFlow 2.15.0 with GPU support. Stable version compatible with CUDA 12.x.');
