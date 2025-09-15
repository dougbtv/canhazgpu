#!/bin/bash

set -e

echo "=== k8shazgpu Hello World Example ==="
echo

# Check if k8shazgpu binary exists
if ! command -v k8shazgpu &> /dev/null; then
    echo "Error: k8shazgpu command not found. Please build and install it first:"
    echo "  go build ./cmd/k8shazgpu"
    echo "  sudo cp k8shazgpu /usr/local/bin/"
    exit 1
fi

echo "1. Checking cluster status..."
kubectl cluster-info

echo
echo "2. Running GPU workload with k8shazgpu..."
echo "This will:"
echo "  - Create a ResourceClaim requesting 1 GPU"
echo "  - Wait for allocation"
echo "  - Create a Pod that prints CUDA_VISIBLE_DEVICES"
echo "  - Show the logs"
echo

k8shazgpu run \
  --gpus 1 \
  --image busybox \
  --name hello-gpu-demo \
  --follow \
  -- /bin/sh -c 'echo "Hello from GPU workload!"; echo "CUDA_VISIBLE_DEVICES=$CUDA_VISIBLE_DEVICES"; echo "Available GPUs: $CUDA_VISIBLE_DEVICES"; sleep 30; echo "Workload completed"'

echo
echo "3. Checking status..."
k8shazgpu status --name hello-gpu-demo

echo
echo "4. Cleaning up..."
k8shazgpu cleanup --name hello-gpu-demo

echo
echo "=== Hello World Example Complete ==="