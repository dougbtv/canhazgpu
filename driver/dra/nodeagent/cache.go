package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
)

// SimpleCacheReconciler is a minimal cache reconciler for testing
type SimpleCacheReconciler struct {
	client             dynamic.Interface
	nodeName           string
	lastFullReconcile  time.Time
	lastCachePlanHash  string
	currentImageStatus map[string]map[string]interface{} // imageRef -> status map
}

// NewSimpleCacheReconciler creates a simple cache reconciler
func NewSimpleCacheReconciler(client dynamic.Interface, nodeName string) *SimpleCacheReconciler {
	return &SimpleCacheReconciler{
		client:             client,
		nodeName:           nodeName,
		currentImageStatus: make(map[string]map[string]interface{}),
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

	// Check git availability
	if err := r.checkGitAvailability(); err != nil {
		klog.Warningf("Git not available: %v - git repository caching will be disabled", err)
	}

	// Get cache plans and process them
	pulledImages, clonedRepos, errors := r.processCachePlans(ctx)

	// Create or update NodeCacheStatus with actual results
	return r.updateNodeCacheStatus(ctx, pulledImages, clonedRepos, errors)
}

// processCachePlans fetches cache plans and pulls required images
func (r *SimpleCacheReconciler) processCachePlans(ctx context.Context) ([]map[string]interface{}, []map[string]interface{}, []string) {
	var pulledImages []map[string]interface{}
	var clonedRepos []map[string]interface{}
	var errors []string

	// Get cache plans
	cachePlans, err := r.getCachePlans(ctx)
	if err != nil {
		klog.Errorf("Failed to get cache plans: %v", err)
		errors = append(errors, fmt.Sprintf("Failed to get cache plans: %v", err))
		return pulledImages, clonedRepos, errors
	}

	// Calculate cache plan hash to detect changes
	planHash := r.calculatePlanHash(cachePlans)
	planChanged := planHash != r.lastCachePlanHash

	// Check if we should do a full reconciliation (hourly or if plan changed)
	now := time.Now()
	timeSinceLastFull := now.Sub(r.lastFullReconcile)
	shouldFullReconcile := planChanged || timeSinceLastFull >= time.Hour

	if planChanged {
		klog.Infof("Cache plan changed, triggering immediate reconciliation")
		r.lastCachePlanHash = planHash
	} else if shouldFullReconcile {
		klog.Infof("Performing hourly reconciliation (last: %v ago)", timeSinceLastFull)
	} else {
		klog.V(4).Infof("Skipping reconciliation - last full reconcile was %v ago (< 1 hour) and no plan changes", timeSinceLastFull)
		// Return current status without pulling
		images, repos := r.getCurrentStatus()
		return images, repos, errors
	}

	if shouldFullReconcile {
		r.lastFullReconcile = now
	}

	// Process each cache plan
	for _, plan := range cachePlans {
		// Check for cache update annotations first
		updateRequests := r.checkForUpdateAnnotations(&plan)

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
			if !ok {
				continue
			}

			name, _ := itemMap["name"].(string)

			// Check if this item has a pending update request
			updateRequest, hasUpdate := updateRequests[name]

			switch itemType {
			case "image":
				r.processImageItem(itemMap, planChanged, &pulledImages, &errors)
			case "gitRepo":
				r.processGitRepoItem(itemMap, planChanged || hasUpdate, updateRequest, &clonedRepos, &errors)
			}
		}
	}

	return pulledImages, clonedRepos, errors
}

// processImageItem handles processing of image cache items
func (r *SimpleCacheReconciler) processImageItem(itemMap map[string]interface{}, planChanged bool, pulledImages *[]map[string]interface{}, errors *[]string) {
	imageData, ok := itemMap["image"].(map[string]interface{})
	if !ok {
		return
	}

	imageRef, ok := imageData["ref"].(string)
	if !ok {
		return
	}

	name, _ := itemMap["name"].(string)
	if name == "" {
		name = imageRef
	}

	// Check if we already have status for this image
	existingStatus, exists := r.currentImageStatus[imageRef]

	// If plan didn't change and we have recent status, use it
	if !planChanged && exists {
		*pulledImages = append(*pulledImages, existingStatus)
		return
	}

	// Create status entry for this image
	imageStatus := map[string]interface{}{
		"ref":     imageRef,
		"name":    name,
		"present": false,
		"status":  "pulling",
		"message": "Pulling image...",
	}

	// Add to results immediately to show pulling status
	*pulledImages = append(*pulledImages, imageStatus)
	r.currentImageStatus[imageRef] = imageStatus

	// Actually pull the image
	err := r.pullImage(imageRef)
	if err != nil {
		klog.Errorf("Failed to pull image %s: %v", imageRef, err)
		imageStatus["status"] = "failed"
		imageStatus["message"] = fmt.Sprintf("Pull failed: %v", err)
		*errors = append(*errors, fmt.Sprintf("Failed to pull image %s: %v", imageRef, err))
	} else {
		klog.Infof("Successfully pulled image %s", imageRef)
		imageStatus["status"] = "ready"
		imageStatus["present"] = true
		imageStatus["message"] = "Pull completed successfully"
	}

	// Update the stored status
	r.currentImageStatus[imageRef] = imageStatus
}

