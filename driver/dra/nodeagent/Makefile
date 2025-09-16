BINARY_NAME=canhazgpu-nodeagent
DOCKER_IMAGE=quay.io/dosmith/k8shazgpu-nodeagent
VERSION?=latest
CONTAINER_RUNTIME?=podman

.PHONY: build
build:
	CGO_ENABLED=0 GOOS=linux go build -o $(BINARY_NAME) .

.PHONY: docker
docker: build
	$(CONTAINER_RUNTIME) build -t $(DOCKER_IMAGE):$(VERSION) .

.PHONY: clean
clean:
	rm -f $(BINARY_NAME)

.PHONY: test
test:
	go test ./...

push: docker
	$(CONTAINER_RUNTIME) push $(DOCKER_IMAGE):$(VERSION)