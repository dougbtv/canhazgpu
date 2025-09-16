BINARY_NAME = canhazgpu-kubeletplugin
IMAGE_TAG = quay.io/dosmith/k8shazgpu-kubeletplugin:latest

.PHONY: build
build:
	CGO_ENABLED=0 GOOS=linux go build -o $(BINARY_NAME) .

.PHONY: docker
docker: build
	podman build -t $(IMAGE_TAG) .

.PHONY: push
push: docker
	podman push $(IMAGE_TAG)

.PHONY: clean
clean:
	rm -f $(BINARY_NAME)