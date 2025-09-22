package k8scli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Manage resource cache",
	Long:  `Manage cached images and git repositories for faster workload startup.`,
}

var cachePlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Manage cache plans",
}

var cachePlanShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current cache plan",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		client, err := getDynamicClient()
		if err != nil {
			return fmt.Errorf("failed to create client: %w", err)
		}

		gvr := schema.GroupVersionResource{
			Group:    "canhazgpu.dev",
			Version:  "v1alpha1",
			Resource: "cacheplans",
		}

		plan, err := client.Resource(gvr).Get(ctx, "default", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get cache plan: %w", err)
		}

		// Pretty print the plan
		fmt.Printf("Cache Plan: %s\n", plan.GetName())
		fmt.Printf("Created: %s\n", plan.GetCreationTimestamp().Format("2006-01-02 15:04:05"))

		spec, found, err := unstructured.NestedMap(plan.Object, "spec")
		if err != nil || !found {
			fmt.Println("No cache items defined")
			return nil
		}

		items, found, err := unstructured.NestedSlice(spec, "items")
		if err != nil || !found {
			fmt.Println("No cache items defined")
			return nil
		}

		fmt.Printf("\nCache Items (%d):\n", len(items))
		fmt.Println("TYPE      NAME                                              REF/URL")
		fmt.Println("--------  ------------------------------------------------  --------------------------------------------------")

		for _, item := range items {
			itemMap := item.(map[string]interface{})
			itemType := getStringFromMap(itemMap, "type")
			name := getStringFromMap(itemMap, "name")

			var ref string
			if itemType == "image" {
				if img, ok := itemMap["image"].(map[string]interface{}); ok {
					ref = getStringFromMap(img, "ref")
				}
			} else if itemType == "gitRepo" {
				if repo, ok := itemMap["gitRepo"].(map[string]interface{}); ok {
					ref = getStringFromMap(repo, "url")
				}
			} else if itemType == "models" {
				if model, ok := itemMap["models"].(map[string]interface{}); ok {
					ref = getStringFromMap(model, "repoId")
				}
			}

			fmt.Printf("%-8s  %-48s  %-50s\n", itemType, truncateString(name, 48), truncateString(ref, 50))
		}
		return nil
	},
}

var cacheListCmd = &cobra.Command{
	Use:   "list",
	Short: "List cache status across nodes",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		client, err := getDynamicClient()
		if err != nil {
			return fmt.Errorf("failed to create client: %w", err)
		}

		gvr := schema.GroupVersionResource{
			Group:    "canhazgpu.dev",
			Version:  "v1alpha1",
			Resource: "nodecachestatuses",
		}

		list, err := client.Resource(gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list node cache statuses: %w", err)
		}

		if len(list.Items) == 0 {
			fmt.Println("No nodes with cache status found")
			return nil
		}

		fmt.Printf("%-20s %-8s %-8s %-8s %-6s %-20s\n", "NODE", "IMAGES", "REPOS", "MODELS", "ERRORS", "LAST_UPDATE")
		fmt.Println("---------------------------------------------------------------------------------------------")

		for _, item := range list.Items {
			nodeName := getStringFromUnstructured(&item, "status", "nodeName")
			if nodeName == "" {
				nodeName = item.GetName()
			}

			images := getArrayFromUnstructured(&item, "status", "images")
			gitRepos := getArrayFromUnstructured(&item, "status", "gitRepos")
			errors := getArrayFromUnstructured(&item, "status", "errors")
			lastUpdate := getStringFromUnstructured(&item, "status", "lastUpdate")

			// Separate git repos and models
			var actualGitRepos []interface{}
			var models []interface{}

			for _, repo := range gitRepos {
				repoMap, ok := repo.(map[string]interface{})
				if !ok {
					continue
				}

				// Check for unique fields to determine type
				if _, hasBranch := repoMap["branch"]; hasBranch {
					// This is a git repository (has branch field)
					actualGitRepos = append(actualGitRepos, repo)
				} else if _, hasRevision := repoMap["revision"]; hasRevision {
					// This is a model (has revision field)
					models = append(models, repo)
				} else {
					// Fallback: check ref content for backwards compatibility
					ref := getStringFromMap(repoMap, "ref")
					if strings.Contains(ref, "github.com") || strings.Contains(ref, "gitlab.com") || strings.Contains(ref, ".git") {
						actualGitRepos = append(actualGitRepos, repo)
					} else {
						models = append(models, repo)
					}
				}
			}

			// Format last update time
			lastUpdateFormatted := "never"
			if lastUpdate != "" {
				if t, err := time.Parse(time.RFC3339, lastUpdate); err == nil {
					lastUpdateFormatted = t.Format("15:04:05")
				}
			}

			fmt.Printf("%-20s %-8d %-8d %-8d %-6d %-20s\n",
				truncateString(nodeName, 20),
				len(images),
				len(actualGitRepos),
				len(models),
				len(errors),
				lastUpdateFormatted)
		}

		return nil
	},
}

var cacheStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show detailed cache status with individual image information",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		client, err := getDynamicClient()
		if err != nil {
			return fmt.Errorf("failed to create client: %w", err)
		}

		gvr := schema.GroupVersionResource{
			Group:    "canhazgpu.dev",
			Version:  "v1alpha1",
			Resource: "nodecachestatuses",
		}

		list, err := client.Resource(gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list node cache statuses: %w", err)
		}

		if len(list.Items) == 0 {
			fmt.Println("No nodes with cache status found")
			return nil
		}

		for _, item := range list.Items {
			nodeName := getStringFromUnstructured(&item, "status", "nodeName")
			if nodeName == "" {
				nodeName = item.GetName()
			}

			images := getArrayFromUnstructured(&item, "status", "images")
			gitRepos := getArrayFromUnstructured(&item, "status", "gitRepos")
			lastUpdate := getStringFromUnstructured(&item, "status", "lastUpdate")

			fmt.Printf("\n=== Node: %s ===\n", nodeName)
			fmt.Printf("Last Update: %s\n", lastUpdate)
			fmt.Printf("Images (%d):\n", len(images))

			if len(images) == 0 {
				fmt.Println("  No images")
			} else {
				fmt.Printf("%-8s %-50s %-10s %s\n", "STATUS", "IMAGE", "PRESENT", "MESSAGE")
				fmt.Println("-------------------------------------------------------------------------------------")

				for _, img := range images {
					imgMap, ok := img.(map[string]interface{})
					if !ok {
						continue
					}

					ref := getStringFromMap(imgMap, "ref")
					status := getStringFromMap(imgMap, "status")
					present := getBoolFromMap(imgMap, "present")
					message := getStringFromMap(imgMap, "message")

					presentStr := "No"
					if present {
						presentStr = "Yes"
					}

					// Add status icon
					statusIcon := ""
					switch status {
					case "pulling":
						statusIcon = "🔄"
					case "ready":
						statusIcon = "✅"
					case "failed":
						statusIcon = "❌"
					default:
						statusIcon = "❓"
					}

					fmt.Printf("%-8s %-50s %-10s %s\n",
						statusIcon+" "+status,
						truncateString(ref, 48),
						presentStr,
						truncateString(message, 40))
				}
			}

			// Separate git repos and models
			var actualGitRepos []interface{}
			var models []interface{}

			for _, repo := range gitRepos {
				repoMap, ok := repo.(map[string]interface{})
				if !ok {
					continue
				}

				// Check for unique fields to determine type
				if _, hasBranch := repoMap["branch"]; hasBranch {
					// This is a git repository (has branch field)
					actualGitRepos = append(actualGitRepos, repo)
				} else if _, hasRevision := repoMap["revision"]; hasRevision {
					// This is a model (has revision field)
					models = append(models, repo)
				} else {
					// Fallback: check ref content for backwards compatibility
					ref := getStringFromMap(repoMap, "ref")
					if strings.Contains(ref, "github.com") || strings.Contains(ref, "gitlab.com") || strings.Contains(ref, ".git") {
						actualGitRepos = append(actualGitRepos, repo)
					} else {
						models = append(models, repo)
					}
				}
			}

			// Git Repositories section
			fmt.Printf("\nGit Repositories (%d):\n", len(actualGitRepos))

			if len(actualGitRepos) == 0 {
				fmt.Println("  No git repositories")
			} else {
				fmt.Printf("%-8s %-40s %-10s %-10s %s\n", "STATUS", "REPOSITORY", "BRANCH", "PRESENT", "MESSAGE")
				fmt.Println("-------------------------------------------------------------------------------------")

				for _, repo := range actualGitRepos {
					repoMap, ok := repo.(map[string]interface{})
					if !ok {
						continue
					}

					ref := getStringFromMap(repoMap, "ref")
					status := getStringFromMap(repoMap, "status")
					branch := getStringFromMap(repoMap, "branch")
					present := getBoolFromMap(repoMap, "present")
					message := getStringFromMap(repoMap, "message")

					presentStr := "No"
					if present {
						presentStr = "Yes"
					}

					// Add status icon
					statusIcon := ""
					switch status {
					case "pulling":
						statusIcon = "🔄"
					case "ready":
						statusIcon = "✅"
					case "failed":
						statusIcon = "❌"
					default:
						statusIcon = "❓"
					}

					if branch == "" {
						branch = "main"
					}

					fmt.Printf("%-8s %-40s %-10s %-10s %s\n",
						statusIcon+" "+status,
						truncateString(ref, 38),
						truncateString(branch, 8),
						presentStr,
						truncateString(message, 30))
				}
			}

			// Models section
			fmt.Printf("\nModels (%d):\n", len(models))

			if len(models) == 0 {
				fmt.Println("  No models")
			} else {
				fmt.Printf("%-8s %-40s %-10s %-10s %s\n", "STATUS", "MODEL", "REVISION", "PRESENT", "MESSAGE")
				fmt.Println("-------------------------------------------------------------------------------------")

				for _, model := range models {
					modelMap, ok := model.(map[string]interface{})
					if !ok {
						continue
					}

					ref := getStringFromMap(modelMap, "ref")
					status := getStringFromMap(modelMap, "status")
					revision := getStringFromMap(modelMap, "revision")
					present := getBoolFromMap(modelMap, "present")
					message := getStringFromMap(modelMap, "message")

					presentStr := "No"
					if present {
						presentStr = "Yes"
					}

					// Add status icon
					statusIcon := ""
					switch status {
					case "pulling":
						statusIcon = "🔄"
					case "ready":
						statusIcon = "✅"
					case "failed":
						statusIcon = "❌"
					default:
						statusIcon = "❓"
					}

					if revision == "" {
						revision = "main"
					}

					fmt.Printf("%-8s %-40s %-10s %-10s %s\n",
						statusIcon+" "+status,
						truncateString(ref, 38),
						truncateString(revision, 8),
						presentStr,
						truncateString(message, 30))
				}
			}
		}

		return nil
	},
}

