package k8scli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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

		fmt.Printf("%-20s %-8s %-8s %-6s %-20s\n", "NODE", "IMAGES", "REPOS", "ERRORS", "LAST_UPDATE")
		fmt.Println("--------------------------------------------------------------------------------")

		for _, item := range list.Items {
			nodeName := getStringFromUnstructured(&item, "status", "nodeName")
			if nodeName == "" {
				nodeName = item.GetName()
			}

			images := getArrayFromUnstructured(&item, "status", "images")
			gitRepos := getArrayFromUnstructured(&item, "status", "gitRepos")
			errors := getArrayFromUnstructured(&item, "status", "errors")
			lastUpdate := getStringFromUnstructured(&item, "status", "lastUpdate")

			// Format last update time
			lastUpdateFormatted := "never"
			if lastUpdate != "" {
				if t, err := time.Parse(time.RFC3339, lastUpdate); err == nil {
					lastUpdateFormatted = t.Format("15:04:05")
				}
			}

			fmt.Printf("%-20s %-8d %-8d %-6d %-20s\n",
				truncateString(nodeName, 20),
				len(images),
				len(gitRepos),
				len(errors),
				lastUpdateFormatted)
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

var cacheAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add resources to cache plan",
}

func init() {
	// Cache command structure
	cacheCmd.AddCommand(cachePlanCmd)
	cacheCmd.AddCommand(cacheListCmd)
	cacheCmd.AddCommand(cacheAddCmd)

	// Plan subcommands
	cachePlanCmd.AddCommand(cachePlanShowCmd)

	// Add subcommands
	cacheAddCmd.AddCommand(cacheAddImageCmd)

	// Flags
	cacheAddImageCmd.Flags().String("name", "", "Name for the cache item (auto-generated if not provided)")

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

// Helper functions for extracting data from unstructured objects
func getStringFromUnstructured(obj *unstructured.Unstructured, fields ...string) string {
	val, found, err := unstructured.NestedString(obj.Object, fields...)
	if err != nil || !found {
		return ""
	}
	return val
}

func getArrayFromUnstructured(obj *unstructured.Unstructured, fields ...string) []interface{} {
	val, found, err := unstructured.NestedSlice(obj.Object, fields...)
	if err != nil || !found {
		return []interface{}{}
	}
	return val
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}