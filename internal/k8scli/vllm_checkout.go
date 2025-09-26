package k8scli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// VLLMCheckoutInfo contains information about a vLLM git checkout
type VLLMCheckoutInfo struct {
	IsVLLMCheckout    bool
	WorkingDir        string
	RemoteURL         string
	CurrentBranch     string
	CurrentCommit     string
	MergeBaseCommit   string
	ImageRef          string
	HasLocalChanges   bool
	ModifiedFiles     []string
	UntrackedFiles    []string
	DiffData          string
}

// detectVLLMCheckout analyzes the current working directory to see if it's a vLLM checkout
func detectVLLMCheckout() (*VLLMCheckoutInfo, error) {
	info := &VLLMCheckoutInfo{}

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return info, fmt.Errorf("failed to get current working directory: %w", err)
	}
	info.WorkingDir = cwd

	// Check if we're in a git repository
	if !isGitRepository(cwd) {
		return info, nil
	}

	// Check if this is a vLLM repository by looking for vLLM-specific files
	if !isVLLMRepository(cwd) {
		return info, nil
	}

	info.IsVLLMCheckout = true

	// Get git information
	if err := info.extractGitInfo(); err != nil {
		return info, fmt.Errorf("failed to extract git info: %w", err)
	}

	// Get merge base with upstream/main for image selection
	if err := info.findMergeBase(); err != nil {
		return info, fmt.Errorf("failed to find merge base: %w", err)
	}

	// Construct image reference
	info.ImageRef = fmt.Sprintf("public.ecr.aws/q9t5s3a7/vllm-ci-postmerge-repo:%s", info.MergeBaseCommit)

	// Check for local changes
	if err := info.detectLocalChanges(); err != nil {
		return info, fmt.Errorf("failed to detect local changes: %w", err)
	}

	return info, nil
}

// isGitRepository checks if the directory is a git repository
func isGitRepository(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	return err == nil && strings.TrimSpace(string(output)) == "true"
}

// isVLLMRepository checks if this is a vLLM repository by looking for vLLM-specific indicators
func isVLLMRepository(dir string) bool {
	// Check for vLLM-specific files/directories
	vllmIndicators := []string{
		"vllm",           // vllm package directory
		"setup.py",       // Python setup file
		"pyproject.toml", // Modern Python project file
	}

	for _, indicator := range vllmIndicators {
		if _, err := os.Stat(filepath.Join(dir, indicator)); err == nil {
			// Additional check: look for vLLM-specific content
			if indicator == "setup.py" {
				content, err := os.ReadFile(filepath.Join(dir, indicator))
				if err == nil && strings.Contains(string(content), "vllm") {
					return true
				}
			} else if indicator == "pyproject.toml" {
				content, err := os.ReadFile(filepath.Join(dir, indicator))
				if err == nil && strings.Contains(string(content), "vllm") {
					return true
				}
			} else if indicator == "vllm" {
				// Check if vllm directory contains __init__.py
				if _, err := os.Stat(filepath.Join(dir, "vllm", "__init__.py")); err == nil {
					return true
				}
			}
		}
	}

	return false
}

// extractGitInfo extracts git repository information
func (info *VLLMCheckoutInfo) extractGitInfo() error {
	// Get remote URL (prefer origin)
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = info.WorkingDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get origin remote URL: %w", err)
	}
	info.RemoteURL = strings.TrimSpace(string(output))

	// Get current branch
	cmd = exec.Command("git", "branch", "--show-current")
	cmd.Dir = info.WorkingDir
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}
	info.CurrentBranch = strings.TrimSpace(string(output))

	// Get current commit
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = info.WorkingDir
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get current commit: %w", err)
	}
	info.CurrentCommit = strings.TrimSpace(string(output))

	return nil
}

// findMergeBase finds the merge base with upstream/main for image selection
func (info *VLLMCheckoutInfo) findMergeBase() error {
	// First, try to fetch upstream to ensure we have latest refs
	cmd := exec.Command("git", "fetch", "upstream")
	cmd.Dir = info.WorkingDir
	cmd.Run() // Ignore errors - upstream might not exist or be accessible

	// Find merge base with upstream/main
	cmd = exec.Command("git", "merge-base", "upstream/main", "HEAD")
	cmd.Dir = info.WorkingDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback: try origin/main if upstream/main doesn't exist
		cmd = exec.Command("git", "merge-base", "origin/main", "HEAD")
		cmd.Dir = info.WorkingDir
		output, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to find merge base with upstream/main or origin/main: %w", err)
		}
	}
	info.MergeBaseCommit = strings.TrimSpace(string(output))

	return nil
}

