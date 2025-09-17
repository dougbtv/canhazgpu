package k8scli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile     string
	namespace   string
	kubeContext string
	timeout     time.Duration
	gpus        int
	gpuIDs      []string
	preferNode  string
)

var rootCmd = &cobra.Command{
	Use:   "k8shazgpu",
	Short: "Kubernetes GPU reservation tool using DRA",
	Long: `k8shazgpu is a Kubernetes-native version of canhazgpu that uses
Dynamic Resource Allocation (DRA) to coordinate GPU reservations across
a Kubernetes cluster while maintaining compatibility with local Redis state.`,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.k8shazgpu.yaml)")
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "default", "Kubernetes namespace")
	rootCmd.PersistentFlags().StringVar(&kubeContext, "context", "", "Kubernetes context to use")
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", 5*time.Minute, "Timeout for operations")

	// Bind flags to viper
	viper.BindPFlag("namespace", rootCmd.PersistentFlags().Lookup("namespace"))
	viper.BindPFlag("kubeContext", rootCmd.PersistentFlags().Lookup("context"))
	viper.BindPFlag("timeout", rootCmd.PersistentFlags().Lookup("timeout"))

	// Add subcommands
	rootCmd.AddCommand(reserveCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(cleanupCmd)
	rootCmd.AddCommand(reconcileCmd)
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".k8shazgpu")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}