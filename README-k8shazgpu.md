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

k8shazgpu vllm run --follow --name vllm-dev-privileged -- vllm serve facebook/opt-125m --gpu-memory-utilization 0.8

k8shazgpu vllm run --name vllm-dev-checker --image-name vllm-pinned --repo-name dougbtv-vllm -- vllm serve facebook/opt-125m --gpu-memory-utilization 0.8

curl http://10.244.0.145:8000/v1/completions   -H "Content-Type: application/json"   -d '{
    "model": "facebook/opt-125m",
    "prompt": "What is the capital of Vermont?",
    "max_tokens": 200
  }'


# Model cache.


## Add a public model
k8shazgpu cache add model facebook/opt-125m
k8shazgpu cache add model mistralai/Mistral-7B-v0.1

## Add a model with specific revision
k8shazgpu cache add model microsoft/DialoGPT-medium --revision v1.0

## Add a model with custom name
k8shazgpu cache add model huggingface/CodeBERTa-small-v1 --name codeberta

## View cache status (shows models separately)
k8shazgpu cache status

## Set up HF token for private models
kubectl create secret generic hf-token --from-literal=token=your_token_here -n canhazgpu-system