var cacheAddImageCmd = &cobra.Command{
	Use:   "image <ref>",
	Short: "Add image to cache plan",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		imageRef := args[0]
		name, _ := cmd.Flags().GetString("name")
		if name == "" {
			// Generate name from image ref
			name = generateImageName(imageRef)
		}

		return addImageToCachePlan(imageRef, name)
	},
}

var cacheAddGitRepoCmd = &cobra.Command{
	Use:   "gitrepo <url>",
	Short: "Add git repository to cache plan",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		gitURL := args[0]
		name, _ := cmd.Flags().GetString("name")
		branch, _ := cmd.Flags().GetString("branch")

		if name == "" {
			// Generate name from git URL
			name = generateGitRepoName(gitURL)
		}

		if branch == "" {
			branch = "main" // default branch
		}

		return addGitRepoToCachePlan(gitURL, branch, name)
	},
}

var cacheAddModelCmd = &cobra.Command{
	Use:   "model <repo-id>",
	Short: "Add Hugging Face model to cache plan",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoId := args[0]
		name, _ := cmd.Flags().GetString("name")
		revision, _ := cmd.Flags().GetString("revision")

		if name == "" {
			// Generate name from repo ID
			name = generateModelName(repoId)
		}

		if revision == "" {
			revision = "main" // default revision
		}

		return addModelToCachePlan(repoId, revision, name)
	},
}

var cacheAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add resources to cache plan",
}

var cacheUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update cached resources to latest versions",
	Long:  `Update cached resources to pull latest commits for git repositories and latest image tags.`,
}

var cacheUpdateGitRepoCmd = &cobra.Command{
	Use:   "gitrepo <name>",
	Short: "Update git repository cache to latest commits",
	Long: `Update a cached git repository to pull the latest commits from the remote branch.
This handles both new commits and force pushes by performing a git fetch and reset.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoName := args[0]
		force, _ := cmd.Flags().GetBool("force")

		fmt.Printf("Updating git repository cache: %s\n", repoName)
		if force {
			fmt.Println("  Force update enabled - will handle force pushes")
		}

		return updateGitRepoCache(repoName, force)
	},
}

var cacheUpdateAllCmd = &cobra.Command{
	Use:   "all",
	Short: "Update all cached resources",
	Long:  `Update all cached git repositories and images to their latest versions.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")

		fmt.Println("Updating all cached resources...")
		if force {
			fmt.Println("  Force update enabled - will handle force pushes")
		}

		return updateAllCachedResources(force)
	},
}

var cacheRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove resources from cache plan",
}

var cacheRemoveImageCmd = &cobra.Command{
	Use:   "image <ref>",
	Short: "Remove image from cache plan",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		imageRef := args[0]
		name, _ := cmd.Flags().GetString("name")

		if name == "" {
			// Generate name from image ref
			name = generateImageName(imageRef)
		}

		return removeImageFromCachePlan(imageRef, name)
	},
}

var cacheRemoveModelCmd = &cobra.Command{
	Use:   "model <repo-id>",
	Short: "Remove model from cache plan",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoId := args[0]
		name, _ := cmd.Flags().GetString("name")

		if name == "" {
			// Generate name from repo ID
			name = generateModelName(repoId)
		}

		return removeModelFromCachePlan(repoId, name)
	},
}

func init() {
	// Cache command structure
	cacheCmd.AddCommand(cachePlanCmd)
	cacheCmd.AddCommand(cacheListCmd)
	cacheCmd.AddCommand(cacheStatusCmd)
	cacheCmd.AddCommand(cacheAddCmd)
	cacheCmd.AddCommand(cacheUpdateCmd)
	cacheCmd.AddCommand(cacheRemoveCmd)

	// Plan subcommands
	cachePlanCmd.AddCommand(cachePlanShowCmd)

	// Add subcommands
	cacheAddCmd.AddCommand(cacheAddImageCmd)
	cacheAddCmd.AddCommand(cacheAddGitRepoCmd)
	cacheAddCmd.AddCommand(cacheAddModelCmd)

	// Update subcommands
	cacheUpdateCmd.AddCommand(cacheUpdateGitRepoCmd)
	cacheUpdateCmd.AddCommand(cacheUpdateAllCmd)

	// Remove subcommands
	cacheRemoveCmd.AddCommand(cacheRemoveImageCmd)
	cacheRemoveCmd.AddCommand(cacheRemoveModelCmd)

	// Flags
	cacheAddImageCmd.Flags().String("name", "", "Name for the cache item (auto-generated if not provided)")
	cacheAddGitRepoCmd.Flags().String("name", "", "Name for the cache item (auto-generated if not provided)")
	cacheAddGitRepoCmd.Flags().String("branch", "", "Git branch to clone (default: main)")
	cacheAddModelCmd.Flags().String("name", "", "Name for the cache item (auto-generated if not provided)")
	cacheAddModelCmd.Flags().String("revision", "", "Model revision to download (default: main)")

	// Update flags
	cacheUpdateGitRepoCmd.Flags().Bool("force", false, "Force update even with force pushes (git reset --hard)")
	cacheUpdateAllCmd.Flags().Bool("force", false, "Force update all repos even with force pushes")

	cacheRemoveImageCmd.Flags().String("name", "", "Name for the cache item (auto-generated if not provided)")
	cacheRemoveModelCmd.Flags().String("name", "", "Name for the cache item (auto-generated if not provided)")

	rootCmd.AddCommand(cacheCmd)
}