// UpdateRequest represents a cache update request from annotations
type UpdateRequest struct {
	Timestamp string
	Force     bool
}

// checkForUpdateAnnotations checks for cache update annotations in the cache plan
func (r *SimpleCacheReconciler) checkForUpdateAnnotations(plan *unstructured.Unstructured) map[string]UpdateRequest {
	updateRequests := make(map[string]UpdateRequest)

	annotations := plan.GetAnnotations()
	if annotations == nil {
		return updateRequests
	}

	for key, value := range annotations {
		// Look for update annotations: canhazgpu.dev/update-repo-{name}
		if strings.HasPrefix(key, "canhazgpu.dev/update-repo-") {
			repoName := strings.TrimPrefix(key, "canhazgpu.dev/update-repo-")

			// Check for corresponding force annotation
			forceKey := fmt.Sprintf("canhazgpu.dev/force-update-%s", repoName)
			force := annotations[forceKey] == "true"

			updateRequests[repoName] = UpdateRequest{
				Timestamp: value,
				Force:     force,
			}

			klog.Infof("Found update request for repo %s: timestamp=%s, force=%v", repoName, value, force)
		}
	}

	return updateRequests
}

// processGitRepoItem handles processing of git repository cache items
func (r *SimpleCacheReconciler) processGitRepoItem(itemMap map[string]interface{}, shouldUpdate bool, updateRequest UpdateRequest, clonedRepos *[]map[string]interface{}, errors *[]string) {
	gitRepoData, ok := itemMap["gitRepo"].(map[string]interface{})
	if !ok {
		return
	}

	gitURL, ok := gitRepoData["url"].(string)
	if !ok {
		return
	}

	branch, ok := gitRepoData["branch"].(string)
	if !ok {
		branch = "main" // default branch
	}

	name, _ := itemMap["name"].(string)
	if name == "" {
		name = gitURL
	}

	// Get the pathName from gitRepo data for directory naming
	pathName, ok := gitRepoData["pathName"].(string)
	if !ok || pathName == "" {
		pathName = name // fallback to name if pathName not specified
	}

	// Check if we already have status for this repo
	repoKey := fmt.Sprintf("%s#%s", gitURL, branch)
	existingStatus, exists := r.currentImageStatus[repoKey] // Reusing image status map for simplicity

	// If no changes and no update request and we have recent status, use it
	if !shouldUpdate && exists {
		*clonedRepos = append(*clonedRepos, existingStatus)
		return
	}

	// Create status entry for this git repo
	statusMessage := fmt.Sprintf("Cloning repository (branch: %s)...", branch)
	if updateRequest.Force {
		statusMessage = fmt.Sprintf("Force updating repository (branch: %s)...", branch)
	} else if shouldUpdate && updateRequest.Timestamp != "" {
		statusMessage = fmt.Sprintf("Updating repository (branch: %s)...", branch)
	}

	repoStatus := map[string]interface{}{
		"ref":     gitURL,
		"name":    name,
		"branch":  branch,
		"present": false,
		"status":  "pulling",
		"message": statusMessage,
	}

	// Add to results immediately to show pulling status
	*clonedRepos = append(*clonedRepos, repoStatus)
	r.currentImageStatus[repoKey] = repoStatus

	// Actually clone/update the repository
	err := r.cloneGitRepo(gitURL, branch, pathName, updateRequest.Force)
	if err != nil {
		klog.Errorf("Failed to clone git repo %s (branch: %s): %v", gitURL, branch, err)
		repoStatus["status"] = "failed"
		repoStatus["message"] = fmt.Sprintf("Clone failed: %v", err)
		*errors = append(*errors, fmt.Sprintf("Failed to clone git repo %s (branch: %s): %v", gitURL, branch, err))
	} else {
		klog.Infof("Successfully cloned git repo %s (branch: %s)", gitURL, branch)
		repoStatus["status"] = "ready"
		repoStatus["present"] = true
		repoStatus["message"] = fmt.Sprintf("Clone completed successfully (branch: %s)", branch)
	}

	// Update the stored status
	r.currentImageStatus[repoKey] = repoStatus
}

