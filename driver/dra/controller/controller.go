package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/russellb/canhazgpu/driver/dra/api"
)

const FinalizerName = "canhazgpu.com/finalizer"

type ResourceClaimController struct {
	client.Client
	Scheme            *runtime.Scheme
	DriverName        string
	NodeAgentEndpoint string
}

func (r *ResourceClaimController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the ResourceClaim
	var claim resourceapi.ResourceClaim
	if err := r.Get(ctx, req.NamespacedName, &claim); err != nil {
		if errors.IsNotFound(err) {
			// ResourceClaim was deleted, handle cleanup
			logger.Info("ResourceClaim deleted, performing cleanup", "claimUID", req.Name)
			if err := r.handleResourceClaimDeletion(ctx, req.Name); err != nil {
				logger.Error(err, "failed to cleanup deleted ResourceClaim", "claimUID", req.Name)
				return ctrl.Result{RequeueAfter: time.Minute}, err
			}
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch ResourceClaim")
		return ctrl.Result{}, err
	}

	// Only handle claims that reference our ResourceClass
	// For now, we'll handle all claims in this simplified Phase 1 implementation
	// TODO: Add proper ResourceClass filtering in Phase 2

	// Check if claim is being deleted
	if !claim.DeletionTimestamp.IsZero() {
		// ResourceClaim is being deleted, handle deallocation if our finalizer is present
		if controllerutil.ContainsFinalizer(&claim, FinalizerName) {
			logger.Info("ResourceClaim being deleted, performing deallocation", "claim", claim.Name, "claimUID", string(claim.UID))
			if err := r.handleResourceClaimDeletion(ctx, string(claim.UID)); err != nil {
				logger.Error(err, "failed to deallocate resources during deletion", "claim", claim.Name)
				return ctrl.Result{RequeueAfter: time.Minute}, err
			}

			// Remove our finalizer to allow deletion to proceed
			controllerutil.RemoveFinalizer(&claim, FinalizerName)
			if err := r.Update(ctx, &claim); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add our finalizer if not present
	if !controllerutil.ContainsFinalizer(&claim, FinalizerName) {
		controllerutil.AddFinalizer(&claim, FinalizerName)
		if err := r.Update(ctx, &claim); err != nil {
			return ctrl.Result{}, err
		}
		// Requeue to process the updated resource
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if claim is already allocated
	if claim.Status.Allocation != nil {
		// Already allocated, nothing to do
		return ctrl.Result{}, nil
	}

	// Handle allocation
	if err := r.allocateResources(ctx, &claim); err != nil {
		logger.Error(err, "failed to allocate resources")
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}

	return ctrl.Result{}, nil
}

func (r *ResourceClaimController) allocateResources(ctx context.Context, claim *resourceapi.ResourceClaim) error {
	logger := log.FromContext(ctx)

	// Parse claim parameters
	params, err := r.parseClaimParameters(ctx, claim)
	if err != nil {
		return fmt.Errorf("failed to parse claim parameters: %w", err)
	}

	// For Phase 1, we'll use a simple strategy: allocate on any ready node
	node, err := r.selectNode(ctx)
	if err != nil {
		return fmt.Errorf("failed to select node: %w", err)
	}

	// Request allocation from node agent
	allocReq := &api.AllocationRequest{
		ClaimUID:   string(claim.UID),
		GPUCount:   params.GPUCount,
		GPUIDs:     params.GPUIDs,
		Namespace:  claim.Namespace,
	}

	allocResp, err := r.requestAllocationFromNode(ctx, node.Name, allocReq)
	if err != nil {
		return fmt.Errorf("failed to request allocation from node %s: %w", node.Name, err)
	}

	// Create allocation result with CDI device references
	deviceResults := make([]resourceapi.DeviceRequestAllocationResult, len(allocResp.AllocatedGPUs))
	for i, gpuID := range allocResp.AllocatedGPUs {
		deviceResults[i] = resourceapi.DeviceRequestAllocationResult{
			Request: "gpu-request",
			Driver:  "canhazgpu.com",
			Pool:    "node",
			Device:  fmt.Sprintf("gpu%d", gpuID),
		}
	}

	allocationResult := &resourceapi.AllocationResult{
		NodeSelector: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Key:      "kubernetes.io/hostname",
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{node.Name},
						},
					},
				},
			},
		},
		Devices: resourceapi.DeviceAllocationResult{
			Results: deviceResults,
		},
	}

	// Update claim status
	claim.Status.Allocation = allocationResult

	if err := r.Status().Update(ctx, claim); err != nil {
		// If update fails, we should deallocate on the node
		deallocReq := &api.DeallocationRequest{
			ClaimUID: string(claim.UID),
		}
		r.requestDeallocationFromNode(ctx, node.Name, deallocReq)
		return fmt.Errorf("failed to update claim status: %w", err)
	}

	logger.Info("successfully allocated resources",
		"claim", claim.Name,
		"node", node.Name,
		"gpus", allocResp.AllocatedGPUs)

	return nil
}