// detectLocalChanges detects modified and untracked files
func (info *VLLMCheckoutInfo) detectLocalChanges() error {
	// Get modified files
	cmd := exec.Command("git", "diff", "--name-only")
	cmd.Dir = info.WorkingDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get modified files: %w", err)
	}
	if len(output) > 0 {
		info.ModifiedFiles = strings.Split(strings.TrimSpace(string(output)), "\n")
		info.HasLocalChanges = true
	}

	// Get untracked files
	cmd = exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = info.WorkingDir
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get untracked files: %w", err)
	}
	if len(output) > 0 {
		info.UntrackedFiles = strings.Split(strings.TrimSpace(string(output)), "\n")
		info.HasLocalChanges = true
	}

	// Generate diff data if there are changes
	if info.HasLocalChanges {
		if err := info.generateDiffData(); err != nil {
			return fmt.Errorf("failed to generate diff data: %w", err)
		}
	}

	return nil
}

// generateDiffData creates a comprehensive diff including modified and untracked files
func (info *VLLMCheckoutInfo) generateDiffData() error {
	var diffBuilder strings.Builder

	// Add git diff for modified files
	if len(info.ModifiedFiles) > 0 {
		cmd := exec.Command("git", "diff")
		cmd.Dir = info.WorkingDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to generate git diff: %w", err)
		}
		diffBuilder.WriteString("# Modified files diff\n")
		diffBuilder.Write(output)
		diffBuilder.WriteString("\n")
	}

	// Add untracked files content
	if len(info.UntrackedFiles) > 0 {
		diffBuilder.WriteString("# Untracked files\n")
		for _, file := range info.UntrackedFiles {
			filePath := filepath.Join(info.WorkingDir, file)
			content, err := os.ReadFile(filePath)
			if err != nil {
				// Skip files that can't be read (e.g., binary files, permission issues)
				continue
			}
			diffBuilder.WriteString(fmt.Sprintf("# New file: %s\n", file))
			diffBuilder.WriteString(string(content))
			diffBuilder.WriteString("\n\n")
		}
	}

	info.DiffData = diffBuilder.String()
	return nil
}

// getCacheRepoName generates a cache repository name from the remote URL and branch
func (info *VLLMCheckoutInfo) getCacheRepoName() string {
	// Extract repo name from URL
	url := info.RemoteURL

	// Handle both SSH and HTTPS URLs
	if strings.HasPrefix(url, "git@") {
		// SSH format: git@github.com:dougbtv/vllm.git
		parts := strings.Split(url, ":")
		if len(parts) >= 2 {
			repoPath := strings.TrimSuffix(parts[1], ".git")
			parts = strings.Split(repoPath, "/")
			if len(parts) >= 2 {
				return fmt.Sprintf("%s-vllm", parts[0])
			}
		}
	} else if strings.Contains(url, "github.com") {
		// HTTPS format: https://github.com/dougbtv/vllm.git
		parts := strings.Split(url, "/")
		if len(parts) >= 2 {
			repoOwner := parts[len(parts)-2]
			return fmt.Sprintf("%s-vllm", repoOwner)
		}
	}

	// Fallback
	return "local-vllm"
}

// Summary returns a human-readable summary of the checkout info
func (info *VLLMCheckoutInfo) Summary() string {
	if !info.IsVLLMCheckout {
		return "Not a vLLM checkout"
	}

	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("vLLM Checkout detected:\n"))
	summary.WriteString(fmt.Sprintf("  Directory: %s\n", info.WorkingDir))
	summary.WriteString(fmt.Sprintf("  Remote: %s\n", info.RemoteURL))
	summary.WriteString(fmt.Sprintf("  Branch: %s\n", info.CurrentBranch))
	summary.WriteString(fmt.Sprintf("  Commit: %s\n", info.CurrentCommit[:8]))
	summary.WriteString(fmt.Sprintf("  Merge base: %s\n", info.MergeBaseCommit[:8]))
	summary.WriteString(fmt.Sprintf("  Image: %s\n", info.ImageRef))
	summary.WriteString(fmt.Sprintf("  Cache repo name: %s\n", info.getCacheRepoName()))

	if info.HasLocalChanges {
		summary.WriteString(fmt.Sprintf("  Local changes: %d modified, %d untracked files\n",
			len(info.ModifiedFiles), len(info.UntrackedFiles)))
	} else {
		summary.WriteString("  Local changes: none\n")
	}

	return summary.String()
}

