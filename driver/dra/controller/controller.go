package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func (r *ResourceClaimController) AutoReconcilePods(ctx context.Context) error {
	// Find allocated ResourceClaims that don't have associated Pods yet
	var claims resourceapi.ResourceClaimList
	if err := r.List(ctx, &claims); err != nil {
		return fmt.Errorf("failed to list ResourceClaims: %w", err)
	}

	var pods corev1.PodList
	if err := r.List(ctx, &pods); err != nil {
		return fmt.Errorf("failed to list Pods: %w", err)
	}

	// Create a map of ResourceClaim names that already have Pods
	claimsWithPods := make(map[string]bool)
	for _, pod := range pods.Items {
		for _, claimRef := range pod.Spec.ResourceClaims {
			if claimRef.ResourceClaimName != nil {
				claimsWithPods[*claimRef.ResourceClaimName] = true
			}
		}
	}

	// Check each allocated claim
	for _, claim := range claims.Items {
		// Skip if claim is not allocated
		if claim.Status.Allocation == nil {
			continue
		}

		// Skip if claim already has a Pod
		if claimsWithPods[claim.Name] {
			continue
		}

		// Check for vLLM workload annotation first
		workloadType, isVLLM := claim.Annotations["canhazgpu.dev/workload"]

		var pod *corev1.Pod
		var err error

		if isVLLM && workloadType == "vllm" {
			// Handle vLLM workload
			pod, err = r.createVLLMPod(ctx, &claim)
			if err != nil {
				ctrl.Log.WithName("auto-reconciler").Error(err, "failed to create vLLM Pod", "claim", claim.Name)
				continue
			}
		} else {
			// Check for traditional pod-spec annotation
			podSpecJSON, exists := claim.Annotations["k8shazgpu.com/pod-spec"]
			if !exists {
				continue
			}

			// Parse Pod spec from annotation
			var podSpec struct {
				Image   string   `json:"image"`
				Command []string `json:"command"`
			}
			if err := json.Unmarshal([]byte(podSpecJSON), &podSpec); err != nil {
				ctrl.Log.WithName("auto-reconciler").Error(err, "failed to parse pod spec", "claim", claim.Name)
				continue
			}

			// Create traditional Pod for this claim
			pod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      claim.Name + "-pod",
					Namespace: claim.Namespace,
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "gpu-workload",
							Image:   podSpec.Image,
							Command: podSpec.Command,
						},
					},
					ResourceClaims: []corev1.PodResourceClaim{
						{
							Name: "gpu-claim",
							ResourceClaimName: &claim.Name,
						},
					},
				},
			}

			// Add resource claim reference to container
			pod.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{
				{
					Name: "gpu-claim",
				},
			}
		}

		// Create Pod for this claim
		ctrl.Log.WithName("auto-reconciler").Info("creating Pod for allocated claim", "claim", claim.Name)

		if err := r.Create(ctx, pod); err != nil {
			ctrl.Log.WithName("auto-reconciler").Error(err, "failed to create Pod", "claim", claim.Name, "pod", pod.Name)
			continue
		}

		ctrl.Log.WithName("auto-reconciler").Info("created Pod for allocated claim", "claim", claim.Name, "pod", pod.Name)
	}

	return nil
}

