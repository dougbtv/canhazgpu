package k8scli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/russellb/canhazgpu/pkg/k8s"
)

var vllmCmd = &cobra.Command{
	Use:   "vllm",
	Short: "vLLM-specific operations",
	Long:  `Commands for working with vLLM workloads using cached images and repositories.`,
}

var vllmRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a vLLM workload with cached resources",
	Long: `Run a vLLM workload using cached images and git repositories.
The Pod will have access to the cached git repository at /workdir and model cache at /models.`,
	Example: `  # Run with defaults (sleep 300)
  k8shazgpu vllm run --name vllm-demo

  # Run with custom command
  k8shazgpu vllm run --name vllm-demo -- /bin/sh -c 'cd /workdir && python examples/offline_inference.py'

  # Run with specific cached resources
  k8shazgpu vllm run --name vllm-demo --image-name vllm-pinned --repo-name dougbtv-vllm --gpus 2`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		client, err := k8s.NewClient(viper.GetString("kubeContext"), namespace)
		if err != nil {
			return fmt.Errorf("failed to create Kubernetes client: %w", err)
		}

		// Get flags
		claimName, err := cmd.Flags().GetString("name")
		if err != nil {
			return err
		}

		imageName, err := cmd.Flags().GetString("image-name")
		if err != nil {
			return err
		}

		repoName, err := cmd.Flags().GetString("repo-name")
		if err != nil {
			return err
		}

		port, err := cmd.Flags().GetInt("port")
		if err != nil {
			return err
		}

		skipCacheCheck, err := cmd.Flags().GetBool("skip-cache-check")
		if err != nil {
			return err
		}

		// Generate name if not provided
		if claimName == "" {
			claimName = fmt.Sprintf("k8shazgpu-vllm-%d", generateRandomSuffix())
		}

		// Default command if none provided
		cmdArgs := args
		if len(cmdArgs) == 0 {
			cmdArgs = []string{"/bin/sh", "-c", "sleep 300"}
		}

		req := &k8s.ReservationRequest{
			Name:       claimName,
			GPUCount:   gpus,
			GPUIDs:     gpuIDs,
			PreferNode: preferNode,
			Port:       port,  // Add port mapping
		}

		// Detect vLLM checkout and auto-configure
		vllmInfo, err := detectVLLMCheckout()
		if err != nil {
			return fmt.Errorf("failed to detect vLLM checkout: %w", err)
		}

		if vllmInfo.IsVLLMCheckout {
			fmt.Printf("🔍 vLLM checkout detected!\n")
			fmt.Print(vllmInfo.Summary())

			// Override defaults with checkout-derived values if not explicitly set
			if imageName == "vllm-pinned" {
				// User didn't override, use checkout-derived image
				checkoutImageName := fmt.Sprintf("vllm-checkout-%s", vllmInfo.MergeBaseCommit[:8])
				fmt.Printf("📸 Using checkout-derived image: %s\n", checkoutImageName)
				imageName = checkoutImageName
			}

			if repoName == "dougbtv-vllm" {
				// User didn't override, use checkout-derived repo
				checkoutRepoName := vllmInfo.getCacheRepoName()
				fmt.Printf("📂 Using checkout-derived repo: %s\n", checkoutRepoName)
				repoName = checkoutRepoName
			}

			// Ensure checkout repo and image are in cache plan
			if err := vllmInfo.ensureInCachePlan(); err != nil {
				return fmt.Errorf("failed to ensure checkout items in cache plan: %w", err)
			}

			// Create diff ConfigMap if there are local changes
			if vllmInfo.HasLocalChanges {
				fmt.Printf("📦 Packaging %d modified and %d untracked files for transport\n",
					len(vllmInfo.ModifiedFiles), len(vllmInfo.UntrackedFiles))
				if err := vllmInfo.createDiffConfigMap(namespace, claimName); err != nil {
					return fmt.Errorf("failed to create diff ConfigMap: %w", err)
				}
			}
		}

		// Cache validation and warming (unless skipped)
		if !skipCacheCheck {
			fmt.Printf("🔍 Checking cache status for required items...\n")

			// First check current cache status
			err := validateCacheItems(imageName, repoName)
			if err != nil {
				fmt.Printf("⚠️  Cache validation failed: %v\n", err)
				fmt.Printf("🔄 Triggering cache updates to ensure items are ready...\n")

				// Trigger updates for missing/not ready items
				if err := triggerCacheUpdates(imageName, repoName); err != nil {
					return fmt.Errorf("failed to trigger cache updates: %w", err)
				}

				// Wait for cache items to be ready
				fmt.Printf("⏳ Waiting for cache items to be ready on all nodes (timeout: 5 minutes)...\n")
				if err := waitForCacheReady(imageName, repoName, 5*time.Minute); err != nil {
					return fmt.Errorf("cache warming failed: %w", err)
				}
			} else {
				fmt.Printf("✅ All cache items are ready on all nodes\n")
			}
		} else {
			fmt.Printf("⚠️  Skipping cache validation (--skip-cache-check enabled)\n")
		}

		fmt.Printf("Creating ResourceClaim %s for vLLM workload requesting %d GPU(s)...\n", claimName, gpus)

		// Include diff ConfigMap annotation if we have local changes
		var diffConfigMapName string
		if vllmInfo.IsVLLMCheckout && vllmInfo.HasLocalChanges {
			diffConfigMapName = getDiffConfigMapName(claimName)
			fmt.Printf("📋 Will annotate ResourceClaim with diff ConfigMap: %s\n", diffConfigMapName)
		}

		// Create ResourceClaim with vLLM-specific annotations
		claim, err := client.CreateResourceClaimWithVLLMAnnotations(ctx, req, imageName, repoName, cmdArgs, diffConfigMapName)
		if err != nil {
			return fmt.Errorf("failed to create ResourceClaim: %w", err)
		}

		fmt.Printf("Waiting for allocation of claim %s...\n", claim.Name)

		// Wait for allocation with periodic status updates
		runCtx := &runCommandContext{}
		allocated, err := runCtx.waitForAllocationWithStatusUpdates(ctx, client, claim.Name, claimName)
		if err != nil {
			return fmt.Errorf("failed waiting for allocation: %w", err)
		}

		// If allocated is nil, it means the request was queued and we're exiting early
		if allocated == nil {
			return nil
		}

		fmt.Printf("✓ Allocated %d GPU(s) on node %s\n", len(allocated.AllocatedGPUs), allocated.NodeName)
		fmt.Printf("✓ Controller will create vLLM Pod with cached resources\n")

		// Wait a moment for Pod creation
		time.Sleep(2 * time.Second)

		// Show Pod status
		podName := claimName + "-vllm-pod"
		fmt.Printf("Waiting for Pod %s to start...\n", podName)

		if err := client.WaitForPodRunning(ctx, podName); err != nil {
			fmt.Printf("Warning: Pod may still be starting: %v\n", err)
		} else {
			fmt.Printf("✓ Pod %s is running\n", podName)
			if port > 0 {
				fmt.Printf("✓ vLLM API server will be available on port %d\n", port)
			}
		}

		// Stream logs if requested
		follow, _ := cmd.Flags().GetBool("follow")
		if follow {
			fmt.Printf("Streaming logs from Pod %s:\n", podName)
			fmt.Println(strings.Repeat("-", 50))
			return client.StreamPodLogs(ctx, podName)
		} else {
			fmt.Printf("\nTo exec into the Pod: kubectl exec -it %s -n %s -- /bin/bash\n", podName, namespace)
			fmt.Printf("To view logs: kubectl logs %s -n %s\n", podName, namespace)
			fmt.Printf("To cleanup: k8shazgpu cleanup --name %s\n", claimName)
			if port > 0 {
				fmt.Printf("To access vLLM API: http://localhost:%d (after setting up port-forward)\n", port)
				fmt.Printf("To set up port-forward: kubectl port-forward %s %d:%d -n %s\n", podName, port, port, namespace)
			}
			fmt.Printf("\nPod mounts:\n")
			fmt.Printf("  /workdir - Git repository (%s)\n", repoName)
			fmt.Printf("  /models  - Model cache directory\n")
		}

		return nil
	},
}

func init() {
	vllmRunCmd.Flags().IntVar(&gpus, "gpus", 1, "Number of GPUs to reserve")
	vllmRunCmd.Flags().StringSliceVar(&gpuIDs, "gpu-ids", []string{}, "Specific GPU IDs to request (comma-separated)")
	vllmRunCmd.Flags().StringVar(&preferNode, "prefer-node", "", "Preferred node name for GPU allocation")
	vllmRunCmd.Flags().String("name", "", "Name for the reservation (auto-generated if not provided)")
	vllmRunCmd.Flags().String("image-name", "vllm-pinned", "Name of cached image to use")
	vllmRunCmd.Flags().String("repo-name", "dougbtv-vllm", "Name of cached git repository to use")
	vllmRunCmd.Flags().Bool("follow", false, "Follow Pod logs after creation")
	vllmRunCmd.Flags().Int("port", 8000, "Port to expose for vLLM API server (0 to disable port mapping)")
	vllmRunCmd.Flags().Bool("skip-cache-check", false, "Skip cache validation and warming before GPU allocation")

	vllmCmd.AddCommand(vllmRunCmd)
}