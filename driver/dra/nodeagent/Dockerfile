FROM registry.k8s.io/build-image/debian-base:bookworm-v1.0.3

# Install crictl for CRI operations
RUN apt-get update && \
    apt-get install -y curl && \
    CRICTL_VERSION="v1.28.0" && \
    curl -L "https://github.com/kubernetes-sigs/cri-tools/releases/download/${CRICTL_VERSION}/crictl-${CRICTL_VERSION}-linux-amd64.tar.gz" | tar -C /usr/local/bin -xz && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

COPY canhazgpu-nodeagent /canhazgpu-nodeagent

ENTRYPOINT ["/canhazgpu-nodeagent"]