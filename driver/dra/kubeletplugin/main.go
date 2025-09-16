package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/klog/v2"
)

const (
	DriverName = "canhazgpu.com"
)

type Config struct {
	nodeName                      string
	cdiRoot                       string
	numDevices                    int
	kubeletRegistrarDirectoryPath string
	kubeletPluginsDirectoryPath   string
	healthcheckPort               int
	kubeConfig                    string
}

func main() {
	config := &Config{}

	cmd := &cobra.Command{
		Use:   "canhazgpu-kubeletplugin",
		Short: "Kubelet plugin for canhazgpu DRA driver",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), config)
		},
	}

	cmd.Flags().StringVar(&config.nodeName, "node-name", "", "The name of the node")
	cmd.Flags().StringVar(&config.cdiRoot, "cdi-root", "/var/run/cdi", "CDI root directory")
	cmd.Flags().IntVar(&config.numDevices, "num-devices", 8, "Number of GPU devices")
	cmd.Flags().StringVar(&config.kubeletRegistrarDirectoryPath, "kubelet-registrar-directory-path",
		kubeletplugin.KubeletRegistryDir, "Kubelet registrar directory")
	cmd.Flags().StringVar(&config.kubeletPluginsDirectoryPath, "kubelet-plugins-directory-path",
		kubeletplugin.KubeletPluginsDir, "Kubelet plugins directory")
	cmd.Flags().IntVar(&config.healthcheckPort, "healthcheck-port", -1, "Healthcheck port")
	cmd.Flags().StringVar(&config.kubeConfig, "kubeconfig", "", "Kubeconfig file path")

	// Environment variable defaults
	if nodeName := os.Getenv("NODE_NAME"); nodeName != "" {
		config.nodeName = nodeName
	}
	if cdiRoot := os.Getenv("CDI_ROOT"); cdiRoot != "" {
		config.cdiRoot = cdiRoot
	}

	cmd.MarkFlagRequired("node-name")

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, config *Config) error {
	logger := klog.FromContext(ctx)

	// Create Kubernetes client
	kubeClient, err := createKubeClient(config.kubeConfig)
	if err != nil {
		return fmt.Errorf("failed to create kube client: %w", err)
	}

	// Setup plugin directory
	pluginPath := filepath.Join(config.kubeletPluginsDirectoryPath, DriverName)
	err = os.MkdirAll(pluginPath, 0750)
	if err != nil {
		return err
	}

	// Setup CDI directory
	err = os.MkdirAll(config.cdiRoot, 0750)
	if err != nil {
		return err
	}

	// Setup signal handling
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	// Create and start the driver
	driver, err := NewDriver(ctx, config, kubeClient)
	if err != nil {
		return err
	}

	<-ctx.Done()
	stop()

	if err := context.Cause(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error(err, "error from context")
	}

	err = driver.Shutdown(logger)
	if err != nil {
		logger.Error(err, "Unable to cleanly shutdown driver")
	}

	return nil
}

func createKubeClient(kubeconfig string) (kubernetes.Interface, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(config)
}