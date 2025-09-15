package k8scli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/russellb/canhazgpu/pkg/k8s"
)

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Delete ResourceClaims and associated Pods",
	Long: `Delete the specified ResourceClaim and any associated Pods.
This will release the reserved GPU resources.`,
	Example: `  # Cleanup specific reservation
  k8shazgpu cleanup --name my-reservation

  # Cleanup all resources in namespace
  k8shazgpu cleanup --all`,
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

		all, err := cmd.Flags().GetBool("all")
		if err != nil {
			return err
		}

		if !all && claimName == "" {
			return fmt.Errorf("either --name or --all must be specified")
		}

		if all && claimName != "" {
			return fmt.Errorf("cannot specify both --name and --all")
		}

		if all {
			return cleanupAll(ctx, client)
		}

		return cleanupClaim(ctx, client, claimName)
	},
}

func cleanupClaim(ctx context.Context, client *k8s.Client, claimName string) error {
	fmt.Printf("Cleaning up ResourceClaim %s...\n", claimName)

	// Get associated Pod name first
	status, err := client.GetClaimStatus(ctx, claimName)
	if err != nil {
		fmt.Printf("Warning: failed to get claim status: %v\n", err)
	}

	// Delete Pod if it exists
	if status != nil && status.PodName != "" {
		fmt.Printf("Deleting Pod %s...\n", status.PodName)
		if err := client.DeletePod(ctx, status.PodName); err != nil {
			fmt.Printf("Warning: failed to delete Pod %s: %v\n", status.PodName, err)
		} else {
			fmt.Printf("✓ Pod %s deleted\n", status.PodName)
		}
	}

	// Delete ResourceClaim
	if err := client.DeleteResourceClaim(ctx, claimName); err != nil {
		return fmt.Errorf("failed to delete ResourceClaim %s: %w", claimName, err)
	}

	fmt.Printf("✓ ResourceClaim %s deleted\n", claimName)
	return nil
}

func cleanupAll(ctx context.Context, client *k8s.Client) error {
	fmt.Printf("Cleaning up all ResourceClaims in namespace %s...\n", namespace)

	statuses, err := client.ListClaimStatuses(ctx)
	if err != nil {
		return fmt.Errorf("failed to list claims: %w", err)
	}

	if len(statuses) == 0 {
		fmt.Println("No ResourceClaims found to cleanup")
		return nil
	}

	for _, status := range statuses {
		if err := cleanupClaim(ctx, client, status.Name); err != nil {
			fmt.Printf("Error cleaning up %s: %v\n", status.Name, err)
		}
	}

	fmt.Printf("✓ Cleanup completed\n")
	return nil
}

func init() {
	cleanupCmd.Flags().String("name", "", "Name of ResourceClaim to cleanup")
	cleanupCmd.Flags().Bool("all", false, "Cleanup all ResourceClaims in namespace")
}