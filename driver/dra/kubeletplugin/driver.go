package main

import (
	"context"
	"fmt"
	"path/filepath"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/klog/v2"
)

type driver struct {
	client    kubernetes.Interface
	helper    *kubeletplugin.Helper
	config    *Config
	cancelCtx func(error)
}

func NewDriver(ctx context.Context, config *Config, kubeClient kubernetes.Interface) (*driver, error) {
	driver := &driver{
		client: kubeClient,
		config: config,
	}

	// Start the kubelet plugin
	helper, err := kubeletplugin.Start(
		ctx,
		driver,
		kubeletplugin.KubeClient(kubeClient),
		kubeletplugin.NodeName(config.nodeName),
		kubeletplugin.DriverName(DriverName),
		kubeletplugin.RegistrarDirectoryPath(config.kubeletRegistrarDirectoryPath),
		kubeletplugin.PluginDataDirectoryPath(filepath.Join(config.kubeletPluginsDirectoryPath, DriverName)),
	)
	if err != nil {
		return nil, err
	}
	driver.helper = helper

	// Create and publish device resources
	devices := make([]resourceapi.Device, config.numDevices)
	for i := 0; i < config.numDevices; i++ {
		devices[i] = resourceapi.Device{
			Name: fmt.Sprintf("gpu%d", i),
		}
	}

	resources := resourceslice.DriverResources{
		Pools: map[string]resourceslice.Pool{
			"node": {
				Slices: []resourceslice.Slice{
					{
						Devices: devices,
					},
				},
			},
		},
	}

	if err := helper.PublishResources(ctx, resources); err != nil {
		return nil, err
	}

	return driver, nil
}

func (d *driver) Shutdown(logger klog.Logger) error {
	if d.helper != nil {
		d.helper.Stop()
	}
	return nil
}

// PrepareResourceClaims is called by kubelet to prepare devices for pods
func (d *driver) PrepareResourceClaims(ctx context.Context, claims []*resourceapi.ResourceClaim) (map[types.UID]kubeletplugin.PrepareResult, error) {
	logger := klog.FromContext(ctx)
	logger.Info("PrepareResourceClaims called", "claims", len(claims))

	result := make(map[types.UID]kubeletplugin.PrepareResult)

	for _, claim := range claims {
		var gpuIDs []int
		var devices []kubeletplugin.Device

		// Extract GPU IDs from the claim allocation results
		if claim.Status.Allocation != nil && claim.Status.Allocation.Devices.Results != nil {
			for _, allocationResult := range claim.Status.Allocation.Devices.Results {
				if allocationResult.Driver == DriverName {
					var gpuID int
					if _, err := fmt.Sscanf(allocationResult.Device, "gpu%d", &gpuID); err == nil {
						gpuIDs = append(gpuIDs, gpuID)

						// Use NVIDIA CDI device - try the 'all' device first as a test
						cdiDeviceID := "nvidia.com/gpu=all"
						devices = append(devices, kubeletplugin.Device{
							Requests:     []string{allocationResult.Request},
							PoolName:     allocationResult.Pool,
							DeviceName:   allocationResult.Device,
							CDIDeviceIDs: []string{cdiDeviceID},
						})
					}
				}
			}
		}

		if len(gpuIDs) == 0 {
			result[claim.UID] = kubeletplugin.PrepareResult{
				Err: fmt.Errorf("no GPUs allocated for claim %s", claim.Name),
			}
			continue
		}

		// For now, we'll rely on the node agent to have created the CDI spec
		// In a full implementation, we would generate and write CDI specs here

		logger.Info("Successfully prepared resources",
			"claim", claim.Name,
			"claimUID", claim.UID,
			"gpuIDs", gpuIDs)

		result[claim.UID] = kubeletplugin.PrepareResult{
			Devices: devices,
		}
	}

	return result, nil
}

// UnprepareResourceClaims is called by kubelet to clean up devices after pods
func (d *driver) UnprepareResourceClaims(ctx context.Context, claims []kubeletplugin.NamespacedObject) (map[types.UID]error, error) {
	logger := klog.FromContext(ctx)
	logger.Info("UnprepareResourceClaims called", "claims", len(claims))

	result := make(map[types.UID]error)

	for _, claim := range claims {
		// For now, we don't need to do any cleanup
		// The CDI spec file can remain as it's shared across pods
		logger.Info("Unprepared resources", "claim", claim.Name, "claimUID", claim.UID)
		result[claim.UID] = nil
	}

	return result, nil
}

// HandleError handles background errors
func (d *driver) HandleError(ctx context.Context, err error, msg string) {
	logger := klog.FromContext(ctx)
	utilruntime.HandleErrorWithContext(ctx, err, msg)

	// If the error is fatal, we could cancel the main context to shut down gracefully
	// For now, just log it
	logger.Error(err, msg)
}