func (r *ResourceClaimController) parseClaimParameters(ctx context.Context, claim *resourceapi.ResourceClaim) (*api.ClaimParameters, error) {
	params := &api.ClaimParameters{
		GPUCount: 1, // Default
	}

	// For Phase 1, extract GPU count from device requests
	if len(claim.Spec.Devices.Requests) > 0 {
		params.GPUCount = int(claim.Spec.Devices.Requests[0].Count)
	}

	// TODO: Add support for specific GPU IDs and node preferences in Phase 2
	return params, nil
}

func (r *ResourceClaimController) selectNode(ctx context.Context) (*corev1.Node, error) {
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	// For Phase 1, select the first ready node
	for _, node := range nodes.Items {
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				return &node, nil
			}
		}
	}

	return nil, fmt.Errorf("no ready nodes found")
}

func (r *ResourceClaimController) requestAllocationFromNode(ctx context.Context, nodeName string, req *api.AllocationRequest) (*api.AllocationResponse, error) {
	// Communicate with node agent via HTTP
	nodeAgentURL := fmt.Sprintf("http://%s:8082/allocate", nodeName)

	// Convert request to JSON
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal allocation request: %w", err)
	}

	// Make HTTP request to node agent
	httpReq, err := http.NewRequestWithContext(ctx, "POST", nodeAgentURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to make HTTP request to node agent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("node agent returned error status: %d", resp.StatusCode)
	}

	// Parse response
	var allocResp api.AllocationResponse
	if err := json.NewDecoder(resp.Body).Decode(&allocResp); err != nil {
		return nil, fmt.Errorf("failed to decode allocation response: %w", err)
	}

	if !allocResp.Success {
		return nil, fmt.Errorf("node agent allocation failed: %s", allocResp.Error)
	}

	return &allocResp, nil
}

func (r *ResourceClaimController) requestDeallocationFromNode(ctx context.Context, nodeName string, req *api.DeallocationRequest) error {
	// Communicate with node agent via HTTP
	nodeAgentURL := fmt.Sprintf("http://%s:8082/deallocate", nodeName)

	// Convert request to JSON
	reqBody, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal deallocation request: %w", err)
	}

	// Make HTTP request to node agent
	httpReq, err := http.NewRequestWithContext(ctx, "POST", nodeAgentURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to make HTTP request to node agent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("node agent returned error status: %d", resp.StatusCode)
	}

	return nil
}

func (r *ResourceClaimController) handleResourceClaimDeletion(ctx context.Context, claimUID string) error {
	logger := log.FromContext(ctx)

	// Get all nodes to attempt deallocation from each one
	// Since we don't track which node has the allocation, we'll try all nodes
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	deallocReq := &api.DeallocationRequest{
		ClaimUID: claimUID,
	}

	// Try deallocation on all ready nodes
	for _, node := range nodes.Items {
		// Check if node is ready
		ready := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				ready = true
				break
			}
		}

		if !ready {
			continue
		}

		// Attempt deallocation - ignore errors since the claim might not be allocated on this node
		if err := r.requestDeallocationFromNode(ctx, node.Name, deallocReq); err != nil {
			logger.V(1).Info("deallocation attempt failed", "node", node.Name, "error", err.Error())
		} else {
			logger.Info("successfully deallocated resources", "claimUID", claimUID, "node", node.Name)
		}
	}

	return nil
}

func (r *ResourceClaimController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resourceapi.ResourceClaim{}).
		Complete(r)
}