package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
)

// SimpleCacheReconciler is a minimal cache reconciler for testing
type SimpleCacheReconciler struct {
	client   dynamic.Interface
	nodeName string
}

// NewSimpleCacheReconciler creates a simple cache reconciler
func NewSimpleCacheReconciler(client dynamic.Interface, nodeName string) *SimpleCacheReconciler {
	return &SimpleCacheReconciler{
		client:   client,
		nodeName: nodeName,
	}
}

// Reconcile performs cache reconciliation by fetching cache plans and pulling images
func (r *SimpleCacheReconciler) Reconcile(ctx context.Context) error {
	klog.V(4).Info("Starting cache reconciliation")

	// Check if cache directory exists, create if not
	cacheDir := "/var/lib/canhazgpu-cache"
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		klog.Errorf("Failed to create cache directory: %v", err)
	}

	// Get cache plans and process them
	pulledImages, errors := r.processCachePlans(ctx)

	// Create or update NodeCacheStatus with actual results
	return r.updateNodeCacheStatus(ctx, pulledImages, errors)
}

// processCachePlans fetches cache plans and pulls required images
func (r *SimpleCacheReconciler) processCachePlans(ctx context.Context) ([]map[string]interface{}, []string) {
	var pulledImages []map[string]interface{}
	var errors []string

	// Get cache plans
	cachePlans, err := r.getCachePlans(ctx)
	if err != nil {
		klog.Errorf("Failed to get cache plans: %v", err)
		errors = append(errors, fmt.Sprintf("Failed to get cache plans: %v", err))
		return pulledImages, errors
	}

	// Process each cache plan
	for _, plan := range cachePlans {
		items, ok := plan.Object["spec"].(map[string]interface{})["items"].([]interface{})
		if !ok {
			continue
		}

		for _, item := range items {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}

			itemType, ok := itemMap["type"].(string)
			if !ok || itemType != "image" {
				continue
			}

			// Extract image reference
			imageData, ok := itemMap["image"].(map[string]interface{})
			if !ok {
				continue
			}

			imageRef, ok := imageData["ref"].(string)
			if !ok {
				continue
			}

			// Pull the image
			klog.Infof("Pulling image: %s", imageRef)
			if err := r.pullImage(imageRef); err != nil {
				klog.Errorf("Failed to pull image %s: %v", imageRef, err)
				errors = append(errors, fmt.Sprintf("Failed to pull image %s: %v", imageRef, err))
			} else {
				klog.Infof("Successfully pulled image: %s", imageRef)
				pulledImages = append(pulledImages, map[string]interface{}{
					"ref":         imageRef,
					"present":     true,
					"digest":      "", // TODO: get actual digest from crictl
					"lastChecked": time.Now().Format(time.RFC3339),
					"message":     "Successfully pulled",
				})
			}
		}
	}

	return pulledImages, errors
}

// getCachePlans fetches all cache plans from the cluster
func (r *SimpleCacheReconciler) getCachePlans(ctx context.Context) ([]unstructured.Unstructured, error) {
	gvr := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "cacheplans",
	}

	list, err := r.client.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	return list.Items, nil
}

// pullImage pulls an image using crictl
func (r *SimpleCacheReconciler) pullImage(imageRef string) error {
	// Use crictl to pull the image via CRI-O socket
	cmd := exec.Command("crictl", "--runtime-endpoint", "unix:///host/run/crio/crio.sock", "pull", imageRef)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("crictl pull failed: %v, output: %s", err, string(output))
	}
	klog.V(4).Infof("crictl pull output: %s", string(output))
	return nil
}

// updateNodeCacheStatus creates/updates the NodeCacheStatus
func (r *SimpleCacheReconciler) updateNodeCacheStatus(ctx context.Context, pulledImages []map[string]interface{}, errors []string) error {
	gvr := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "nodecachestatuses",
	}

	// Create base resource (without status field)
	resource := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "canhazgpu.dev/v1alpha1",
			"kind":       "NodeCacheStatus",
			"metadata": map[string]interface{}{
				"name": r.nodeName,
				"labels": map[string]interface{}{
					"kubernetes.io/hostname": r.nodeName,
				},
			},
		},
	}

	// Status data to be set separately
	statusData := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "canhazgpu.dev/v1alpha1",
			"kind":       "NodeCacheStatus",
			"metadata": map[string]interface{}{
				"name": r.nodeName,
			},
			"status": map[string]interface{}{
				"nodeName":   r.nodeName,
				"images":     pulledImages,
				"gitRepos":   []interface{}{},
				"errors":     errors,
				"lastUpdate": time.Now().Format(time.RFC3339),
			},
		},
	}

	// Try to get existing resource
	existing, err := r.client.Resource(gvr).Get(ctx, r.nodeName, metav1.GetOptions{})
	if err != nil {
		// Create new resource first
		created, err := r.client.Resource(gvr).Create(ctx, resource, metav1.CreateOptions{})
		if err != nil {
			klog.Errorf("Failed to create NodeCacheStatus for node %s: %v", r.nodeName, err)
			return err
		}
		klog.Infof("Created NodeCacheStatus for node %s", r.nodeName)

		// Now update the status subresource
		statusData.SetResourceVersion(created.GetResourceVersion())
		_, err = r.client.Resource(gvr).UpdateStatus(ctx, statusData, metav1.UpdateOptions{})
		if err != nil {
			klog.Errorf("Failed to update status for NodeCacheStatus %s: %v", r.nodeName, err)
			return err
		}
		klog.Infof("Updated status for NodeCacheStatus %s with %d images", r.nodeName, len(pulledImages))
	} else {
		// Update status subresource for existing resource
		statusData.SetResourceVersion(existing.GetResourceVersion())
		_, err = r.client.Resource(gvr).UpdateStatus(ctx, statusData, metav1.UpdateOptions{})
		if err != nil {
			klog.Errorf("Failed to update status for NodeCacheStatus %s: %v", r.nodeName, err)
			return err
		}
		klog.Infof("Updated status for NodeCacheStatus %s with %d images", r.nodeName, len(pulledImages))
	}

	return nil
}