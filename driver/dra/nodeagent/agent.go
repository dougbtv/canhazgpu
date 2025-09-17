package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"k8s.io/klog/v2"

	"github.com/russellb/canhazgpu/driver/dra/api"
	"github.com/russellb/canhazgpu/pkg/redisstate"
)

type NodeAgent struct {
	NodeName    string
	RedisClient *redisstate.Client
	GPUCount    int
}

func (na *NodeAgent) setupRoutes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/status", na.handleStatus)
	mux.HandleFunc("/allocate", na.handleAllocate)
	mux.HandleFunc("/deallocate", na.handleDeallocate)
	mux.HandleFunc("/health", na.handleHealth)

	return mux
}

func (na *NodeAgent) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	availableGPUs, err := na.RedisClient.GetAvailableGPUs(ctx)
	if err != nil {
		klog.Errorf("Failed to get available GPUs: %v", err)
		http.Error(w, fmt.Sprintf("Failed to get GPU status: %v", err), http.StatusInternalServerError)
		return
	}

	// Get allocated GPUs info
	var allocatedGPUs []api.GPUInfo
	for i := 0; i < na.GPUCount; i++ {
		state, err := na.RedisClient.GetGPUState(ctx, i)
		if err != nil {
			continue
		}

		if state.User != "" {
			gpuInfo := api.GPUInfo{
				ID: i,
			}

			if state.Type == "k8s" {
				// Extract claim UID from user field (format: "k8s:claimUID")
				claimUID := state.User[4:] // Remove "k8s:" prefix
				gpuInfo.ClaimUID = claimUID
				// TODO: Get pod name and namespace from Redis if stored
			} else {
				// Manual or other reservation - show as allocated but without k8s details
				gpuInfo.ClaimUID = fmt.Sprintf("manual:%s", state.User)
			}

			allocatedGPUs = append(allocatedGPUs, gpuInfo)
		}
	}

	response := &api.NodeStatusResponse{
		NodeName:      na.NodeName,
		TotalGPUs:     na.GPUCount,
		AvailableGPUs: availableGPUs,
		AllocatedGPUs: allocatedGPUs,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (na *NodeAgent) handleAllocate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req api.AllocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Get available GPUs
	availableGPUs, err := na.RedisClient.GetAvailableGPUs(ctx)
	if err != nil {
		klog.Errorf("Failed to get available GPUs: %v", err)
		response := &api.AllocationResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to get available GPUs: %v", err),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	// Select GPUs to allocate
	var selectedGPUs []int
	if len(req.GPUIDs) > 0 {
		// Specific GPU IDs requested
		for _, gpuIDStr := range req.GPUIDs {
			gpuID, err := strconv.Atoi(gpuIDStr)
			if err != nil {
				response := &api.AllocationResponse{
					Success: false,
					Error:   fmt.Sprintf("Invalid GPU ID: %s", gpuIDStr),
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(response)
				return
			}

			// Check if GPU is available
			found := false
			for _, availableGPU := range availableGPUs {
				if availableGPU == gpuID {
					found = true
					break
				}
			}

			if !found {
				response := &api.AllocationResponse{
					Success: false,
					Error:   fmt.Sprintf("GPU %d is not available", gpuID),
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(response)
				return
			}

			selectedGPUs = append(selectedGPUs, gpuID)
		}
	} else {
		// Allocate any available GPUs
		if len(availableGPUs) < req.GPUCount {
			response := &api.AllocationResponse{
				Success: false,
				Error:   fmt.Sprintf("Not enough available GPUs: requested %d, available %d", req.GPUCount, len(availableGPUs)),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		selectedGPUs = availableGPUs[:req.GPUCount]
	}

	// Reserve the selected GPUs
	if err := na.RedisClient.ReserveGPUsForClaim(ctx, selectedGPUs, req.ClaimUID, req.PodName, req.Namespace); err != nil {
		klog.Errorf("Failed to reserve GPUs: %v", err)
		response := &api.AllocationResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to reserve GPUs: %v", err),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	klog.Infof("Successfully allocated GPUs %v for claim %s", selectedGPUs, req.ClaimUID)

	response := &api.AllocationResponse{
		Success:       true,
		AllocatedGPUs: selectedGPUs,
		NodeName:      na.NodeName,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (na *NodeAgent) handleDeallocate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req api.DeallocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	if err := na.RedisClient.ReleaseGPUsForClaim(ctx, req.ClaimUID); err != nil {
		klog.Errorf("Failed to release GPUs for claim %s: %v", req.ClaimUID, err)
		response := &api.DeallocationResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to release GPUs: %v", err),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	klog.Infof("Successfully released GPUs for claim %s", req.ClaimUID)

	response := &api.DeallocationResponse{
		Success: true,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (na *NodeAgent) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Simple health check - verify Redis connection
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := na.RedisClient.Ping(ctx); err != nil {
		klog.Errorf("Health check failed - Redis connection error: %v", err)
		http.Error(w, "Redis connection failed", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (na *NodeAgent) startHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Update heartbeat for all claims managed by this agent
			// This is a simplified implementation - in practice we'd track active claims
			klog.V(4).Info("Heartbeat tick - skipping for Phase 1")
		}
	}
}