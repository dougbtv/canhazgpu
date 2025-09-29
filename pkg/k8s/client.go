package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	resourceclient "k8s.io/client-go/kubernetes/typed/resource/v1beta1"
)

const (
	DeviceClassName = "canhazgpu.class"
	DriverName      = "canhazgpu.com"
)

type Client struct {
	clientset      kubernetes.Interface
	resourceClient resourceclient.ResourceV1beta1Interface
	namespace      string
}

func NewClient(kubeContext, namespace string) (*Client, error) {
	config, err := getKubeConfig(kubeContext)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	resourceClient, err := resourceclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource client: %w", err)
	}

	return &Client{
		clientset:      clientset,
		resourceClient: resourceClient,
		namespace:      namespace,
	}, nil
}

func getKubeConfig(kubeContext string) (*rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}

	if kubeContext != "" {
		overrides.CurrentContext = kubeContext
	}

	config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	return config.ClientConfig()
}

func (c *Client) CreateResourceClaim(ctx context.Context, req *ReservationRequest) (*resourceapi.ResourceClaim, error) {
	return c.CreateResourceClaimWithPodSpec(ctx, req, nil)
}

func (c *Client) CreateResourceClaimWithPodSpec(ctx context.Context, req *ReservationRequest, podSpec *PodSpec) (*resourceapi.ResourceClaim, error) {
	// Create ResourceClaimSpec for v1beta1 API
	spec := resourceapi.ResourceClaimSpec{
		Devices: resourceapi.DeviceClaim{
			Requests: []resourceapi.DeviceRequest{
				{
					Name:            "gpu-request",
					DeviceClassName: DeviceClassName,
					AllocationMode:  resourceapi.DeviceAllocationModeExactCount,
					Count:           int64(req.GPUCount),
				},
			},
		},
	}

	// TODO: Add support for specific GPU IDs and node preferences in Phase 2

	annotations := make(map[string]string)
	if podSpec != nil {
		// Store Pod spec as JSON annotation for delayed Pod creation
		podSpecJSON, err := json.Marshal(podSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Pod spec: %w", err)
		}
		annotations["k8shazgpu.com/pod-spec"] = string(podSpecJSON)
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        req.Name,
			Namespace:   c.namespace,
			Annotations: annotations,
		},
		Spec: spec,
	}

	return c.resourceClient.ResourceClaims(c.namespace).Create(ctx, claim, metav1.CreateOptions{})
}

func (c *Client) CreateResourceClaimWithVLLMAnnotations(ctx context.Context, req *ReservationRequest, imageName, repoName string, cmdArgs []string, diffConfigMap string) (*resourceapi.ResourceClaim, error) {
	// Create ResourceClaimSpec for v1beta1 API
	spec := resourceapi.ResourceClaimSpec{
		Devices: resourceapi.DeviceClaim{
			Requests: []resourceapi.DeviceRequest{
				{
					Name:            "gpu-request",
					DeviceClassName: DeviceClassName,
					AllocationMode:  resourceapi.DeviceAllocationModeExactCount,
					Count:           int64(req.GPUCount),
				},
			},
		},
	}

	// Create annotations for vLLM workload
	annotations := map[string]string{
		"canhazgpu.dev/workload":   "vllm",
		"canhazgpu.dev/image-name": imageName,
		"canhazgpu.dev/repo-name":  repoName,
		"canhazgpu.dev/cmd":        strings.Join(cmdArgs, " "),
	}

	// Add prefer-node annotation if specified
	if req.PreferNode != "" {
		annotations["canhazgpu.dev/prefer-node"] = req.PreferNode
	}

	// Add port annotation if specified
	if req.Port > 0 {
		annotations["canhazgpu.dev/port"] = fmt.Sprintf("%d", req.Port)
	}

	// Add diff ConfigMap annotation if specified
	if diffConfigMap != "" {
		annotations["canhazgpu.dev/diff-configmap"] = diffConfigMap
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        req.Name,
			Namespace:   c.namespace,
			Annotations: annotations,
		},
		Spec: spec,
	}

	return c.resourceClient.ResourceClaims(c.namespace).Create(ctx, claim, metav1.CreateOptions{})
}

func (c *Client) WaitForAllocation(ctx context.Context, claimName string) (*AllocationResult, error) {
	return c.WaitForAllocationWithTimeout(ctx, claimName, 5*time.Minute)
}