// checkGitAvailability verifies git is available in the container
func (r *SimpleCacheReconciler) checkGitAvailability() error {
	cmd := exec.Command("git", "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git command not found: %v", err)
	}
	klog.V(4).Infof("Git available: %s", strings.TrimSpace(string(output)))
	return nil
}

// cloneGitRepo clones a git repository to the cache directory
func (r *SimpleCacheReconciler) cloneGitRepo(gitURL, branch, name string, force bool) error {
	// Check git availability first
	if err := r.checkGitAvailability(); err != nil {
		return fmt.Errorf("git not available: %v", err)
	}

	cacheDir := "/var/lib/canhazgpu-cache"

	// Create a directory name based on the repo name
	repoDir := fmt.Sprintf("%s/%s", cacheDir, name)

	// Check if directory already exists
	if _, err := os.Stat(repoDir); err == nil {
		// Directory exists, handle based on force flag
		if force {
			klog.Infof("Force update requested for %s, performing fetch and reset", repoDir)

			// Force update: fetch latest and reset to remote HEAD
			fetchCmd := exec.Command("git", "-C", repoDir, "fetch", "origin", branch)
			fetchOutput, fetchErr := fetchCmd.CombinedOutput()
			if fetchErr != nil {
				klog.V(4).Infof("Git fetch failed, trying fresh clone: %v, output: %s", fetchErr, string(fetchOutput))
				os.RemoveAll(repoDir)
			} else {
				// Reset to remote HEAD (handles force pushes)
				resetCmd := exec.Command("git", "-C", repoDir, "reset", "--hard", fmt.Sprintf("origin/%s", branch))
				resetOutput, resetErr := resetCmd.CombinedOutput()
				if resetErr != nil {
					klog.V(4).Infof("Git reset failed, trying fresh clone: %v, output: %s", resetErr, string(resetOutput))
					os.RemoveAll(repoDir)
				} else {
					klog.V(4).Infof("Git force update successful: %s", string(resetOutput))
					return nil
				}
			}
		} else {
			// Regular update: try pull
			klog.V(4).Infof("Repository directory %s exists, updating...", repoDir)

			// Change to repo directory and pull latest
			cmd := exec.Command("git", "-C", repoDir, "pull", "origin", branch)
			output, err := cmd.CombinedOutput()
			if err != nil {
				klog.V(4).Infof("Git pull failed, trying fresh clone: %v, output: %s", err, string(output))
				// Remove directory and do fresh clone
				os.RemoveAll(repoDir)
			} else {
				klog.V(4).Infof("Git pull output: %s", string(output))
				return nil
			}
		}
	}

	// Fresh clone
	cmd := exec.Command("git", "clone", "--branch", branch, "--depth", "1", gitURL, repoDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone failed: %v, output: %s", err, string(output))
	}

	klog.V(4).Infof("Git clone output: %s", string(output))
	return nil
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

// calculatePlanHash creates a hash of the cache plans for change detection
func (r *SimpleCacheReconciler) calculatePlanHash(plans []unstructured.Unstructured) string {
	var planData strings.Builder
	for _, plan := range plans {
		planData.WriteString(plan.GetResourceVersion())
		planData.WriteString(fmt.Sprintf("%v", plan.Object["spec"]))
	}
	return fmt.Sprintf("%x", planData.String())
}

// getCurrentStatus returns the current cached image and git repo status
func (r *SimpleCacheReconciler) getCurrentStatus() ([]map[string]interface{}, []map[string]interface{}) {
	var images []map[string]interface{}
	var repos []map[string]interface{}

	for key, status := range r.currentImageStatus {
		if ref, ok := status["ref"].(string); ok {
			// Check if this is a git repo (contains #branch in key or is a git URL)
			if contains(key, "#") || contains(ref, "github.com") || contains(ref, "gitlab.com") || contains(ref, ".git") {
				repos = append(repos, status)
			} else {
				images = append(images, status)
			}
		}
	}
	return images, repos
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
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
func (r *SimpleCacheReconciler) updateNodeCacheStatus(ctx context.Context, pulledImages []map[string]interface{}, clonedRepos []map[string]interface{}, errors []string) error {
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
				"gitRepos":   clonedRepos,
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
		klog.Infof("Updated status for NodeCacheStatus %s with %d images and %d git repos", r.nodeName, len(pulledImages), len(clonedRepos))
	} else {
		// Update status subresource for existing resource
		statusData.SetResourceVersion(existing.GetResourceVersion())
		_, err = r.client.Resource(gvr).UpdateStatus(ctx, statusData, metav1.UpdateOptions{})
		if err != nil {
			klog.Errorf("Failed to update status for NodeCacheStatus %s: %v", r.nodeName, err)
			return err
		}
		klog.Infof("Updated status for NodeCacheStatus %s with %d images and %d git repos", r.nodeName, len(pulledImages), len(clonedRepos))
	}

	return nil
}