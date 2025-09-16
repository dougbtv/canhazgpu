package k8scli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/russellb/canhazgpu/pkg/k8s"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of GPU reservations and Pods",
	Long: `Display the current status of ResourceClaims and associated Pods
in the specified namespace.`,
	Example: `  # Show status in default namespace
  k8shazgpu status

  # Show status with specific claim name
  k8shazgpu status --name my-reservation`,
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

		if claimName != "" {
			// Show status for specific claim
			return showClaimStatus(ctx, client, claimName)
		}

		// Show status for all claims
		return showAllStatus(ctx, client)
	},
}

func showClaimStatus(ctx context.Context, client *k8s.Client, claimName string) error {
	status, err := client.GetClaimStatus(ctx, claimName)
	if err != nil {
		return fmt.Errorf("failed to get claim status: %w", err)
	}

	fmt.Printf("ResourceClaim: %s\n", status.Name)
	fmt.Printf("  Status: %s\n", status.State)

	if status.Allocated {
		fmt.Printf("  Node: %s\n", status.NodeName)
		fmt.Printf("  GPUs: %s\n", formatGPUList(status.AllocatedGPUs))
	}

	if status.PodName != "" {
		fmt.Printf("  Pod: %s (%s)\n", status.PodName, status.PodPhase)
	}

	if status.Error != "" {
		fmt.Printf("  Error: %s\n", status.Error)
	}

	return nil
}

func showAllStatus(ctx context.Context, client *k8s.Client) error {
	// Get GPU summary first
	summary, err := client.GetGPUSummary(ctx)
	if err != nil {
		fmt.Printf("Warning: failed to get GPU summary: %v\n\n", err)
	} else {
		fmt.Printf("GPU Summary:\n")
		fmt.Printf("  Total GPUs: %d\n", summary.TotalGPUs)
		fmt.Printf("  Available: %d\n", summary.AvailableGPUs)
		fmt.Printf("  Allocated: %d\n", summary.AllocatedGPUs)
		fmt.Println()

		// Show per-node details
		for _, node := range summary.Nodes {
			fmt.Printf("Node %s:\n", node.NodeName)
			fmt.Printf("  Total GPUs: %d\n", node.TotalGPUs)
			fmt.Printf("  Available GPUs: %s\n", formatGPUList(node.AvailableGPUs))
			if len(node.AllocatedGPUs) > 0 {
				fmt.Printf("  Allocated GPUs:\n")
				for _, gpu := range node.AllocatedGPUs {
					claimName := "unknown"
					if gpu.ClaimUID != "" {
						// Try to find ResourceClaim name from UID
						claimName = gpu.ClaimUID[:8] + "..." // Show first 8 chars of UID
					}
					fmt.Printf("    GPU %d: %s", gpu.ID, claimName)
					if gpu.PodName != "" {
						fmt.Printf(" (Pod: %s)", gpu.PodName)
					}
					fmt.Println()
				}
			}
			fmt.Println()
		}
	}

	statuses, err := client.ListClaimStatuses(ctx)
	if err != nil {
		return fmt.Errorf("failed to list claim statuses: %w", err)
	}

	if len(statuses) == 0 {
		fmt.Println("No ResourceClaims found in namespace", namespace)
		return nil
	}

	fmt.Printf("ResourceClaims in namespace %s:\n\n", namespace)

	for _, status := range statuses {
		fmt.Printf("NAME: %s\n", status.Name)
		fmt.Printf("  Status: %s\n", status.State)

		if status.Allocated {
			fmt.Printf("  Node: %s\n", status.NodeName)
			fmt.Printf("  GPUs: %s\n", formatGPUList(status.AllocatedGPUs))
		}

		if status.PodName != "" {
			fmt.Printf("  Pod: %s (%s)\n", status.PodName, status.PodPhase)
		}

		if status.Error != "" {
			fmt.Printf("  Error: %s\n", status.Error)
		}

		fmt.Println()
	}

	return nil
}

func formatGPUList(gpus []int) string {
	if len(gpus) == 0 {
		return "none"
	}

	strs := make([]string, len(gpus))
	for i, gpu := range gpus {
		strs[i] = fmt.Sprintf("%d", gpu)
	}
	return strings.Join(strs, ", ")
}

func init() {
	statusCmd.Flags().String("name", "", "Show status for specific ResourceClaim")
}