func getDynamicClient() (dynamic.Interface, error) {
	config, err := clientcmd.BuildConfigFromFlags("", viper.GetString("kubeconfig"))
	if err != nil {
		return nil, err
	}

	return dynamic.NewForConfig(config)
}

func generateImageName(ref string) string {
	// Simple name generation - replace special chars with dashes
	name := ref
	for _, char := range []string{":", "/", ".", "@"} {
		name = replaceAll(name, char, "-")
	}
	return name
}

func replaceAll(s, old, new string) string {
	result := ""
	for i := 0; i < len(s); i++ {
		if i <= len(s)-len(old) && s[i:i+len(old)] == old {
			result += new
			i += len(old) - 1
		} else {
			result += string(s[i])
		}
	}
	return result
}

func addImageToCachePlan(imageRef, name string) error {
	ctx := context.Background()

	client, err := getDynamicClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "cacheplans",
	}

	// Try to get existing plan
	plan, err := client.Resource(gvr).Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		// Create new plan if not exists
		plan = &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "canhazgpu.dev/v1alpha1",
				"kind":       "CachePlan",
				"metadata": map[string]interface{}{
					"name": "default",
				},
				"spec": map[string]interface{}{
					"items": []interface{}{},
				},
			},
		}
	}

	// Add image item
	spec, found, err := unstructured.NestedMap(plan.Object, "spec")
	if err != nil || !found {
		spec = make(map[string]interface{})
	}

	items, found, err := unstructured.NestedSlice(spec, "items")
	if err != nil || !found {
		items = []interface{}{}
	}

	newItem := map[string]interface{}{
		"type":  "image",
		"name":  name,
		"scope": "allNodes",
		"image": map[string]interface{}{
			"ref": imageRef,
		},
	}

	items = append(items, newItem)
	spec["items"] = items
	plan.Object["spec"] = spec

	// Create or update
	if plan.GetResourceVersion() == "" {
		_, err = client.Resource(gvr).Create(ctx, plan, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create cache plan: %w", err)
		}
		fmt.Printf("✓ Created cache plan with image %s\n", imageRef)
	} else {
		_, err = client.Resource(gvr).Update(ctx, plan, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update cache plan: %w", err)
		}
		fmt.Printf("✓ Added image %s to cache plan\n", imageRef)
	}

	return nil
}

func removeImageFromCachePlan(imageRef, name string) error {
	ctx := context.Background()

	client, err := getDynamicClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "cacheplans",
	}

	// Try to get existing plan
	plan, err := client.Resource(gvr).Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("cache plan not found: %w", err)
	}

	// Get items
	spec, found, err := unstructured.NestedMap(plan.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("cache plan has no spec")
	}

	items, found, err := unstructured.NestedSlice(spec, "items")
	if err != nil || !found {
		return fmt.Errorf("cache plan has no items")
	}

	// Find and remove the item
	var newItems []interface{}
	removed := false

	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		// Check if this is the item to remove (by name or by image ref)
		itemName, _ := itemMap["name"].(string)
		itemType, _ := itemMap["type"].(string)

		if itemType == "image" {
			if imageData, ok := itemMap["image"].(map[string]interface{}); ok {
				if itemRef, ok := imageData["ref"].(string); ok {
					// Remove if name matches or if ref matches
					if itemName == name || itemRef == imageRef {
						fmt.Printf("✓ Removing image %s from cache plan\n", itemRef)
						removed = true
						continue
					}
				}
			}
		}

		// Keep this item
		newItems = append(newItems, item)
	}

	if !removed {
		return fmt.Errorf("image %s not found in cache plan", imageRef)
	}

	// Update the plan
	spec["items"] = newItems
	plan.Object["spec"] = spec

	_, err = client.Resource(gvr).Update(ctx, plan, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update cache plan: %w", err)
	}

	fmt.Printf("✓ Updated cache plan\n")
	return nil
}