// ensureInCachePlan ensures that the vLLM checkout repo and image are in the cache plan
func (info *VLLMCheckoutInfo) ensureInCachePlan() error {
	if !info.IsVLLMCheckout {
		return fmt.Errorf("not a vLLM checkout")
	}

	// Check if repo is already in cache plan
	repoName := info.getCacheRepoName()
	repoExists, err := isRepoInCachePlan(repoName)
	if err != nil {
		return fmt.Errorf("failed to check if repo exists in cache plan: %w", err)
	}

	if !repoExists {
		fmt.Printf("📦 Adding vLLM checkout repo to cache plan: %s (branch: %s)\n", repoName, info.CurrentBranch)
		err := addGitRepoToCachePlan(info.RemoteURL, info.CurrentBranch, repoName)
		if err != nil {
			return fmt.Errorf("failed to add repo to cache plan: %w", err)
		}
		fmt.Printf("✅ Added repo %s to cache plan\n", repoName)
	} else {
		fmt.Printf("✅ Repo %s already in cache plan\n", repoName)
	}

	// Check if image is already in cache plan
	imageName := fmt.Sprintf("vllm-checkout-%s", info.MergeBaseCommit[:8])
	imageExists, err := isImageInCachePlan(imageName)
	if err != nil {
		return fmt.Errorf("failed to check if image exists in cache plan: %w", err)
	}

	if !imageExists {
		fmt.Printf("🏗️  Adding vLLM checkout image to cache plan: %s\n", imageName)
		err := addImageToCachePlan(info.ImageRef, imageName)
		if err != nil {
			return fmt.Errorf("failed to add image to cache plan: %w", err)
		}
		fmt.Printf("✅ Added image %s to cache plan\n", imageName)
	} else {
		fmt.Printf("✅ Image %s already in cache plan\n", imageName)
	}

	return nil
}

// isRepoInCachePlan checks if a git repository is already in the cache plan
func isRepoInCachePlan(repoName string) (bool, error) {
	ctx := context.Background()

	client, err := getDynamicClient()
	if err != nil {
		return false, fmt.Errorf("failed to create client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "cacheplans",
	}

	plan, err := client.Resource(gvr).Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to get cache plan: %w", err)
	}

	spec, found, err := unstructured.NestedMap(plan.Object, "spec")
	if err != nil || !found {
		return false, nil
	}

	items, found, err := unstructured.NestedSlice(spec, "items")
	if err != nil || !found {
		return false, nil
	}

	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		itemType, _ := itemMap["type"].(string)
		name, _ := itemMap["name"].(string)

		if itemType == "gitRepo" && name == repoName {
			return true, nil
		}
	}

	return false, nil
}

// isImageInCachePlan checks if an image is already in the cache plan
func isImageInCachePlan(imageName string) (bool, error) {
	ctx := context.Background()

	client, err := getDynamicClient()
	if err != nil {
		return false, fmt.Errorf("failed to create client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "cacheplans",
	}

	plan, err := client.Resource(gvr).Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to get cache plan: %w", err)
	}

	spec, found, err := unstructured.NestedMap(plan.Object, "spec")
	if err != nil || !found {
		return false, nil
	}

	items, found, err := unstructured.NestedSlice(spec, "items")
	if err != nil || !found {
		return false, nil
	}

	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		itemType, _ := itemMap["type"].(string)
		name, _ := itemMap["name"].(string)

		if itemType == "image" && name == imageName {
			return true, nil
		}
	}

	return false, nil
}

// createDiffConfigMap creates a ConfigMap containing the local diffs
func (info *VLLMCheckoutInfo) createDiffConfigMap(namespace, claimName string) error {
	if !info.HasLocalChanges {
		return nil // No diffs to ship
	}

	ctx := context.Background()

	client, err := getDynamicClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}

	configMapName := fmt.Sprintf("%s-vllm-diffs", claimName)

	// Create ConfigMap with diff data
	configMap := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      configMapName,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/name":     "k8shazgpu",
					"app.kubernetes.io/instance": claimName,
					"app.kubernetes.io/part-of":  "vllm-checkout",
				},
			},
			"data": map[string]interface{}{
				"diff.patch":       info.DiffData,
				"modified_files":   strings.Join(info.ModifiedFiles, "\n"),
				"untracked_files":  strings.Join(info.UntrackedFiles, "\n"),
				"checkout_info":    info.getMetadataJSON(),
			},
		},
	}

	// Create the ConfigMap
	_, err = client.Resource(gvr).Namespace(namespace).Create(ctx, configMap, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create diff ConfigMap: %w", err)
	}

	fmt.Printf("📤 Created ConfigMap %s with local diffs\n", configMapName)
	return nil
}

// getMetadataJSON returns checkout metadata as JSON string
func (info *VLLMCheckoutInfo) getMetadataJSON() string {
	metadata := map[string]interface{}{
		"remote_url":      info.RemoteURL,
		"current_branch":  info.CurrentBranch,
		"current_commit":  info.CurrentCommit,
		"merge_base":      info.MergeBaseCommit,
		"working_dir":     info.WorkingDir,
		"modified_files":  info.ModifiedFiles,
		"untracked_files": info.UntrackedFiles,
	}

	// Simple JSON serialization (could use encoding/json for more complex cases)
	var parts []string
	for k, v := range metadata {
		switch val := v.(type) {
		case string:
			parts = append(parts, fmt.Sprintf(`"%s": "%s"`, k, val))
		case []string:
			fileList := `["` + strings.Join(val, `", "`) + `"]`
			parts = append(parts, fmt.Sprintf(`"%s": %s`, k, fileList))
		}
	}

	return "{" + strings.Join(parts, ", ") + "}"
}

// getDiffConfigMapName returns the ConfigMap name for a given claim
func getDiffConfigMapName(claimName string) string {
	return fmt.Sprintf("%s-vllm-diffs", claimName)
}