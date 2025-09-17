package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"

	"github.com/russellb/canhazgpu/pkg/cache/types"
)

const (
	CacheRootPath = "/var/lib/canhazgpu-cache"
	GitCachePath  = CacheRootPath + "/git"
)

// Reconciler handles cache reconciliation on a node
type Reconciler struct {
	client   dynamic.Interface
	nodeName string
	criType  string // "crio" or "containerd"
}

// NewReconciler creates a new cache reconciler
func NewReconciler(client dynamic.Interface, nodeName string) *Reconciler {
	r := &Reconciler{
		client:   client,
		nodeName: nodeName,
	}
	r.detectCRI()
	return r
}

// detectCRI detects the container runtime interface
func (r *Reconciler) detectCRI() {
	// Check for CRI-O socket first
	if _, err := os.Stat("/host/run/crio/crio.sock"); err == nil {
		r.criType = "crio"
		klog.Info("Detected CRI-O container runtime")
		return
	}

	// Check for containerd socket
	if _, err := os.Stat("/host/run/containerd/containerd.sock"); err == nil {
		r.criType = "containerd"
		klog.Info("Detected containerd container runtime")
		return
	}

	klog.Warning("No container runtime socket detected, image operations will fail")
}

// Reconcile performs cache reconciliation
func (r *Reconciler) Reconcile(ctx context.Context) error {
	// Get CachePlan
	plan, err := r.getCachePlan(ctx, "default")
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(4).Info("No CachePlan found, skipping cache reconciliation")
			return nil
		}
		return fmt.Errorf("failed to get CachePlan: %w", err)
	}

	// Initialize status
	status := &types.NodeCacheStatusData{
		NodeName:   r.nodeName,
		Images:     []types.ImageStatus{},
		GitRepos:   []types.GitRepoStatus{},
		Errors:     []string{},
		LastUpdate: &metav1.Time{Time: time.Now()},
	}

	// Ensure cache directories exist
	if err := r.ensureCacheDirectories(); err != nil {
		status.Errors = append(status.Errors, fmt.Sprintf("Failed to create cache directories: %v", err))
	}

	// Process cache items
	for _, item := range plan.Spec.Items {
		// For Phase 2, only handle scope: allNodes
		if item.Scope != "" && item.Scope != "allNodes" {
			klog.V(4).Infof("Skipping item %s with scope %s (not allNodes)", item.Name, item.Scope)
			continue
		}

		switch item.Type {
		case types.CacheItemTypeImage:
			if item.Image != nil {
				imgStatus := r.reconcileImage(ctx, item.Name, item.Image)
				status.Images = append(status.Images, imgStatus)
			}
		case types.CacheItemTypeGitRepo:
			if item.GitRepo != nil {
				repoStatus := r.reconcileGitRepo(ctx, item.Name, item.GitRepo)
				status.GitRepos = append(status.GitRepos, repoStatus)
			}
		default:
			klog.V(4).Infof("Skipping unsupported cache item type %s for item %s", item.Type, item.Name)
		}
	}

	// Update NodeCacheStatus
	return r.updateNodeCacheStatus(ctx, status)
}