func removeModelFromCachePlan(repoId, name string) error {
	ctx := context.Background()

	client, err := getDynamicClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "cacheplans",
	}

	// Try to get existing plan
	plan, err := client.Resource(gvr).Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("cache plan not found: %w", err)
	}

	// Get items
	spec, found, err := unstructured.NestedMap(plan.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("cache plan has no spec")
	}

	items, found, err := unstructured.NestedSlice(spec, "items")
	if err != nil || !found {
		return fmt.Errorf("cache plan has no items")
	}

	// Find and remove the item
	var newItems []interface{}
	removed := false

	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		// Check if this is the item to remove (by name or by model repo ID)
		itemName, _ := itemMap["name"].(string)
		itemType, _ := itemMap["type"].(string)

		if itemType == "models" {
			if modelData, ok := itemMap["models"].(map[string]interface{}); ok {
				if itemRepoId, ok := modelData["repoId"].(string); ok {
					// Remove if name matches or if repo ID matches
					if itemName == name || itemRepoId == repoId {
						fmt.Printf("✓ Removing model %s from cache plan\n", itemRepoId)
						removed = true
						continue
					}
				}
			}
		}

		// Keep this item
		newItems = append(newItems, item)
	}

	if !removed {
		return fmt.Errorf("model %s not found in cache plan", repoId)
	}

	// Update the plan
	spec["items"] = newItems
	plan.Object["spec"] = spec

	_, err = client.Resource(gvr).Update(ctx, plan, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update cache plan: %w", err)
	}

	fmt.Printf("✓ Updated cache plan\n")
	return nil
}

