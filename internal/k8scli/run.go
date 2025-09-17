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

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Reserve GPUs and run a workload",
	Long: `Reserve GPU resources and run a workload in a Pod.
The Pod will have access to the reserved GPUs via CUDA_VISIBLE_DEVICES environment variable.`,
	Example: `  # Run a simple command with 1 GPU
  k8shazgpu run --gpus 1 --image busybox -- /bin/sh -c 'echo $CUDA_VISIBLE_DEVICES; sleep 60'

  # Run with specific GPU IDs
  k8shazgpu run --gpus 2 --gpu-ids 0,1 --image nvidia/cuda:11.8-runtime-ubuntu20.04 -- nvidia-smi`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		client, err := k8s.NewClient(viper.GetString("kubeContext"), namespace)
		if err != nil {
			return fmt.Errorf("failed to create Kubernetes client: %w", err)
		}

		image, err := cmd.Flags().GetString("image")
		if err != nil {
			return err
		}

		if image == "" {
			return fmt.Errorf("--image is required for run command")
		}

		if len(args) == 0 {
			return fmt.Errorf("command is required after --")
		}

		claimName, err := cmd.Flags().GetString("name")
		if err != nil {
			return err
		}

		// Generate name if not provided
		if claimName == "" {
			claimName = fmt.Sprintf("k8shazgpu-run-%d", generateRandomSuffix())
		}

		req := &k8s.ReservationRequest{
			Name:       claimName,
			GPUCount:   gpus,
			GPUIDs:     gpuIDs,
			PreferNode: preferNode,
		}

		// Store Pod spec for delayed creation
		podSpec := &k8s.PodSpec{
			Image:   image,
			Command: args,
		}

		fmt.Printf("Creating ResourceClaim %s requesting %d GPU(s)...\n", claimName, gpus)

		claim, err := client.CreateResourceClaimWithPodSpec(ctx, req, podSpec)
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

		fmt.Printf("✓ Allocated %d GPU(s), creating Pod...\n", len(allocated.AllocatedGPUs))

		podReq := &k8s.PodRequest{
			Name:      claimName + "-pod",
			Image:     image,
			Command:   args,
			ClaimName: claim.Name,
		}

		pod, err := client.CreatePod(ctx, podReq)
		if err != nil {
			return fmt.Errorf("failed to create Pod: %w", err)
		}

		fmt.Printf("Waiting for Pod %s to start...\n", pod.Name)

		if err := client.WaitForPodRunning(ctx, pod.Name); err != nil {
			return fmt.Errorf("failed waiting for Pod to run: %w", err)
		}

		fmt.Printf("✓ Pod %s is running\n", pod.Name)

		// Stream logs
		follow, _ := cmd.Flags().GetBool("follow")
		if follow {
			fmt.Printf("Streaming logs from Pod %s:\n", pod.Name)
			fmt.Println(strings.Repeat("-", 50))
			return client.StreamPodLogs(ctx, pod.Name)
		} else {
			fmt.Printf("\nTo view logs: kubectl logs %s -n %s\n", pod.Name, namespace)
			fmt.Printf("To cleanup: k8shazgpu cleanup --name %s\n", claimName)
		}

		return nil
	},
}

func init() {
	runCmd.Flags().IntVar(&gpus, "gpus", 1, "Number of GPUs to reserve")
	runCmd.Flags().StringSliceVar(&gpuIDs, "gpu-ids", []string{}, "Specific GPU IDs to request (comma-separated)")
	runCmd.Flags().StringVar(&preferNode, "prefer-node", "", "Preferred node name for GPU allocation")
	runCmd.Flags().String("name", "", "Name for the reservation (auto-generated if not provided)")
	runCmd.Flags().String("image", "", "Container image to run (required)")
	runCmd.Flags().Bool("follow", false, "Follow Pod logs after creation")
	runCmd.MarkFlagRequired("image")
}

func generateRandomSuffix() int {
	// Simple random suffix for auto-generated names
	return int(time.Now().Unix() % 10000)
}

