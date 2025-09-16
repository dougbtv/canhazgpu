package k8s

import (
	"context"
	"fmt"
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

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: c.namespace,
		},
		Spec: spec,
	}

	return c.resourceClient.ResourceClaims(c.namespace).Create(ctx, claim, metav1.CreateOptions{})
}

func (c *Client) WaitForAllocation(ctx context.Context, claimName string) (*AllocationResult, error) {
	timeout := 5 * time.Minute
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

	// Extract GPU IDs from CDI device references
	for _, device := range allocation.Devices.Results {
		if strings.HasPrefix(device.Driver, DriverName) {
			// Parse GPU ID from device name like "canhazgpu.com/gpu0"
			parts := strings.Split(device.Device, "/")
			if len(parts) >= 2 {
				gpuIDStr := strings.TrimPrefix(parts[1], "gpu")
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

	// TODO: Stream logs to stdout
	// For now, just return - this would need proper log streaming implementation
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

func (c *Client) DeletePod(ctx context.Context, podName string) error {
	return c.clientset.CoreV1().Pods(c.namespace).Delete(ctx, podName, metav1.DeleteOptions{})
}