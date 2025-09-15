package api

// ClaimParameters represents parameters parsed from a ResourceClaim
type ClaimParameters struct {
	GPUCount   int      `json:"gpuCount"`
	GPUIDs     []string `json:"gpuIDs,omitempty"`
	PreferNode string   `json:"preferNode,omitempty"`
}

// AllocationRequest represents a request to allocate GPUs on a node
type AllocationRequest struct {
	ClaimUID   string   `json:"claimUID"`
	GPUCount   int      `json:"gpuCount"`
	GPUIDs     []string `json:"gpuIDs,omitempty"`
	Namespace  string   `json:"namespace"`
	PodName    string   `json:"podName,omitempty"`
}

// AllocationResponse represents the response from a node allocation request
type AllocationResponse struct {
	Success       bool   `json:"success"`
	AllocatedGPUs []int  `json:"allocatedGPUs"`
	NodeName      string `json:"nodeName"`
	Error         string `json:"error,omitempty"`
}

// DeallocationRequest represents a request to deallocate GPUs on a node
type DeallocationRequest struct {
	ClaimUID string `json:"claimUID"`
}

// DeallocationResponse represents the response from a node deallocation request
type DeallocationResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// NodeStatusRequest represents a request for node GPU status
type NodeStatusRequest struct{}

// NodeStatusResponse represents the response with node GPU status
type NodeStatusResponse struct {
	NodeName      string    `json:"nodeName"`
	TotalGPUs     int       `json:"totalGPUs"`
	AvailableGPUs []int     `json:"availableGPUs"`
	AllocatedGPUs []GPUInfo `json:"allocatedGPUs"`
}

// GPUInfo represents information about an allocated GPU
type GPUInfo struct {
	ID        int    `json:"id"`
	ClaimUID  string `json:"claimUID"`
	PodName   string `json:"podName,omitempty"`
	Namespace string `json:"namespace"`
}