func formatGPUSummaryForError(summary *k8s.GPUSummary) string {
	var result strings.Builder
	for _, node := range summary.Nodes {
		result.WriteString(fmt.Sprintf("  Node %s: %d/%d GPUs allocated",
			node.NodeName, len(node.AllocatedGPUs), node.TotalGPUs))
		if len(node.AllocatedGPUs) > 0 {
			result.WriteString(" (")
			for i, gpu := range node.AllocatedGPUs {
				if i > 0 {
					result.WriteString(", ")
				}
				result.WriteString(fmt.Sprintf("GPU%d", gpu.ID))
				if gpu.PodName != "" {
					result.WriteString(fmt.Sprintf(":%s", gpu.PodName))
				}
			}
			result.WriteString(")")
		}
		result.WriteString("\n")
	}
	return result.String()
}

func (c *runCommandContext) waitForAllocationWithStatusUpdates(ctx context.Context, client *k8s.Client, claimName, displayName string) (*k8s.AllocationResult, error) {
	statusShown := false
	statusInterval := 5 * time.Second
	ticker := time.NewTicker(statusInterval)
	defer ticker.Stop()

	// Try immediate allocation first
	allocated, err := client.WaitForAllocationWithTimeout(ctx, claimName, 2*time.Second)
	if err == nil {
		return allocated, nil
	}

	// Show initial status if allocation is pending
	summary, summaryErr := client.GetGPUSummary(ctx)
	if summaryErr == nil && summary.AvailableGPUs == 0 {
		fmt.Printf("⏳ No GPUs currently available - your request is queued\n")
		fmt.Printf("\nCurrent GPU status:\n%s", formatGPUStatus(summary))
		statusShown = true
	}

	startTime := time.Now()
	for {
		select {
		case <-ctx.Done():
			if statusShown {
				fmt.Printf("\n🧹 To cancel this request: kubectl delete resourceclaim %s\n", claimName)
				fmt.Printf("💡 To monitor later: k8shazgpu status --name %s\n", displayName)
			}
			return nil, ctx.Err()
		case <-ticker.C:
			// Check allocation status
			allocated, err := client.WaitForAllocationWithTimeout(ctx, claimName, 500*time.Millisecond)
			if err == nil {
				if statusShown {
					fmt.Printf("✓ GPU allocation successful after %v\n", time.Since(startTime).Round(time.Second))
				}
				return allocated, nil
			}

			// Show periodic status update
			if summary, summaryErr := client.GetGPUSummary(ctx); summaryErr == nil {
				elapsed := time.Since(startTime).Round(time.Second)
				if summary.AvailableGPUs == 0 {
					fmt.Printf("⏳ Still waiting for GPU allocation (%v elapsed)\n", elapsed)
					if !statusShown {
						fmt.Printf("\nCurrent GPU status:\n%s", formatGPUStatus(summary))
						statusShown = true
					}
				} else {
					fmt.Printf("🔄 %d GPUs became available (%v elapsed), checking allocation...\n", summary.AvailableGPUs, elapsed)
				}
			}
		}
	}
}

func formatGPUStatus(summary *k8s.GPUSummary) string {
	var result strings.Builder
	for _, node := range summary.Nodes {
		result.WriteString(fmt.Sprintf("  Node %s: %d total GPUs\n", node.NodeName, node.TotalGPUs))
		if len(node.AllocatedGPUs) > 0 {
			result.WriteString("    Allocated GPUs:\n")
			for _, gpu := range node.AllocatedGPUs {
				if gpu.PodName != "" {
					result.WriteString(fmt.Sprintf("      GPU%d → Pod: %s\n", gpu.ID, gpu.PodName))
				} else if gpu.ClaimUID != "" {
					result.WriteString(fmt.Sprintf("      GPU%d → Claim: %s\n", gpu.ID, gpu.ClaimUID[:8]+"..."))
				} else {
					result.WriteString(fmt.Sprintf("      GPU%d → Reserved\n", gpu.ID))
				}
			}
		}
		if summary.AvailableGPUs > 0 {
			result.WriteString(fmt.Sprintf("    Available: %d GPUs\n", summary.AvailableGPUs))
		} else {
			result.WriteString("    Available: None\n")
		}
	}

	// Show helpful commands
	result.WriteString("\n💡 Helpful commands while waiting:\n")
	result.WriteString("   k8shazgpu status     - View current GPU allocation status\n")
	result.WriteString("   kubectl get pods     - See running pods\n")

	return result.String()
}

type runCommandContext struct {}