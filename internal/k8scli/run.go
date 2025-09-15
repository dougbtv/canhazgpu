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

		fmt.Printf("Creating ResourceClaim %s requesting %d GPU(s)...\n", claimName, gpus)

		claim, err := client.CreateResourceClaim(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to create ResourceClaim: %w", err)
		}

		fmt.Printf("Waiting for allocation of claim %s...\n", claim.Name)

		allocated, err := client.WaitForAllocation(ctx, claim.Name)
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