package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/klog/v2"

	"github.com/russellb/canhazgpu/pkg/cdi"
	"github.com/russellb/canhazgpu/pkg/redisstate"
)

func main() {
	var (
		port         = flag.Int("port", 8082, "HTTP server port")
		redisHost    = flag.String("redis-host", "localhost", "Redis host")
		redisPort    = flag.Int("redis-port", 6379, "Redis port")
		redisSocket  = flag.String("redis-socket", "", "Redis Unix socket path (overrides host/port)")
		redisDB      = flag.Int("redis-db", 0, "Redis database")
		cdiPath      = flag.String("cdi-path", "/var/run/cdi/canhazgpu.json", "Path to CDI spec file")
		gpuCount     = flag.Int("gpu-count", 0, "Number of GPUs (auto-detect if 0)")
		nodeName     = flag.String("node-name", "", "Kubernetes node name")
	)

	klog.InitFlags(nil)
	flag.Parse()

	if *nodeName == "" {
		*nodeName = os.Getenv("NODE_NAME")
		if *nodeName == "" {
			klog.Fatal("node-name must be provided via flag or NODE_NAME environment variable")
		}
	}

	// Create Redis client (prefer socket over network)
	var redisClient *redisstate.Client
	if *redisSocket != "" {
		klog.Infof("Connecting to Redis via socket: %s", *redisSocket)
		redisClient = redisstate.NewClientWithSocket(*redisSocket, *redisDB)
	} else {
		klog.Infof("Connecting to Redis via network: %s:%d", *redisHost, *redisPort)
		redisClient = redisstate.NewClient(*redisHost, *redisPort, *redisDB)
	}
	defer redisClient.Close()

	// Test Redis connection
	ctx := context.Background()
	if err := redisClient.Ping(ctx); err != nil {
		klog.Fatalf("Failed to connect to Redis: %v", err)
	}

	// Determine GPU count
	if *gpuCount == 0 {
		*gpuCount = detectGPUCount()
	}

	if *gpuCount == 0 {
		klog.Warning("No GPUs detected or GPU count not specified")
	}

	// Generate and write CDI spec
	if err := generateCDISpec(*gpuCount, *cdiPath); err != nil {
		klog.Errorf("Failed to generate CDI spec: %v", err)
	} else {
		klog.Infof("Generated CDI spec with %d GPUs at %s", *gpuCount, *cdiPath)
	}

	// Create and start HTTP server
	agent := &NodeAgent{
		NodeName:    *nodeName,
		RedisClient: redisClient,
		GPUCount:    *gpuCount,
	}

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: agent.setupRoutes(),
	}

	// Start server in background
	go func() {
		klog.Infof("Starting HTTP server on port %d", *port)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			klog.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// Start heartbeat routine
	go agent.startHeartbeat(ctx)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	klog.Info("Shutting down...")

	// Shutdown HTTP server
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		klog.Errorf("Failed to shutdown HTTP server: %v", err)
	}

	klog.Info("Shutdown complete")
}

func detectGPUCount() int {
	// TODO: Implement GPU detection using nvidia-smi or similar
	// For now, return a default value
	return 1
}

func generateCDISpec(gpuCount int, cdiPath string) error {
	if gpuCount == 0 {
		return nil
	}

	spec := cdi.GenerateGPUSpec(gpuCount)
	return spec.WriteSpecToFile(cdiPath)
}