// ensureCacheDirectories creates necessary cache directories
func (r *Reconciler) ensureCacheDirectories() error {
	dirs := []string{
		CacheRootPath,
		GitCachePath,
		CacheRootPath + "/wheels", // For future use
		CacheRootPath + "/models", // For future use
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

// reconcileImage ensures an image is present in the host runtime
func (r *Reconciler) reconcileImage(ctx context.Context, name string, img *types.ImageCache) types.ImageStatus {
	status := types.ImageStatus{
		Ref:         img.Ref,
		Present:     false,
		LastChecked: &metav1.Time{Time: time.Now()},
	}

	if r.criType == "" {
		status.Message = "No container runtime detected"
		return status
	}

	// Check if image is present
	present, digest, err := r.checkImagePresent(img.Ref)
	if err != nil {
		status.Message = fmt.Sprintf("Failed to check image: %v", err)
		return status
	}

	if present {
		status.Present = true
		status.Digest = digest
		status.Message = fmt.Sprintf("Present via %s", r.criType)
		return status
	}

	// Pull image
	klog.Infof("Pulling image %s", img.Ref)
	if err := r.pullImage(img.Ref); err != nil {
		status.Message = fmt.Sprintf("Failed to pull image: %v", err)
		return status
	}

	// Verify image is now present
	present, digest, err = r.checkImagePresent(img.Ref)
	if err != nil {
		status.Message = fmt.Sprintf("Failed to verify pulled image: %v", err)
		return status
	}

	status.Present = present
	status.Digest = digest
	if present {
		status.Message = fmt.Sprintf("Successfully pulled via %s", r.criType)
	} else {
		status.Message = "Image pull appeared to succeed but image not found"
	}

	return status
}

// checkImagePresent checks if an image is present and returns its digest
func (r *Reconciler) checkImagePresent(ref string) (bool, string, error) {
	var cmd *exec.Cmd

	switch r.criType {
	case "crio":
		cmd = exec.Command("crictl", "images", "-o", "json")
		cmd.Env = append(os.Environ(), "CRICTL_RUNTIME_ENDPOINT=unix:///host/run/crio/crio.sock")
	case "containerd":
		cmd = exec.Command("crictl", "images", "-o", "json")
		cmd.Env = append(os.Environ(), "CRICTL_RUNTIME_ENDPOINT=unix:///host/run/containerd/containerd.sock")
	default:
		return false, "", fmt.Errorf("unsupported CRI type: %s", r.criType)
	}

	output, err := cmd.Output()
	if err != nil {
		return false, "", fmt.Errorf("failed to list images: %w", err)
	}

	var result struct {
		Images []struct {
			ID       string   `json:"id"`
			RepoTags []string `json:"repoTags"`
		} `json:"images"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		return false, "", fmt.Errorf("failed to parse images output: %w", err)
	}

	for _, img := range result.Images {
		for _, tag := range img.RepoTags {
			if tag == ref {
				return true, img.ID, nil
			}
		}
	}

	return false, "", nil
}

// pullImage pulls an image using the container runtime
func (r *Reconciler) pullImage(ref string) error {
	var cmd *exec.Cmd

	switch r.criType {
	case "crio":
		cmd = exec.Command("crictl", "pull", ref)
		cmd.Env = append(os.Environ(), "CRICTL_RUNTIME_ENDPOINT=unix:///host/run/crio/crio.sock")
	case "containerd":
		cmd = exec.Command("crictl", "pull", ref)
		cmd.Env = append(os.Environ(), "CRICTL_RUNTIME_ENDPOINT=unix:///host/run/containerd/containerd.sock")
	default:
		return fmt.Errorf("unsupported CRI type: %s", r.criType)
	}

	// Set timeout for image pull
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to pull image %s: %w (output: %s)", ref, err, string(output))
	}

	return nil
}

// reconcileGitRepo ensures a git repository is cloned and synced
func (r *Reconciler) reconcileGitRepo(ctx context.Context, name string, repo *types.GitRepoCache) types.GitRepoStatus {
	repoPath := filepath.Join(GitCachePath, repo.PathName)
	status := types.GitRepoStatus{
		Name:     name,
		Path:     repoPath,
		URL:      repo.URL,
		Branch:   repo.Branch,
		Synced:   false,
		LastSync: &metav1.Time{Time: time.Now()},
	}

	// Check if repo exists
	gitDir := filepath.Join(repoPath, ".git")
	repoExists := false
	if _, err := os.Stat(gitDir); err == nil {
		repoExists = true
	}

	if !repoExists {
		// Clone repository
		klog.Infof("Cloning repository %s to %s", repo.URL, repoPath)
		if err := r.cloneRepo(repo.URL, repoPath); err != nil {
			status.Message = fmt.Sprintf("Failed to clone repository: %v", err)
			return status
		}
	}

	// Sync repository
	if err := r.syncRepo(repoPath, repo); err != nil {
		status.Message = fmt.Sprintf("Failed to sync repository: %v", err)
		return status
	}

	// Get current commit
	commit, err := r.getCurrentCommit(repoPath)
	if err != nil {
		status.Message = fmt.Sprintf("Failed to get current commit: %v", err)
		return status
	}

	status.Commit = commit
	status.Synced = true
	status.Message = "Successfully synced"

	return status
}

// cloneRepo clones a git repository
func (r *Reconciler) cloneRepo(url, path string) error {
	cmd := exec.Command("git", "clone", url, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %w (output: %s)", err, string(output))
	}
	return nil
}

// syncRepo syncs a git repository according to the sync strategy
func (r *Reconciler) syncRepo(path string, repo *types.GitRepoCache) error {
	// Fetch latest changes
	cmd := exec.Command("git", "-C", path, "fetch", "--all", "--prune")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch failed: %w (output: %s)", err, string(output))
	}

	// Handle branch checkout and sync strategy
	if repo.Branch != "" {
		// Checkout branch
		cmd = exec.Command("git", "-C", path, "checkout", repo.Branch)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git checkout branch failed: %w (output: %s)", err, string(output))
		}

		// Apply sync strategy (default is hardReset)
		if repo.SyncStrategy == "" || repo.SyncStrategy == "hardReset" {
			cmd = exec.Command("git", "-C", path, "reset", "--hard", "origin/"+repo.Branch)
			if output, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("git reset --hard failed: %w (output: %s)", err, string(output))
			}
		}
	}

	// If specific commit is requested, checkout that commit
	if repo.Commit != "" {
		cmd = exec.Command("git", "-C", path, "checkout", repo.Commit)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git checkout commit failed: %w (output: %s)", err, string(output))
		}
	}

	return nil
}

// getCurrentCommit gets the current commit hash
func (r *Reconciler) getCurrentCommit(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current commit: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// getCachePlan retrieves the CachePlan from the cluster
func (r *Reconciler) getCachePlan(ctx context.Context, name string) (*types.CachePlan, error) {
	gvr := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "cacheplans",
	}

	obj, err := r.client.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// Convert unstructured to CachePlan
	plan := &types.CachePlan{}
	if err := convertUnstructured(obj, plan); err != nil {
		return nil, fmt.Errorf("failed to convert CachePlan: %w", err)
	}

	return plan, nil
}

// updateNodeCacheStatus updates the NodeCacheStatus in the cluster
func (r *Reconciler) updateNodeCacheStatus(ctx context.Context, statusData *types.NodeCacheStatusData) error {
	gvr := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "nodecachestatuses",
	}

	// Try to get existing status
	existing, err := r.client.Resource(gvr).Get(ctx, r.nodeName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get existing NodeCacheStatus: %w", err)
	}

	status := &types.NodeCacheStatus{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "canhazgpu.dev/v1alpha1",
			Kind:       "NodeCacheStatus",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: r.nodeName,
			Labels: map[string]string{
				"kubernetes.io/hostname": r.nodeName,
			},
		},
		Status: *statusData,
	}

	// Convert to unstructured
	unstructuredStatus, err := convertToUnstructured(status)
	if err != nil {
		return fmt.Errorf("failed to convert status to unstructured: %w", err)
	}

	if errors.IsNotFound(err) {
		// Create new status
		_, err = r.client.Resource(gvr).Create(ctx, unstructuredStatus, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create NodeCacheStatus: %w", err)
		}
	} else {
		// Update existing status
		unstructuredStatus.SetResourceVersion(existing.GetResourceVersion())
		_, err = r.client.Resource(gvr).Update(ctx, unstructuredStatus, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update NodeCacheStatus: %w", err)
		}
	}

	return nil
}

// Helper functions for unstructured conversion
func convertUnstructured(obj *unstructured.Unstructured, target interface{}) error {
	data, err := obj.MarshalJSON()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func convertToUnstructured(obj interface{}) (*unstructured.Unstructured, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}

	result := &unstructured.Unstructured{}
	if err := result.UnmarshalJSON(data); err != nil {
		return nil, err
	}

	return result, nil
}