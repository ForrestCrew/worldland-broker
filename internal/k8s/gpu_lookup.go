package k8s

import "strings"

// GPUVRAMLookup maps known GPU model names to their VRAM in MB.
// Used as fallback when NVML or K8s labels don't provide VRAM info.
var GPUVRAMLookup = map[string]int{
	"Tesla T4":                  15360,
	"Tesla V100-SXM2":          32768,
	"Tesla V100-PCIE":          16384,
	"NVIDIA A100-SXM4-40GB":    40960,
	"NVIDIA A100-SXM4-80GB":    81920,
	"NVIDIA A100 80GB PCIe":    81920,
	"NVIDIA H100 80GB HBM3":    81920,
	"NVIDIA H100":              81920,
	"NVIDIA L40":               49152,
	"NVIDIA L4":                24576,
	"NVIDIA RTX 4090":          24576,
	"NVIDIA RTX 4080":          16384,
	"NVIDIA RTX 3090":          24576,
	"NVIDIA GeForce RTX 5090":  32768,
	"NVIDIA GeForce RTX 4090":  24576,
	"NVIDIA GeForce RTX 4080":  16384,
	"NVIDIA GeForce RTX 3090":  24576,
	"NVIDIA GeForce RTX 3080":  10240,
	"NVIDIA A10":               24576,
	"NVIDIA A40":               49152,
}

// LookupVRAM returns VRAM in MB for a GPU model name.
// Falls back to partial matching if exact match not found.
// Returns 0 if model is unknown.
func LookupVRAM(gpuModel string) int {
	if vram, ok := GPUVRAMLookup[gpuModel]; ok {
		return vram
	}
	// Partial matching (contains, case-insensitive)
	lower := strings.ToLower(gpuModel)
	for model, vram := range GPUVRAMLookup {
		if strings.Contains(lower, strings.ToLower(model)) {
			return vram
		}
	}
	return 0
}