func (c *Client) WaitForAllocationWithTimeout(ctx context.Context, claimName string, timeout time.Duration) (*AllocationResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	watchOpts := metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.name", claimName).String(),
		Watch:         true,
	}

	watcher, err := c.resourceClient.ResourceClaims(c.namespace).Watch(ctx, watchOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to watch ResourceClaim: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case event := <-watcher.ResultChan():
			if event.Type == watch.Error {
				return nil, fmt.Errorf("watch error: %v", event.Object)
			}

			claim, ok := event.Object.(*resourceapi.ResourceClaim)
			if !ok {
				continue
			}

			if claim.Status.Allocation != nil {
				// Parse allocation result
				result, err := parseAllocationResult(claim.Status.Allocation)
				if err != nil {
					return nil, fmt.Errorf("failed to parse allocation result: %w", err)
				}
				return result, nil
			}

		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for allocation")
		}
	}
}

func parseAllocationResult(allocation *resourceapi.AllocationResult) (*AllocationResult, error) {
	result := &AllocationResult{}

	// Extract node name from NodeSelector
	if allocation.NodeSelector != nil && len(allocation.NodeSelector.NodeSelectorTerms) > 0 {
		for _, term := range allocation.NodeSelector.NodeSelectorTerms {
			for _, expr := range term.MatchExpressions {
				if expr.Key == "kubernetes.io/hostname" && len(expr.Values) > 0 {
					result.NodeName = expr.Values[0]
					break
				}
			}
		}
	}

	// Extract GPU IDs from allocation results
	for _, device := range allocation.Devices.Results {
		if device.Driver == DriverName {
			// Parse GPU ID from device name like "gpu0"
			if strings.HasPrefix(device.Device, "gpu") {
				gpuIDStr := strings.TrimPrefix(device.Device, "gpu")
				if gpuID, err := strconv.Atoi(gpuIDStr); err == nil {
					result.AllocatedGPUs = append(result.AllocatedGPUs, gpuID)
				}
			}
		}
	}

	return result, nil
}

func (c *Client) CreatePod(ctx context.Context, req *PodRequest) (*corev1.Pod, error) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: c.namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "gpu-workload",
					Image:   req.Image,
					Command: req.Command,
				},
			},
			ResourceClaims: []corev1.PodResourceClaim{
				{
					Name: "gpu-claim",
					ResourceClaimName: &req.ClaimName,
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

	return c.clientset.CoreV1().Pods(c.namespace).Create(ctx, pod, metav1.CreateOptions{})
}

func (c *Client) WaitForPodRunning(ctx context.Context, podName string) error {
	timeout := 5 * time.Minute
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	watchOpts := metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.name", podName).String(),
		Watch:         true,
	}

	watcher, err := c.clientset.CoreV1().Pods(c.namespace).Watch(ctx, watchOpts)
	if err != nil {
		return fmt.Errorf("failed to watch Pod: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case event := <-watcher.ResultChan():
			if event.Type == watch.Error {
				return fmt.Errorf("watch error: %v", event.Object)
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			if pod.Status.Phase == corev1.PodRunning {
				return nil
			}

			if pod.Status.Phase == corev1.PodFailed {
				return fmt.Errorf("Pod failed: %s", pod.Status.Message)
			}

		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for Pod to run")
		}
	}
}

func (c *Client) StreamPodLogs(ctx context.Context, podName string) error {
	req := c.clientset.CoreV1().Pods(c.namespace).GetLogs(podName, &corev1.PodLogOptions{
		Follow: true,
	})

	podLogs, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("failed to stream logs: %w", err)
	}
	defer podLogs.Close()

	// Stream logs to stdout
	_, err = io.Copy(os.Stdout, podLogs)
	if err != nil {
		return fmt.Errorf("failed to copy logs to stdout: %w", err)
	}

	return nil
}

func (c *Client) GetClaimStatus(ctx context.Context, claimName string) (*ClaimStatus, error) {
	claim, err := c.resourceClient.ResourceClaims(c.namespace).Get(ctx, claimName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	status := &ClaimStatus{
		Name:  claim.Name,
		State: "Pending",
	}

	if claim.Status.Allocation != nil {
		status.State = "Allocated"
		status.Allocated = true

		result, err := parseAllocationResult(claim.Status.Allocation)
		if err == nil {
			status.NodeName = result.NodeName
			status.AllocatedGPUs = result.AllocatedGPUs
		}
	}

	// Find associated Pod
	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, pod := range pods.Items {
			for _, claimRef := range pod.Spec.ResourceClaims {
				if claimRef.ResourceClaimName != nil && *claimRef.ResourceClaimName == claimName {
					status.PodName = pod.Name
					status.PodPhase = pod.Status.Phase
					break
				}
			}
		}
	}

	return status, nil
}

