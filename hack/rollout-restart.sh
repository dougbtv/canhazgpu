#!/bin/bash
kubectl rollout restart deployment/canhazgpu-controller -n canhazgpu-system
kubectl rollout restart daemonset/canhazgpu-nodeagent -n canhazgpu-system