// Helper functions for extracting data from unstructured objects
func getStringFromUnstructured(obj *unstructured.Unstructured, fields ...string) string {
	val, found, err := unstructured.NestedString(obj.Object, fields...)
	if err != nil || !found {
		return ""
	}
	return val
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func getBoolFromMap(m map[string]interface{}, key string) bool {
	if val, ok := m[key].(bool); ok {
		return val
	}
	return false
}

func getArrayFromUnstructured(obj *unstructured.Unstructured, fields ...string) []interface{} {
	val, found, err := unstructured.NestedSlice(obj.Object, fields...)
	if err != nil || !found {
		return []interface{}{}
	}
	return val
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func generateGitRepoName(gitURL string) string {
	// Extract repo name from URL
	// e.g., https://github.com/vllm-project/vllm.git -> vllm-project-vllm
	gitURL = strings.TrimSuffix(gitURL, ".git")
	parts := strings.Split(gitURL, "/")
	if len(parts) >= 2 {
		// Get last two parts (org/repo)
		return strings.ReplaceAll(fmt.Sprintf("%s-%s", parts[len(parts)-2], parts[len(parts)-1]), "/", "-")
	}
	// Fallback to full URL conversion
	return strings.ReplaceAll(strings.ReplaceAll(gitURL, "/", "-"), ":", "-")
}

func generateModelName(repoId string) string {
	// Extract model name from repo ID
	// e.g., facebook/opt-125m -> facebook-opt-125m
	return strings.ReplaceAll(repoId, "/", "-")
}

func addModelToCachePlan(repoId, revision, name string) error {
	ctx := context.Background()

	client, err := getDynamicClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "cacheplans",
	}

	// Try to get existing plan
	plan, err := client.Resource(gvr).Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get cache plan: %w", err)
		}
		// Create new plan
		plan = &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "canhazgpu.dev/v1alpha1",
				"kind":       "CachePlan",
				"metadata": map[string]interface{}{
					"name": "default",
				},
				"spec": map[string]interface{}{
					"items": []interface{}{},
				},
			},
		}
	}

	// Get current items
	spec, found, err := unstructured.NestedMap(plan.Object, "spec")
	if !found || err != nil {
		spec = map[string]interface{}{}
	}

	items, found, err := unstructured.NestedSlice(spec, "items")
	if err != nil || !found {
		items = []interface{}{}
	}

	newItem := map[string]interface{}{
		"type":  "models",
		"name":  name,
		"scope": "allNodes",
		"models": map[string]interface{}{
			"repoId":   repoId,
			"revision": revision,
		},
	}

	items = append(items, newItem)
	spec["items"] = items
	plan.Object["spec"] = spec

	if len(plan.Object) == 3 { // Only has apiVersion, kind, metadata
		// Create new plan
		_, err = client.Resource(gvr).Create(ctx, plan, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create cache plan: %w", err)
		}
		fmt.Printf("✓ Created cache plan and added model %s (revision: %s)\n", repoId, revision)
	} else {
		// Update existing plan
		_, err = client.Resource(gvr).Update(ctx, plan, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update cache plan: %w", err)
		}
		fmt.Printf("✓ Added model %s (revision: %s) to cache plan\n", repoId, revision)
	}

	return nil
}

func addGitRepoToCachePlan(gitURL, branch, name string) error {
	ctx := context.Background()

	client, err := getDynamicClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "cacheplans",
	}

	// Try to get existing plan
	plan, err := client.Resource(gvr).Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		// Create new plan if not exists
		plan = &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "canhazgpu.dev/v1alpha1",
				"kind":       "CachePlan",
				"metadata": map[string]interface{}{
					"name": "default",
				},
				"spec": map[string]interface{}{
					"items": []interface{}{},
				},
			},
		}
	}

	// Add git repo item
	spec, found, err := unstructured.NestedMap(plan.Object, "spec")
	if err != nil || !found {
		spec = make(map[string]interface{})
	}

	items, found, err := unstructured.NestedSlice(spec, "items")
	if err != nil || !found {
		items = []interface{}{}
	}

	newItem := map[string]interface{}{
		"type":  "gitRepo",
		"name":  name,
		"scope": "allNodes",
		"gitRepo": map[string]interface{}{
			"url":      gitURL,
			"branch":   branch,
			"pathName": name, // Use the generated name as the path
		},
	}

	items = append(items, newItem)
	spec["items"] = items
	plan.Object["spec"] = spec

	// Create or update
	if plan.GetResourceVersion() == "" {
		_, err = client.Resource(gvr).Create(ctx, plan, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create cache plan: %w", err)
		}
		fmt.Printf("✓ Created cache plan with git repo %s\n", gitURL)
	} else {
		_, err = client.Resource(gvr).Update(ctx, plan, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update cache plan: %w", err)
		}
		fmt.Printf("✓ Added git repo %s (branch: %s) to cache plan\n", gitURL, branch)
	}

	return nil
}

