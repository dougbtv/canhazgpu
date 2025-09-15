package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1alpha3"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/russellb/canhazgpu/driver/dra/api"
)

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
			// ResourceClaim was deleted, handle cleanup if needed
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch ResourceClaim")
		return ctrl.Result{}, err
	}

	// Only handle claims for our driver
	if claim.Spec.ResourceClassName != "canhazgpu.class" {
		return ctrl.Result{}, nil
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

	// Create allocation result
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
			Results: make([]resourceapi.DeviceRequestAllocationResult, len(allocResp.AllocatedGPUs)),
		},
	}

	// Add CDI device references for each allocated GPU
	for i, gpuID := range allocResp.AllocatedGPUs {
		allocationResult.Devices.Results[i] = resourceapi.DeviceRequestAllocationResult{
			Request: fmt.Sprintf("gpu-request-%d", i),
			Driver:  r.DriverName,
			Pool:    fmt.Sprintf("node-%s", node.Name),
			Device:  fmt.Sprintf("canhazgpu.com/gpu=%d", gpuID),
		}
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
	// For simplicity, we'll communicate directly with node agent via HTTP
	// In a real implementation, this would use gRPC or a more robust mechanism

	// TODO: Implement HTTP client call to node agent
	// For now, return a mock response
	return &api.AllocationResponse{
		Success:       true,
		AllocatedGPUs: []int{0}, // Mock allocation
		NodeName:      nodeName,
	}, nil
}

func (r *ResourceClaimController) requestDeallocationFromNode(ctx context.Context, nodeName string, req *api.DeallocationRequest) error {
	// TODO: Implement HTTP client call to node agent for deallocation
	return nil
}

func (r *ResourceClaimController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resourceapi.ResourceClaim{}).
		Complete(r)
}