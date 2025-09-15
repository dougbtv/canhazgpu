BINARY_NAME=canhazgpu-controller
DOCKER_IMAGE=canhazgpu/controller
VERSION?=latest

.PHONY: build
build:
	CGO_ENABLED=0 GOOS=linux go build -o $(BINARY_NAME) .

.PHONY: docker
docker: build
	docker build -t $(DOCKER_IMAGE):$(VERSION) .

.PHONY: clean
clean:
	rm -f $(BINARY_NAME)

.PHONY: test
test:
	go test ./...