func (c *Client) ListClaimStatuses(ctx context.Context) ([]*ClaimStatus, error) {
	claims, err := c.resourceClient.ResourceClaims(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var statuses []*ClaimStatus
	for _, claim := range claims.Items {
		status, err := c.GetClaimStatus(ctx, claim.Name)
		if err != nil {
			continue
		}
		statuses = append(statuses, status)
	}

	return statuses, nil
}

func (c *Client) DeleteResourceClaim(ctx context.Context, claimName string) error {
	// Delete parameters ConfigMap if it exists
	_ = c.clientset.CoreV1().ConfigMaps(c.namespace).Delete(ctx, claimName+"-params", metav1.DeleteOptions{})

	return c.resourceClient.ResourceClaims(c.namespace).Delete(ctx, claimName, metav1.DeleteOptions{})
}

func (c *Client) UpdateResourceClaim(ctx context.Context, claim *resourceapi.ResourceClaim) error {
	_, err := c.resourceClient.ResourceClaims(c.namespace).Update(ctx, claim, metav1.UpdateOptions{})
	return err
}

func (c *Client) DeletePod(ctx context.Context, podName string) error {
	return c.clientset.CoreV1().Pods(c.namespace).Delete(ctx, podName, metav1.DeleteOptions{})
}

func (c *Client) DeleteConfigMap(ctx context.Context, configMapName string) error {
	return c.clientset.CoreV1().ConfigMaps(c.namespace).Delete(ctx, configMapName, metav1.DeleteOptions{})
}

func (c *Client) CreatePodsForAllocatedClaims(ctx context.Context) error {
	// Find allocated ResourceClaims that don't have associated Pods yet
	claims, err := c.resourceClient.ResourceClaims(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list ResourceClaims: %w", err)
	}

	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
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

		// Skip if claim doesn't have pod-spec annotation
		podSpecJSON, exists := claim.Annotations["k8shazgpu.com/pod-spec"]
		if !exists {
			continue
		}

		// Parse Pod spec from annotation
		var podSpec PodSpec
		if err := json.Unmarshal([]byte(podSpecJSON), &podSpec); err != nil {
			fmt.Printf("Warning: failed to parse pod spec for claim %s: %v\n", claim.Name, err)
			continue
		}

		// Create Pod for this claim
		fmt.Printf("Creating Pod for allocated claim %s...\n", claim.Name)
		podReq := &PodRequest{
			Name:      claim.Name + "-pod",
			Image:     podSpec.Image,
			Command:   podSpec.Command,
			ClaimName: claim.Name,
		}

		_, err := c.CreatePod(ctx, podReq)
		if err != nil {
			fmt.Printf("Warning: failed to create Pod for claim %s: %v\n", claim.Name, err)
			continue
		}

		fmt.Printf("✓ Created Pod %s for claim %s\n", podReq.Name, claim.Name)
	}

	return nil
}

func (c *Client) GetGPUSummary(ctx context.Context) (*GPUSummary, error) {
	// Try to get real-time status from node agents first
	summary, err := c.getGPUSummaryFromNodeAgents(ctx)
	if err == nil {
		return summary, nil
	}

	// Fallback to ResourceClaims-only calculation if node agents are unreachable
	return c.getGPUSummaryFromClaims(ctx)
}

func (c *Client) getGPUSummaryFromNodeAgents(ctx context.Context) (*GPUSummary, error) {
	// Get nodes to query their agents
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	summary := &GPUSummary{}

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

		// Query node agent for real-time GPU status
		nodeInfo, err := c.getNodeGPUInfo(ctx, node.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get GPU info from node %s: %w", node.Name, err)
		}

		summary.Nodes = append(summary.Nodes, *nodeInfo)
		summary.TotalGPUs += nodeInfo.TotalGPUs
		summary.AvailableGPUs += len(nodeInfo.AvailableGPUs)
		summary.AllocatedGPUs += len(nodeInfo.AllocatedGPUs)
	}

	return summary, nil
}

