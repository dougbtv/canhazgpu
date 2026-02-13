package gpu

import (
	"context"

	"github.com/russellb/canhazgpu/internal/types"
)

// VirtualProvider implements the GPUProvider interface for virtual/simulated GPUs
// Used in Kubernetes environments with fake-gpu-operator or when GPU management
// is handled externally (e.g., k8shazgpu with DRA)
type VirtualProvider struct{}

// NewVirtualProvider creates a new virtual GPU provider
func NewVirtualProvider() *VirtualProvider {
	return &VirtualProvider{}
}

// Name returns the name of the provider
func (v *VirtualProvider) Name() string {
	return "virtual"
}

// IsAvailable always returns true since virtual provider has no system dependencies
func (v *VirtualProvider) IsAvailable() bool {
	return true
}

// DetectGPUUsage returns an empty map since virtual GPUs have no physical processes
// GPU allocation is managed entirely through Redis, not through physical GPU detection
func (v *VirtualProvider) DetectGPUUsage(ctx context.Context) (map[int]*types.GPUUsage, error) {
	return make(map[int]*types.GPUUsage), nil
}

// GetGPUCount returns 0 for virtual provider
// The actual GPU count is set via the admin --gpus flag and stored in Redis
func (v *VirtualProvider) GetGPUCount(ctx context.Context) (int, error) {
	return 0, nil
}
