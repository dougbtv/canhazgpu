package k8scli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/russellb/canhazgpu/pkg/k8s"
)

var reserveCmd = &cobra.Command{
	Use:   "reserve",
	Short: "Reserve GPUs without running a workload",
	Long: `Reserve GPU resources and wait until allocation is successful.
This creates a ResourceClaim that holds the GPUs until manually released.`,
	Example: `  # Reserve 1 GPU
  k8shazgpu reserve --gpus 1 --name my-reservation

  # Reserve specific GPU IDs
  k8shazgpu reserve --gpus 2 --gpu-ids 0,1 --name specific-gpus`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		client, err := k8s.NewClient(viper.GetString("kubeContext"), namespace)
		if err != nil {
			return fmt.Errorf("failed to create Kubernetes client: %w", err)
		}

		claimName, err := cmd.Flags().GetString("name")
		if err != nil {
			return err
		}

		if claimName == "" {
			return fmt.Errorf("--name is required for reserve command")
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

		fmt.Printf("✓ Successfully allocated %d GPU(s) to claim %s\n", len(allocated.AllocatedGPUs), claim.Name)
		for _, gpuID := range allocated.AllocatedGPUs {
			fmt.Printf("  - GPU %d on node %s\n", gpuID, allocated.NodeName)
		}

		return nil
	},
}

func init() {
	reserveCmd.Flags().IntVar(&gpus, "gpus", 1, "Number of GPUs to reserve")
	reserveCmd.Flags().StringSliceVar(&gpuIDs, "gpu-ids", []string{}, "Specific GPU IDs to request (comma-separated)")
	reserveCmd.Flags().StringVar(&preferNode, "prefer-node", "", "Preferred node name for GPU allocation")
	reserveCmd.Flags().String("name", "", "Name for the reservation (required)")
	reserveCmd.MarkFlagRequired("name")
}