func (c *Client) getGPUSummaryFromClaims(ctx context.Context) (*GPUSummary, error) {
	// Get all ResourceClaims across all namespaces
	claims, err := c.resourceClient.ResourceClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list ResourceClaims: %w", err)
	}

	// Get nodes to understand total GPU capacity
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	summary := &GPUSummary{}
	nodeGPUMap := make(map[string]*NodeGPUInfo)

	// Initialize nodes - assume 1 GPU per ready node for now
	// In a real implementation, this should come from node labels or node agent query
	for _, node := range nodes.Items {
		ready := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				ready = true
				break
			}
		}

		if ready {
			nodeInfo := &NodeGPUInfo{
				NodeName:      node.Name,
				TotalGPUs:     1, // Hardcoded for now - should be configurable
				AvailableGPUs: []int{0}, // Start with GPU 0 available
				AllocatedGPUs: []AllocatedGPUInfo{},
			}
			nodeGPUMap[node.Name] = nodeInfo
			summary.TotalGPUs++
		}
	}

	// Process allocated claims
	for _, claim := range claims.Items {
		if claim.Status.Allocation != nil {
			// Parse allocation to get node and GPU info
			result, err := parseAllocationResult(claim.Status.Allocation)
			if err != nil {
				continue
			}

			if nodeInfo, exists := nodeGPUMap[result.NodeName]; exists {
				// Remove allocated GPUs from available list
				for _, allocatedGPU := range result.AllocatedGPUs {
					// Remove from available
					for i, availGPU := range nodeInfo.AvailableGPUs {
						if availGPU == allocatedGPU {
							nodeInfo.AvailableGPUs = append(nodeInfo.AvailableGPUs[:i], nodeInfo.AvailableGPUs[i+1:]...)
							break
						}
					}

					// Add to allocated
					allocInfo := AllocatedGPUInfo{
						ID:        allocatedGPU,
						ClaimUID:  string(claim.UID),
						Namespace: claim.Namespace,
					}

					// Try to find associated Pod
					pods, err := c.clientset.CoreV1().Pods(claim.Namespace).List(ctx, metav1.ListOptions{})
					if err == nil {
						for _, pod := range pods.Items {
							for _, claimRef := range pod.Spec.ResourceClaims {
								if claimRef.ResourceClaimName != nil && *claimRef.ResourceClaimName == claim.Name {
									allocInfo.PodName = pod.Name
									break
								}
							}
						}
					}

					nodeInfo.AllocatedGPUs = append(nodeInfo.AllocatedGPUs, allocInfo)
				}
			}
		}
	}

	// Calculate totals and add nodes to summary
	for _, nodeInfo := range nodeGPUMap {
		summary.Nodes = append(summary.Nodes, *nodeInfo)
		summary.AvailableGPUs += len(nodeInfo.AvailableGPUs)
		summary.AllocatedGPUs += len(nodeInfo.AllocatedGPUs)
	}

	return summary, nil
}

func (c *Client) getNodeGPUInfo(ctx context.Context, nodeName string) (*NodeGPUInfo, error) {
	return c.getNodeGPUInfoByIP(ctx, nodeName, nodeName)
}

func (c *Client) getNodeGPUInfoByIP(ctx context.Context, nodeName, nodeIP string) (*NodeGPUInfo, error) {
	// Make HTTP request to node agent
	nodeAgentURL := fmt.Sprintf("http://%s:8082/status", nodeIP)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", nodeAgentURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query node agent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("node agent returned status %d", resp.StatusCode)
	}

	// Parse response - need to define the structure based on node agent API
	var nodeStatus struct {
		NodeName      string `json:"nodeName"`
		TotalGPUs     int    `json:"totalGPUs"`
		AvailableGPUs []int  `json:"availableGPUs"`
		AllocatedGPUs []struct {
			ID        int    `json:"id"`
			ClaimUID  string `json:"claimUID"`
			PodName   string `json:"podName,omitempty"`
			Namespace string `json:"namespace"`
		} `json:"allocatedGPUs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&nodeStatus); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	nodeInfo := &NodeGPUInfo{
		NodeName:      nodeStatus.NodeName,
		TotalGPUs:     nodeStatus.TotalGPUs,
		AvailableGPUs: nodeStatus.AvailableGPUs,
	}

	// Convert allocated GPUs
	for _, gpu := range nodeStatus.AllocatedGPUs {
		nodeInfo.AllocatedGPUs = append(nodeInfo.AllocatedGPUs, AllocatedGPUInfo{
			ID:        gpu.ID,
			ClaimUID:  gpu.ClaimUID,
			PodName:   gpu.PodName,
			Namespace: gpu.Namespace,
		})
	}

	return nodeInfo, nil
}