func (r *ResourceClaimController) createVLLMPod(ctx context.Context, claim *resourceapi.ResourceClaim) (*corev1.Pod, error) {
	logger := log.FromContext(ctx)

	// Extract annotations
	imageName := claim.Annotations["canhazgpu.dev/image-name"]
	repoName := claim.Annotations["canhazgpu.dev/repo-name"]
	cmdStr := claim.Annotations["canhazgpu.dev/cmd"]
	portStr := claim.Annotations["canhazgpu.dev/port"]

	if imageName == "" || repoName == "" || cmdStr == "" {
		return nil, fmt.Errorf("missing required vLLM annotations: image-name=%s, repo-name=%s, cmd=%s",
			imageName, repoName, cmdStr)
	}

	// Parse port if specified
	var port int32
	if portStr != "" {
		if portInt, err := strconv.Atoi(portStr); err == nil && portInt > 0 {
			port = int32(portInt)
		}
	}

	// Get the CachePlan to look up image ref and repo path
	imageRef, gitPath, err := r.resolveCacheItems(ctx, imageName, repoName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve cache items: %w", err)
	}

	// Parse command and wrap it with vLLM setup
	var userCmd string
	if cmdStr == "" || cmdStr == "/bin/sh -c sleep 300" {
		userCmd = "sleep 300"
	} else if strings.HasPrefix(cmdStr, "/bin/sh -c ") {
		// Extract the command part after "/bin/sh -c "
		userCmd = strings.TrimPrefix(cmdStr, "/bin/sh -c ")
	} else {
		// Treat as direct command
		userCmd = cmdStr
	}

	// Create wrapper script that sets up vLLM workspace and runs user command
	// Properly escape the user command for shell execution
	escapedUserCmd := strings.ReplaceAll(userCmd, "'", "'\"'\"'")

	wrapperScript := fmt.Sprintf(`
# vLLM workspace setup (replicating CI pattern)
echo "Setting up vLLM workspace..."

# Copy in the code from the checkout to the workspace
rm -rf /vllm-workspace/vllm || true
cp -a /workdir/. /vllm-workspace/

# Overlay the pure-Python vllm into the install package dir
export SITEPKG="$(python3 -c 'import sysconfig; print(sysconfig.get_paths()["purelib"])')"
cp -a /vllm-workspace/vllm/* "$SITEPKG/vllm/"

# Restore src/ layout, as Dockerfile does. Hides code from tests, but allows setup.
rm -rf /vllm-workspace/src || true
mkdir -p /vllm-workspace/src
mv /vllm-workspace/vllm /vllm-workspace/src/vllm

echo "vLLM workspace setup complete. Running user command..."
cd /vllm-workspace

# Now exec the user command (properly escaped)
exec sh -c '%s'
`, escapedUserCmd)

	cmdArgs := []string{"/bin/sh", "-c", wrapperScript}

	// Set up container ports if specified
	var containerPorts []corev1.ContainerPort
	if port > 0 {
		containerPorts = []corev1.ContainerPort{
			{
				Name:          "vllm-api",
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		}
	}

	// Create Pod with vLLM-specific mounts
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claim.Name + "-vllm-pod",
			Namespace: claim.Namespace,
			Labels: map[string]string{
				"app":      "k8shazgpu",
				"workload": "vllm",
				"claim":    claim.Name,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "vllm-workload",
					Image:   imageRef,
					Command: cmdArgs,
					SecurityContext: &corev1.SecurityContext{
						Privileged: &[]bool{true}[0], // TODO: REMOVE FOR PRODUCTION - dev/inspection only
					},
					Env: []corev1.EnvVar{
						{
							Name:  "WORKDIR",
							Value: "/workdir",
						},
						{
							Name:  "MODEL_DIR",
							Value: "/models",
						},
					},
					Ports: containerPorts,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "git-repo",
							MountPath: "/workdir",
							ReadOnly:  false,
						},
						{
							Name:      "model-cache",
							MountPath: "/models",
							ReadOnly:  true,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "git-repo",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: gitPath,
							Type: &[]corev1.HostPathType{corev1.HostPathDirectory}[0],
						},
					},
				},
				{
					Name: "model-cache",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/var/lib/canhazgpu-cache/hub_cache",
							Type: &[]corev1.HostPathType{corev1.HostPathDirectory}[0],
						},
					},
				},
			},
			ResourceClaims: []corev1.PodResourceClaim{
				{
					Name: "gpu-claim",
					ResourceClaimName: &claim.Name,
				},
			},
		},
	}

	// Add resource claim reference to container
	pod.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{
		{
			Name: "gpu-claim",
		},
	}

	logger.Info("creating vLLM Pod",
		"claim", claim.Name,
		"image", imageRef,
		"gitPath", gitPath,
		"command", cmdStr)

	return pod, nil
}

func (r *ResourceClaimController) resolveCacheItems(ctx context.Context, imageName, repoName string) (string, string, error) {
	// Get CachePlan to resolve image ref and repo path
	var cachePlan unstructured.Unstructured
	cachePlan.SetAPIVersion("canhazgpu.dev/v1alpha1")
	cachePlan.SetKind("CachePlan")

	err := r.Get(ctx, client.ObjectKey{Name: "default"}, &cachePlan)
	if err != nil {
		return "", "", fmt.Errorf("failed to get CachePlan: %w", err)
	}

	spec, found, err := unstructured.NestedMap(cachePlan.Object, "spec")
	if err != nil || !found {
		return "", "", fmt.Errorf("CachePlan has no spec")
	}

	items, found, err := unstructured.NestedSlice(spec, "items")
	if err != nil || !found {
		return "", "", fmt.Errorf("CachePlan has no items")
	}

	var imageRef, gitPath string

	// Find image and git repo items
	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		itemType, _ := itemMap["type"].(string)
		name, _ := itemMap["name"].(string)

		if itemType == "image" && name == imageName {
			if imageData, ok := itemMap["image"].(map[string]interface{}); ok {
				if ref, ok := imageData["ref"].(string); ok {
					imageRef = ref
				}
			}
		} else if itemType == "gitRepo" && name == repoName {
			if gitData, ok := itemMap["gitRepo"].(map[string]interface{}); ok {
				if pathName, ok := gitData["pathName"].(string); ok {
					gitPath = fmt.Sprintf("/var/lib/canhazgpu-cache/%s", pathName)
				}
			}
		}
	}

	if imageRef == "" {
		return "", "", fmt.Errorf("image %s not found in CachePlan", imageName)
	}
	if gitPath == "" {
		return "", "", fmt.Errorf("git repo %s not found in CachePlan", repoName)
	}

	return imageRef, gitPath, nil
}

func (r *ResourceClaimController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&resourceapi.ResourceClaim{}).
		Complete(r)
}