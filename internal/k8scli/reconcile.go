package k8scli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/russellb/canhazgpu/pkg/k8s"
)

var reconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Create Pods for allocated ResourceClaims that don't have Pods yet",
	Long: `Reconcile will scan for ResourceClaims that have been allocated but don't have
associated Pods yet, and create the missing Pods using the stored Pod specifications.

This is useful when allocation requests were queued and allocated later after the
original command timed out.`,
	Example: `  # Create missing Pods for allocated claims
  k8shazgpu reconcile

  # Run reconciliation for a specific namespace
  k8shazgpu reconcile --namespace gpu-workloads`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		client, err := k8s.NewClient(viper.GetString("kubeContext"), namespace)
		if err != nil {
			return fmt.Errorf("failed to create Kubernetes client: %w", err)
		}

		fmt.Printf("Scanning for allocated ResourceClaims without Pods in namespace %s...\n", namespace)

		if err := client.CreatePodsForAllocatedClaims(ctx); err != nil {
			return fmt.Errorf("failed to reconcile claims: %w", err)
		}

		fmt.Println("✓ Reconciliation complete")
		return nil
	},
}