func updateGitRepoCache(repoName string, force bool) error {
	ctx := context.Background()

	// Create a Kubernetes client to interact with NodeCacheStatus resources
	client, err := getDynamicClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Get the cache plan to validate that this repo exists
	cachePlanGVR := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "cacheplans",
	}

	plan, err := client.Resource(cachePlanGVR).Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get cache plan: %w", err)
	}

	// Find the git repo in the plan
	spec, found, err := unstructured.NestedMap(plan.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("cache plan has no spec")
	}

	items, found, err := unstructured.NestedSlice(spec, "items")
	if err != nil || !found {
		return fmt.Errorf("cache plan has no items")
	}

	var gitRepo map[string]interface{}
	var repoURL, branch string

	// Find the specific git repository
	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		itemType, _ := itemMap["type"].(string)
		name, _ := itemMap["name"].(string)

		if itemType == "gitRepo" && name == repoName {
			if gitData, ok := itemMap["gitRepo"].(map[string]interface{}); ok {
				repoURL, _ = gitData["url"].(string)
				branch, _ = gitData["branch"].(string)
				gitRepo = gitData
				break
			}
		}
	}

	if gitRepo == nil {
		return fmt.Errorf("git repository '%s' not found in cache plan", repoName)
	}

	fmt.Printf("Found git repository: %s (branch: %s)\n", repoURL, branch)

	// Now trigger an update by adding an annotation to force refresh
	// We'll add a timestamp annotation to the cache plan to trigger the node agents to update
	annotations, found, err := unstructured.NestedStringMap(plan.Object, "metadata", "annotations")
	if err != nil || !found {
		annotations = make(map[string]string)
	}

	updateKey := fmt.Sprintf("canhazgpu.dev/update-repo-%s", repoName)
	forceKey := fmt.Sprintf("canhazgpu.dev/force-update-%s", repoName)

	annotations[updateKey] = fmt.Sprintf("%d", time.Now().Unix())
	if force {
		annotations[forceKey] = "true"
		fmt.Printf("  ⚠️  Force update enabled - will reset to remote HEAD\n")
	}

	unstructured.SetNestedStringMap(plan.Object, annotations, "metadata", "annotations")

	// Update the cache plan with the new annotation
	_, err = client.Resource(cachePlanGVR).Update(ctx, plan, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update cache plan with refresh trigger: %w", err)
	}

	fmt.Printf("✓ Triggered update for git repository: %s\n", repoName)
	fmt.Printf("   Nodes will pull latest commits from branch: %s\n", branch)
	if force {
		fmt.Printf("   Force update will handle any force pushes\n")
	}
	fmt.Printf("\n💡 Monitor update progress with: k8shazgpu cache status\n")

	return nil
}

func updateAllCachedResources(force bool) error {
	ctx := context.Background()

	client, err := getDynamicClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Get the cache plan
	gvr := schema.GroupVersionResource{
		Group:    "canhazgpu.dev",
		Version:  "v1alpha1",
		Resource: "cacheplans",
	}

	plan, err := client.Resource(gvr).Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get cache plan: %w", err)
	}

	// Get all git repositories from the plan
	spec, found, err := unstructured.NestedMap(plan.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("cache plan has no spec")
	}

	items, found, err := unstructured.NestedSlice(spec, "items")
	if err != nil || !found {
		return fmt.Errorf("cache plan has no items")
	}

	var gitRepos []string

	// Collect all git repositories
	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		itemType, _ := itemMap["type"].(string)
		name, _ := itemMap["name"].(string)

		if itemType == "gitRepo" {
			gitRepos = append(gitRepos, name)
		}
	}

	if len(gitRepos) == 0 {
		fmt.Println("No git repositories found in cache plan")
		return nil
	}

	fmt.Printf("Found %d git repositories to update:\n", len(gitRepos))
	for _, repo := range gitRepos {
		fmt.Printf("  - %s\n", repo)
	}
	fmt.Println()

	// Update each repository
	for _, repoName := range gitRepos {
		fmt.Printf("Updating %s...\n", repoName)
		err := updateGitRepoCache(repoName, force)
		if err != nil {
			fmt.Printf("  ❌ Failed to update %s: %v\n", repoName, err)
		} else {
			fmt.Printf("  ✓ Triggered update for %s\n", repoName)
		}
	}

	fmt.Printf("\n✓ Triggered updates for all %d git repositories\n", len(gitRepos))
	fmt.Printf("💡 Monitor update progress with: k8shazgpu cache status\n")

	return nil
}