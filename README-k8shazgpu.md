# k8shazgpu - Kubernetes GPU Allocation and vLLM Management

K8s haz gpu? Yup! 🚀

k8shazgpu is a Kubernetes-native GPU allocation system with integrated vLLM support. It provides dynamic resource allocation (DRA) for GPUs with intelligent caching, automatic checkout detection, and seamless container orchestration.

## Table of Contents

- [User Guide](#user-guide)
  - [Core Concepts](#core-concepts)
  - [Quick Start](#quick-start)
  - [Basic Commands](#basic-commands)
  - [vLLM Integration](#vllm-integration)
  - [Cache Management](#cache-management)
  - [Advanced Usage](#advanced-usage)
- [Developer Guide](#developer-guide)
  - [Building from Source](#building-from-source)
  - [Development Workflow](#development-workflow)
  - [Testing](#testing)
  - [Architecture](#architecture)

---

## User Guide

### Core Concepts

**k8shazgpu** extends Kubernetes with GPU allocation capabilities using Dynamic Resource Allocation (DRA). Key concepts:

- **ResourceClaims**: Kubernetes resources that request GPU allocation
- **Cache Plans**: Declarative specifications for pre-cached images and git repositories
- **Node Agents**: DaemonSet pods that manage caching and GPU allocation on each node
- **Controller**: Creates and manages vLLM pods with allocated GPUs
- **vLLM Integration**: Automatic detection of vLLM checkouts with diff packaging

### Quick Start

1. **Check GPU allocation status:**
   ```bash
   k8shazgpu status
   ```

2. **Run a simple vLLM workload:**
   ```bash
   k8shazgpu vllm run --name my-demo -- vllm serve facebook/opt-125m --gpu-memory-utilization 0.8
   ```

3. **Check what's running:**
   ```bash
   k8shazgpu status
   kubectl get pods
   ```

4. **Clean up:**
   ```bash
   k8shazgpu cleanup --name my-demo
   ```

### Basic Commands

#### Status and Information
```bash
# Check GPU allocation status
k8shazgpu status

# List all ResourceClaims
kubectl get resourceclaims

# Check cache status
k8shazgpu cache status

# View cache plan
k8shazgpu cache plan show
```

#### Running Workloads
```bash
# Run vLLM with specific model
k8shazgpu vllm run --name my-workload -- vllm serve facebook/opt-125m --gpu-memory-utilization 0.8

# Run with multiple GPUs
k8shazgpu vllm run --name multi-gpu --gpus 2 -- vllm serve meta-llama/Llama-2-7b-chat-hf

# Run with specific cached resources
k8shazgpu vllm run --name custom --image-name my-image --repo-name my-repo -- vllm serve facebook/opt-125m

# Follow logs during execution
k8shazgpu vllm run --name demo --follow -- vllm serve facebook/opt-125m --gpu-memory-utilization 0.8
```

#### Cleanup
```bash
# Clean up specific workload
k8shazgpu cleanup --name my-workload

# Clean up all workloads
k8shazgpu cleanup --all
```

### vLLM Integration

k8shazgpu provides seamless integration with vLLM, including automatic checkout detection:

#### Automatic Checkout Detection
When running from a vLLM checkout directory, k8shazgpu automatically:
- Detects the git repository and branch
- Determines the appropriate pre-built image based on merge-base with upstream/main
- Packages local changes (modified and untracked files) for transport
- Applies changes in the container before running your command

```bash
# From within a vLLM checkout directory
cd /path/to/vllm-checkout
k8shazgpu vllm run --name checkout-demo -- vllm serve facebook/opt-125m --gpu-memory-utilization 0.8
```

Output shows automatic detection:
```
🔍 vLLM checkout detected!
vLLM Checkout detected:
  Directory: /path/to/vllm-checkout
  Remote: git@github.com:user/vllm.git
  Branch: feature-branch
  Commit: abc123ef
  Merge base: def456ab
  Image: public.ecr.aws/q9t5s3a7/vllm-ci-postmerge-repo:def456ab...
  Local changes: 2 modified, 1 untracked files
📦 Packaging local diffs for transport
```

#### Manual vLLM Usage
```bash
# Use specific cached image and repo
k8shazgpu vllm run --name manual \
  --image-name vllm-pinned \
  --repo-name my-vllm \
  -- vllm serve facebook/opt-125m --gpu-memory-utilization 0.8

# Run with port mapping
k8shazgpu vllm run --name api-server --port 8000 \
  -- vllm serve facebook/opt-125m --gpu-memory-utilization 0.8

# Access the API server
kubectl port-forward api-server-vllm-pod 8000:8000
curl http://localhost:8000/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "facebook/opt-125m",
    "prompt": "What is the capital of Vermont?",
    "max_tokens": 200
  }'
```

### Cache Management

k8shazgpu uses a declarative caching system for images and git repositories:

#### Image Caching
```bash
# Add container images to cache
k8shazgpu cache add image public.ecr.aws/q9t5s3a7/vllm-ci-postmerge-repo:latest

# Add with custom name
k8shazgpu cache add image nvidia/cuda:12.1-runtime-ubuntu22.04 --name cuda-runtime

# Check cache status
k8shazgpu cache status
```

#### Git Repository Caching
```bash
# Add git repository
k8shazgpu cache add gitrepo https://github.com/vllm-project/vllm.git --branch main --name vllm-main

# Add with specific branch/commit
k8shazgpu cache add gitrepo https://github.com/user/vllm.git --branch feature-branch --name vllm-feature

# Add private repository (requires SSH keys)
k8shazgpu cache add gitrepo git@github.com:user/private-vllm.git --name vllm-private
```

#### Model Caching
```bash
# Add Hugging Face models
k8shazgpu cache add model facebook/opt-125m
k8shazgpu cache add model mistralai/Mistral-7B-v0.1

# Add with specific revision
k8shazgpu cache add model microsoft/DialoGPT-medium --revision v1.0

# Add with custom name
k8shazgpu cache add model huggingface/CodeBERTa-small-v1 --name codeberta

# For private models, set up HF token
kubectl create secret generic hf-token \
  --from-literal=token=your_token_here \
  -n canhazgpu-system
```

#### Cache Operations
```bash
# View current cache plan
k8shazgpu cache plan show

# List cached items
k8shazgpu cache list

# Check detailed cache status with individual item status
k8shazgpu cache status
```

### Advanced Usage

#### GPU Selection
```bash
# Request specific number of GPUs
k8shazgpu vllm run --name multi-gpu --gpus 2 -- vllm serve large-model

# Request specific GPU IDs
k8shazgpu vllm run --name specific-gpu --gpu-ids 0,2 -- vllm serve model

# Prefer specific node
k8shazgpu vllm run --name node-specific --prefer-node worker-1 -- vllm serve model
```

#### Container Lifecycle
```bash
# Skip cache validation (faster startup)
k8shazgpu vllm run --name fast-start --skip-cache-check -- vllm serve model

# Set custom timeout
k8shazgpu vllm run --name long-running --timeout 30m -- long-running-process

# Disable port mapping
k8shazgpu vllm run --name no-port --port 0 -- vllm serve model
```

#### Debugging and Monitoring
```bash
# Check pod logs
kubectl logs my-workload-vllm-pod

# Exec into running pod
kubectl exec -it my-workload-vllm-pod -- /bin/bash

# Monitor GPU allocation
watch k8shazgpu status

# Check node agent logs
kubectl logs -n canhazgpu-system daemonset/canhazgpu-nodeagent

# Check controller logs
kubectl logs -n canhazgpu-system deployment/canhazgpu-controller
```

---

## Developer Guide

### Building from Source

#### Prerequisites
- Go 1.23+
- Docker or Podman
- Kubernetes cluster with CRI-O or containerd
- kubectl configured for your cluster

#### Build Commands

```bash
# Build k8shazgpu CLI binary
make build-k8s

# Build all components (CLI + Kubernetes components)
make build-all

# Build individual Kubernetes components
make build-controller     # DRA controller
make build-nodeagent     # Node agent
make build-kubeletplugin # Kubelet plugin

# Build and install CLI locally
make install

# Clean build artifacts
make clean
```

#### Docker Images

```bash
# Build all Docker images
make docker

# Build and push to registry
make docker
make push

# Deploy to Kubernetes cluster
make deploy

# Undeploy from cluster
make undeploy
```

### Development Workflow

#### Quick Development Cycle

For active development, use the provided script to build, push, and restart all components:

```bash
# Build everything, push images, and restart cluster components
./hack/push-and-restart.sh
```

This script:
1. Builds the k8shazgpu CLI (`make build-k8s`)
2. Builds and pushes Docker images (`make docker`)
3. Applies CRDs and RBAC
4. Restarts all cluster components:
   - `canhazgpu-controller` deployment
   - `canhazgpu-nodeagent` daemonset
   - `canhazgpu-kubeletplugin` daemonset

#### Manual Development Steps

```bash
# 1. Make code changes

# 2. Build specific component
make build-controller  # or build-nodeagent, build-kubeletplugin

# 3. Build and push Docker image
make docker

# 4. Restart specific component
kubectl rollout restart deployment/canhazgpu-controller -n canhazgpu-system
kubectl rollout restart daemonset/canhazgpu-nodeagent -n canhazgpu-system
kubectl rollout restart daemonset/canhazgpu-kubeletplugin -n canhazgpu-system

# 5. Test changes
k8shazgpu status
k8shazgpu vllm run --name test -- vllm serve facebook/opt-125m
```

#### Development Setup

```bash
# Initialize development environment
make dev-setup

# Run hello world test
make hello

# Check integration with existing canhazgpu
./build/canhazgpu status
```

### Testing

#### Test Suites
```bash
# Run all tests (includes integration tests - may be slow)
make test

# Run unit tests only (fast)
make test-short

# Run integration tests only (requires Redis/nvidia-smi)
make test-integration

# Run tests with coverage report
make test-coverage
```

#### Manual Testing
```bash
# Test basic functionality
k8shazgpu status
k8shazgpu cache status

# Test vLLM integration
k8shazgpu vllm run --name test-basic -- vllm serve facebook/opt-125m --gpu-memory-utilization 0.8

# Test cleanup
k8shazgpu cleanup --name test-basic

# Test cache operations
k8shazgpu cache add image nginx:latest
k8shazgpu cache add model facebook/opt-125m
k8shazgpu cache status

# Test from vLLM checkout (if available)
cd /path/to/vllm && k8shazgpu vllm run --name checkout-test -- vllm serve facebook/opt-125m
```

### Architecture

#### System Components

**k8shazgpu CLI**
- Built with Cobra framework
- Interfaces with Kubernetes API
- Manages ResourceClaims and cache operations
- Automatic vLLM checkout detection and diff packaging

**Controller (canhazgpu-controller)**
- Kubernetes deployment in `canhazgpu-system` namespace
- Creates vLLM pods for allocated ResourceClaims
- Manages diff ConfigMaps for checkout workflows
- Handles pod lifecycle and GPU binding

**Node Agent (canhazgpu-nodeagent)**
- DaemonSet running on each GPU node
- Manages local cache (images, git repos, models)
- Provides HTTP API for cache status
- Handles git repository synchronization and diff application

**Kubelet Plugin (canhazgpu-kubeletplugin)**
- DaemonSet implementing DRA kubelet plugin interface
- Allocates/deallocates GPUs via Redis coordination
- Provides device information to containers
- Integrates with existing canhazgpu GPU reservation system

#### Key Technologies
- **Dynamic Resource Allocation (DRA)**: Kubernetes v1.30+ feature for custom resource allocation
- **Redis**: Backend coordination with existing canhazgpu system
- **Custom Resource Definitions**: CachePlan and NodeCacheStatus CRDs
- **ConfigMaps**: Transport mechanism for vLLM checkout diffs
- **Container Registry**: Pre-built vLLM images with different base commits

#### Data Flow

1. **Resource Request**: User runs `k8shazgpu vllm run`
2. **Checkout Detection**: CLI detects vLLM checkout, packages diffs
3. **Cache Validation**: CLI ensures required images/repos are cached
4. **ResourceClaim Creation**: CLI creates ResourceClaim with annotations
5. **GPU Allocation**: Kubelet plugin allocates GPU via Redis
6. **Pod Creation**: Controller creates vLLM pod with mounted resources
7. **Diff Application**: Pod applies local changes before running command
8. **Execution**: User command runs with GPU access and modified code

#### Directory Structure
```
├── internal/k8scli/           # k8shazgpu CLI implementation
│   ├── vllm.go               # vLLM command implementation
│   ├── vllm_checkout.go      # Checkout detection and diff packaging
│   ├── cache.go              # Cache management commands
│   └── cleanup.go            # Resource cleanup
├── driver/dra/               # Kubernetes DRA implementation
│   ├── controller/           # Pod creation controller
│   ├── nodeagent/           # Cache management agent
│   └── kubeletplugin/       # DRA kubelet plugin
├── pkg/k8s/                 # Kubernetes client library
├── deploy/                  # Kubernetes manifests
└── hack/                    # Development scripts
```