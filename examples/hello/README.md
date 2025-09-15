# Hello World Example

This example demonstrates the basic usage of k8shazgpu to reserve GPU resources and run a simple workload.

## Prerequisites

1. A Kubernetes cluster with k8shazgpu deployed
2. Redis running on localhost:6379 on the node
3. The k8shazgpu CLI built and installed

## Quick Start

Simply run the provided script:

```bash
./run.sh
```

This will:
1. Create a ResourceClaim requesting 1 GPU
2. Wait for the DRA controller to allocate the GPU
3. Create a Pod that uses the allocated GPU
4. Show the Pod logs (which should include `CUDA_VISIBLE_DEVICES=0`)
5. Clean up the resources

## Manual Steps

If you prefer to run the commands manually:

```bash
# Reserve and run a GPU workload
k8shazgpu run --gpus 1 --image busybox --name my-demo \
  -- /bin/sh -c 'echo "GPU: $CUDA_VISIBLE_DEVICES"; sleep 30'

# Check status
k8shazgpu status --name my-demo

# View logs
kubectl logs my-demo-pod

# Clean up
k8shazgpu cleanup --name my-demo
```

## Expected Output

When successful, the Pod logs should show:
```
Hello from GPU workload!
CUDA_VISIBLE_DEVICES=0
Available GPUs: 0
Workload completed
```

The exact GPU ID (0, 1, etc.) will depend on which GPU was allocated.

## Troubleshooting

1. **No ResourceClaims created**: Check that the ResourceClass exists:
   ```bash
   kubectl get resourceclasses
   ```

2. **Allocation pending**: Check the controller logs:
   ```bash
   kubectl logs -n canhazgpu-system deployment/canhazgpu-controller
   ```

3. **Pod not starting**: Check the nodeagent logs:
   ```bash
   kubectl logs -n canhazgpu-system daemonset/canhazgpu-nodeagent
   ```

4. **No GPU environment variable**: Verify the CDI spec was generated:
   ```bash
   # On the node:
   cat /var/run/cdi/canhazgpu.json
   ```

## Next Steps

- Try requesting specific GPU IDs: `--gpu-ids 0,1`
- Use a real GPU workload image like `nvidia/cuda:11.8-runtime-ubuntu20.04`
- Check interoperability with local `canhazgpu` commands