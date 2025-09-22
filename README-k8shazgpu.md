K8s haz gpu? Yup.

# Build all components
make build-all

# Deploy to cluster  
make deploy

# Run hello world test
make hello

# Check interoperability
./build/canhazgpu status


# Cache

k8shazgpu cache plan show

k8shazgpu cache list

## Detailed status command (shows individual image status with icons:)
k8shazgpu cache status 

# git caches

k8shazgpu cache add gitrepo https://github.com/vllm-project/vllm.git --branch main --name vllm-main

# testing notes
k8shazgpu vllm run --name vllm-dev-privileged -- sleep 500
k8shazgpu cleanup --name vllm-dev-privileged

vllm serve facebook/opt-125m \
  --gpu-memory-utilization 0.8


vllm serve facebook/opt-125m --gpu-memory-utilization 0.8

k8shazgpu vllm run --follow --name vllm-dev-privileged -- vllm serve facebook/opt-125m --gpu-memory-utilization 

