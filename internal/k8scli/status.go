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