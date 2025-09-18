canhazgpu run --gpus 2 -- \



export MIDSTREAM_IMAGE=quay.io/vllm/automation-vllm:cuda-17646160079
export MIDSTREAM_IMAGE=quay.io/vllm/automation-vllm:cuda-17675859974
export UPSTREAM_IMAGE=public.ecr.aws/q9t5s3a7/vllm-ci-postmerge-repo:7a1c4025f1e2879fa398888d70c596e5818026cb

docker run --rm -it \
  --device=nvidia.com/gpu=all \
  --ipc=host \
  --shm-size=16g \
  -p 8000:8000 \
  -v /raid/engine/doug/models:/models:ro \
  -e CUDA_VISIBLE_DEVICES \
  -e VLLM_NO_USAGE_STATS=1 \
  -e PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True \
  -e HF_HUB_OFFLINE=1 \
  ${MIDSTREAM_IMAGE} \
  vllm serve Qwen-SGlang/Qwen3-Next-80B-A3B-Instruct \
    --tensor-parallel-size 2 \
    --enable-chunked-prefill \
    --trust-remote-code \
    --tokenizer-mode auto \
    --speculative-config '{"method":"qwen3_next_mtp","num_speculative_tokens":2}'



docker run --rm -it \
  --device=nvidia.com/gpu=all \
  --ipc=host \
  --shm-size=16g \
  -p 8000:8000 \
  -v /raid/engine/doug/models:/models:ro \
  -e CUDA_VISIBLE_DEVICES \
  -e VLLM_NO_USAGE_STATS=1 \
  -e PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True \
  -e HF_HUB_OFFLINE=1 \
  ${MIDSTREAM_IMAGE} \
  --model Qwen-SGlang/Qwen3-Next-80B-A3B-Instruct \
  --tensor-parallel-size 2 \
  --enable-chunked-prefill \
  --trust-remote-code \
  --tokenizer-mode auto \
  --speculative-config '{"method":"qwen3_next_mtp","num_speculative_tokens":2}'




docker run --rm -it \
  --device=nvidia.com/gpu=all \
  --ipc=host \
  --shm-size=16g \
  -p 8000:8000 \
  -v /raid/engine/doug/models:/models:ro \
  -e CUDA_VISIBLE_DEVICES \
  -e VLLM_NO_USAGE_STATS=1 \
  -e PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True \
  -e HF_HUB_OFFLINE=1 \
  ${MIDSTREAM_IMAGE} \
  --model /models/Qwen-SGlang/Qwen3-Next-80B-A3B-Instruct \
  --tensor-parallel-size 2 \
  --enable-chunked-prefill \
  --trust-remote-code \
  --tokenizer-mode auto \
  --speculative-config '{"method":"qwen3_next_mtp","num_speculative_tokens":2}'


docker run --rm -it \
  --device=nvidia.com/gpu=all \
  --shm-size=4g \
  -p 8000:8000 \
  -e HF_HUB_OFFLINE=1 \
  -v /raid/engine/doug/models:/models:ro \
  ${MIDSTREAM_IMAGE} \
  --model /models/Qwen-SGlang/Qwen3-Next-80B-A3B-Instruct \
  --tensor-parallel-size 2 \
  --enable-auto-tool-choice \
  --tool-call-parser hermes \
  --uvicorn-log-level debug


docker run --rm -it \
  --device=nvidia.com/gpu=all \
  --shm-size=4g \
  -p 8000:8000 \
  -e HF_HUB_OFFLINE=1 \
  -v /raid/engine/doug/models:/models:ro \
  -v /usr/local/cuda/bin/nvcc:/usr/local/cuda/bin/nvcc:ro \
  ${MIDSTREAM_IMAGE} \
  --model /models/Qwen-SGlang/Qwen3-Next-80B-A3B-Instruct \
  --tensor-parallel-size 2 \
  --enable-auto-tool-choice \
  --tool-call-parser hermes \
  --uvicorn-log-level debug


from Tarun:

```
$ podman run --rm -it \
  --device nvidia.com/gpu=all \
  --security-opt=label=disable \
  --shm-size=4g \
  -p 8000:8000 \
  --userns=keep-id:uid=1001 \
  --env "HUGGING_FACE_HUB_TOKEN=$HF_TOKEN" \
  --env "HF_HUB_OFFLINE=0" \
  -v ./rhaiis-cache:/opt/app-root/src/.cache:Z \
  <image> \
  --model Qwen/Qwen3-Next-80B-A3B-Instruct\
  --tensor-parallel-size 4 \ 
  --enable-auto-tool-choice \
  --tool-call-parser hermes \
  --uvicorn-log-level debug

```

---

and latest: 

docker run --rm -it \
  --device=nvidia.com/gpu=all \
  --ipc=host \
  --shm-size=16g \
  -p 8000:8000 \
  -v /raid/engine/doug/models/Qwen-SGlang:/models:ro \
  -e CUDA_VISIBLE_DEVICES \
  -e VLLM_NO_USAGE_STATS=1 \
  -e PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True \
  -e HF_HUB_OFFLINE=1 \
  quay.io/vllm/automation-vllm:cuda-17646160079 \
  vllm serve /models/Qwen3-Next-80B-A3B-Instruct \
    --tensor-parallel-size 2 \
    --enable-chunked-prefill \
    --trust-remote-code \
    --tokenizer-mode auto \
    --speculative-config '{"method":"qwen3_next_mtp","num_speculative_tokens":2}'



#  --device=nvidia.com/gpu=all \

podman run --rm -it \
  --gpus=all \
  --ipc=host \
  -p 8000:8000 \
  -e PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True \
  -e HF_HUB_OFFLINE=1 \
  public.ecr.aws/q9t5s3a7/vllm-ci-postmerge-repo:02d4b854543c3b2c65435a5ed9bb1c3a9856cfad \
  vllm serve facebook/opt-125m \
    --gpu-memory-utilization 0.8
