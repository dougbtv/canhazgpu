package k8scli

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/russellb/canhazgpu/pkg/k8s"
	corev1 "k8s.io/api/core/v1"
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "View logs from a running k8shazgpu workload",
	Long: `Stream logs from a running k8shazgpu workload Pod.
If --name is not specified, shows logs from the most recent running Pod.`,
	Example: `  # Show logs from a specific workload
  k8shazgpu logs --name my-workload

  # Show logs from the most recent running workload
  k8shazgpu logs`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		client, err := k8s.NewClient(viper.GetString("kubeContext"), namespace)
		if err != nil {
			return fmt.Errorf("failed to create Kubernetes client: %w", err)
		}

		// Get flags
		name, err := cmd.Flags().GetString("name")
		if err != nil {
			return err
		}

		follow, err := cmd.Flags().GetBool("follow")
		if err != nil {
			return err
		}

		// Find the pod
		podName, err := findPodForWorkload(ctx, client, name)
		if err != nil {
			return err
		}

		fmt.Printf("📜 Streaming logs from %s...\n", podName)
		if follow {
			return client.StreamPodLogs(ctx, podName)
		} else {
			return client.GetPodLogs(ctx, podName, false)
		}
	},
}

var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Open an interactive shell in a running k8shazgpu workload",
	Long: `Execute an interactive bash shell in a running k8shazgpu workload Pod.
If --name is not specified, opens a shell in the most recent running Pod.`,
	Example: `  # Debug a specific workload
  k8shazgpu debug --name my-workload

  # Debug the most recent running workload
  k8shazgpu debug

  # Use a different shell
  k8shazgpu debug --name my-workload --shell /bin/sh`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		client, err := k8s.NewClient(viper.GetString("kubeContext"), namespace)
		if err != nil {
			return fmt.Errorf("failed to create Kubernetes client: %w", err)
		}

		// Get flags
		name, err := cmd.Flags().GetString("name")
		if err != nil {
			return err
		}

		shell, err := cmd.Flags().GetString("shell")
		if err != nil {
			return err
		}

		// Find the pod
		podName, err := findPodForWorkload(ctx, client, name)
		if err != nil {
			return err
		}

		fmt.Printf("🐛 Opening interactive shell in %s...\n", podName)
		fmt.Printf("💡 Tip: Type 'exit' to close the shell\n\n")

		return client.ExecInPod(ctx, podName, shell)
	},
}

// findPodForWorkload finds the pod name for a given workload name
// If name is empty, returns the most recent running pod
func findPodForWorkload(ctx context.Context, client *k8s.Client, name string) (string, error) {
	if name != "" {
		// Try to find a pod for the specific claim name
		status, err := client.GetClaimStatus(ctx, name)
		if err != nil {
			return "", fmt.Errorf("failed to find workload '%s': %w", name, err)
		}

		if status.PodName == "" {
			return "", fmt.Errorf("workload '%s' does not have a running Pod yet", name)
		}

		if status.PodPhase != corev1.PodRunning {
			return "", fmt.Errorf("Pod for workload '%s' is not running (status: %s)", name, status.PodPhase)
		}

		return status.PodName, nil
	}

	// No name specified - find the most recent running pod
	statuses, err := client.ListClaimStatuses(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list workloads: %w", err)
	}

	// Filter to running pods and sort by creation time (most recent first)
	type podInfo struct {
		name         string
		claimName    string
		creationTime time.Time
	}

	var runningPods []podInfo
	for _, status := range statuses {
		if status.PodName != "" && status.PodPhase == corev1.PodRunning {
			// Get pod creation time
			creationTime, err := client.GetPodCreationTime(ctx, status.PodName)
			if err != nil {
				continue
			}

			runningPods = append(runningPods, podInfo{
				name:         status.PodName,
				claimName:    status.Name,
				creationTime: creationTime,
			})
		}
	}

	if len(runningPods) == 0 {
		return "", fmt.Errorf("no running workloads found\n💡 Tip: Use 'k8shazgpu status' to see all workloads")
	}

	// Sort by creation time (most recent first)
	sort.Slice(runningPods, func(i, j int) bool {
		return runningPods[i].creationTime.After(runningPods[j].creationTime)
	})

	mostRecent := runningPods[0]
	fmt.Printf("🎯 Auto-selected most recent workload: %s\n", mostRecent.claimName)

	return mostRecent.name, nil
}

func init() {
	logsCmd.Flags().String("name", "", "Name of the workload (uses most recent if not specified)")
	logsCmd.Flags().BoolP("follow", "f", true, "Follow log output")

	debugCmd.Flags().String("name", "", "Name of the workload (uses most recent if not specified)")
	debugCmd.Flags().String("shell", "/bin/bash", "Shell to use for interactive session")

	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(debugCmd)
}
