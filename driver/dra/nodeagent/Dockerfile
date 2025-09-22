FROM registry.k8s.io/build-image/debian-base:bookworm-v1.0.3

# Install crictl for CRI operations, git for repository cloning, and huggingface CLI for model management
RUN apt-get update && \
    apt-get install -y curl git python3 python3-pip && \
    CRICTL_VERSION="v1.28.0" && \
    curl -L "https://github.com/kubernetes-sigs/cri-tools/releases/download/${CRICTL_VERSION}/crictl-${CRICTL_VERSION}-linux-amd64.tar.gz" | tar -C /usr/local/bin -xz && \
    pip3 install --break-system-packages -U "huggingface_hub[cli]" && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* && \
    rm -rf /root/.cache/pip

COPY canhazgpu-nodeagent /canhazgpu-nodeagent

ENTRYPOINT ["/canhazgpu-nodeagent"]