#!/bin/bash
make build-k8s
make docker
kubectl rollout restart deployment/canhazgpu-controller -n canhazgpu-system
kubectl rollout restart daemonset/canhazgpu-nodeagent -n canhazgpu-system
kubectl rollout restart daemonset/canhazgpu-kubeletplugin -n canhazgpu-system
