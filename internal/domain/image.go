package domain

import "time"

// ImageCategory represents the type of base image
type ImageCategory string

const (
	ImageCategoryPyTorch    ImageCategory = "pytorch"
	ImageCategoryTensorFlow ImageCategory = "tensorflow"
	ImageCategoryCUDA       ImageCategory = "cuda"
)

// ValidImageCategories returns all valid ImageCategory values
func ValidImageCategories() []ImageCategory {
	return []ImageCategory{
		ImageCategoryPyTorch,
		ImageCategoryTensorFlow,
		ImageCategoryCUDA,
	}
}

// IsValidCategory checks if a category string is valid
func IsValidCategory(category string) bool {
	for _, c := range ValidImageCategories() {
		if string(c) == category {
			return true
		}
	}
	return false
}

// BaseImage represents a preset GPU container image
type BaseImage struct {
	ID          string        `json:"id"`                    // UUID
	Name        string        `json:"name"`                  // Human-readable name like "PyTorch 2.6 CUDA 12.6"
	DockerImage string        `json:"dockerImage"`           // Full image reference (e.g., pytorch/pytorch:2.6.0-cuda12.6-cudnn9-devel)
	Category    ImageCategory `json:"category"`              // pytorch, tensorflow, or cuda
	GPURequired bool          `json:"gpuRequired"`           // Whether this image requires GPU
	Description string        `json:"description,omitempty"` // Optional description
	CreatedAt   time.Time     `json:"createdAt"`
}

// DefaultImage is the fallback container image when no image is specified
const DefaultImage = "nvidia/cuda:12.1-runtime-ubuntu22.04"
