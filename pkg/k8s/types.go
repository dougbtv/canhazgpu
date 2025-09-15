package k8s

import (
	corev1 "k8s.io/api/core/v1"
)

// ReservationRequest represents a request to reserve GPUs
type ReservationRequest struct {
	Name       string
	GPUCount   int
	GPUIDs     []string
	PreferNode string
}

// AllocationResult represents the result of a GPU allocation
type AllocationResult struct {
	NodeName      string
	AllocatedGPUs []int
}

// PodRequest represents a request to create a Pod with GPU resources
type PodRequest struct {
	Name      string
	Image     string
	Command   []string
	ClaimName string
}

// ClaimStatus represents the status of a ResourceClaim
type ClaimStatus struct {
	Name          string
	State         string
	Allocated     bool
	NodeName      string
	AllocatedGPUs []int
	PodName       string
	PodPhase      corev1.PodPhase
	Error         string
}

// ResourceClassParameters defines the structure for DRA resource class parameters
type ResourceClassParameters struct {
	AllowSpecificGPUs bool `json:"allowSpecificGPUs,omitempty"`
	DefaultGPUCount   int  `json:"defaultGPUCount,omitempty"`
}