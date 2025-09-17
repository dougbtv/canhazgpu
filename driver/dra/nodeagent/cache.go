package main

import (
	"context"
	"os"
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

// Reconcile performs basic cache reconciliation
func (r *SimpleCacheReconciler) Reconcile(ctx context.Context) error {
	klog.V(4).Info("Starting cache reconciliation")

	// Check if cache directory exists, create if not
	cacheDir := "/var/lib/canhazgpu-cache"
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		klog.Errorf("Failed to create cache directory: %v", err)
	}

	// Create or update NodeCacheStatus
	return r.updateNodeCacheStatus(ctx)
}

// updateNodeCacheStatus creates/updates the NodeCacheStatus
func (r *SimpleCacheReconciler) updateNodeCacheStatus(ctx context.Context) error {
	gvr := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "nodecachestatuses",
	}

	status := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "canhazgpu.dev/v1alpha1",
			"kind":       "NodeCacheStatus",
			"metadata": map[string]interface{}{
				"name": r.nodeName,
				"labels": map[string]interface{}{
					"kubernetes.io/hostname": r.nodeName,
				},
			},
			"status": map[string]interface{}{
				"nodeName":   r.nodeName,
				"images":     []interface{}{},
				"gitRepos":   []interface{}{},
				"errors":     []interface{}{},
				"lastUpdate": time.Now().Format(time.RFC3339),
			},
		},
	}

	// Try to get existing status
	existing, err := r.client.Resource(gvr).Get(ctx, r.nodeName, metav1.GetOptions{})
	if err != nil {
		// Create new status
		_, err = r.client.Resource(gvr).Create(ctx, status, metav1.CreateOptions{})
		if err != nil {
			return err
		}
		klog.V(4).Infof("Created NodeCacheStatus for node %s", r.nodeName)
	} else {
		// Update existing status
		status.SetResourceVersion(existing.GetResourceVersion())
		_, err = r.client.Resource(gvr).Update(ctx, status, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
		klog.V(4).Infof("Updated NodeCacheStatus for node %s", r.nodeName)
	}

	return nil
}