# Variables
BINARY_NAME=canhazgpu
BUILD_DIR=build
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X main.version=$(VERSION)"

# help text
.PHONY: help
help:
	@echo "Go build targets:"
	@echo "  make build        - Build the canhazgpu binary"
	@echo "  make build-k8s    - Build the k8shazgpu binary"
	@echo "  make build-all    - Build all binaries (canhazgpu, k8shazgpu, controller, nodeagent)"
	@echo "  make install      - Build and install to /usr/local/bin"
	@echo "  make clean        - Clean build artifacts"
	@echo "  make test         - Run tests (includes integration tests - may be slow)"
	@echo "  make test-short   - Run tests (skip integration tests - fast)"
	@echo "  make test-coverage- Run tests with coverage report"
	@echo "  make test-integration - Run integration tests only (requires Redis/nvidia-smi)"
	@echo "  make deps         - Download Go dependencies"
	@echo "  make fmt          - go fmt"
	@echo ""
	@echo "Kubernetes targets:"
	@echo "  make build-controller - Build DRA controller"
	@echo "  make build-nodeagent  - Build node agent"
	@echo "  make docker           - Build Docker images"
	@echo "  make deploy           - Deploy to Kubernetes"
	@echo "  make undeploy         - Remove from Kubernetes"
	@echo "  make hello            - Run hello world example"
	@echo "  make dev-setup        - Initialize Redis and GPU pool"
	@echo ""
	@echo "Documentation targets:"
	@echo "  make docs-deps    - Install documentation dependencies"
	@echo "  make docs         - Build documentation with MkDocs"
	@echo "  make docs-preview - Build and serve documentation locally"
	@echo "  make docs-clean   - Clean documentation build files"

.PHONY: deps
deps:
	@echo "Downloading Go dependencies"
	@go mod download
	@go mod tidy

.PHONY: build
build: deps
	@echo "Building $(BINARY_NAME)"
	@mkdir -p $(BUILD_DIR)
	@go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) main.go

.PHONY: install
install: build
	@echo "Installing $(BINARY_NAME) to /usr/local/bin"
	@sudo cp -v $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)
	@sudo cp -v ./autocomplete_canhazgpu.sh /etc/bash_completion.d/autocomplete_canhazgpu.sh

.PHONY: clean
clean: clean-k8s
	@echo "Cleaning build artifacts"
	@rm -rf $(BUILD_DIR)

.PHONY: lint
lint:
	@echo "Running lint"
	@if ! command -v golangci-lint >/dev/null 2>&1 ; then curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(shell go env GOPATH)/bin v2.2.1 ; fi
	@echo "Using golangci-lint version: $$(golangci-lint --version | head -n 1)"
	@golangci-lint run

.PHONY: fmt
fmt:
	@echo "Running fmt"
	@go fmt ./...

.PHONY: test
test:
	@echo "Running tests"
	@go test -v ./...

.PHONY: test-short
test-short:
	@echo "Running tests (short mode, skipping integration tests)"
	@go test -short -v ./...

.PHONY: test-coverage
test-coverage:
	@echo "Running tests with coverage"
	@go test -v -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated at coverage.html"

.PHONY: test-integration
test-integration:
	@echo "Running integration tests (requires Redis)"
	@go test -v ./... -run Integration

.PHONY: docs-deps
docs-deps:
	@echo "Installing documentation dependencies"
	@pip install -r requirements-docs.txt

.PHONY: docs
docs:
	@echo "Building documentation with MkDocs"
	@command -v mkdocs >/dev/null 2>&1 || { echo "Error: mkdocs not found. Install with: make docs-deps"; exit 1; }
	@mkdocs build || { echo "Error: MkDocs build failed. Install dependencies with: make docs-deps"; exit 1; }

.PHONY: docs-preview
docs-preview:
	@echo "Starting MkDocs development server"
	@command -v mkdocs >/dev/null 2>&1 || { echo "Error: mkdocs not found. Install with: make docs-deps"; exit 1; }
	@echo "Documentation will be available at: http://127.0.0.1:8000"
	@echo "Press Ctrl+C to stop the server"
	@mkdocs serve || { echo "Error: MkDocs serve failed. Install dependencies with: make docs-deps"; exit 1; }

.PHONY: docs-clean
docs-clean:
	@echo "Cleaning documentation build files"
	@rm -rf site/

# Kubernetes targets
.PHONY: build-k8s
build-k8s: deps
	@echo "Building k8shazgpu"
	@mkdir -p $(BUILD_DIR)
	@go build $(LDFLAGS) -o $(BUILD_DIR)/k8shazgpu ./cmd/k8shazgpu

.PHONY: build-controller
build-controller:
	@echo "Building DRA controller"
	@$(MAKE) -C driver/dra/controller build

.PHONY: build-nodeagent
build-nodeagent:
	@echo "Building node agent"
	@$(MAKE) -C driver/dra/nodeagent build

.PHONY: build-all
build-all: build build-k8s build-controller build-nodeagent

.PHONY: docker
docker: build-controller build-nodeagent
	@echo "Building Docker images"
	@$(MAKE) -C driver/dra/controller docker
	@$(MAKE) -C driver/dra/nodeagent docker

.PHONY: clean-k8s
clean-k8s:
	@echo "Cleaning k8s build artifacts"
	@$(MAKE) -C driver/dra/controller clean
	@$(MAKE) -C driver/dra/nodeagent clean

.PHONY: deploy
deploy:
	@echo "Deploying to Kubernetes"
	@kubectl apply -f deploy/namespace.yaml
	@kubectl apply -f deploy/rbac.yaml
	@kubectl apply -f deploy/resourceclass.yaml
	@kubectl apply -f deploy/controller.yaml
	@kubectl apply -f deploy/daemonset.yaml

.PHONY: undeploy
undeploy:
	@echo "Removing from Kubernetes"
	@kubectl delete -f deploy/daemonset.yaml --ignore-not-found
	@kubectl delete -f deploy/controller.yaml --ignore-not-found
	@kubectl delete -f deploy/resourceclass.yaml --ignore-not-found
	@kubectl delete -f deploy/rbac.yaml --ignore-not-found
	@kubectl delete -f deploy/namespace.yaml --ignore-not-found

.PHONY: hello
hello: build-k8s
	@echo "Running hello world example"
	@cd examples/hello && ./run.sh

.PHONY: dev-setup
dev-setup: build
	@echo "Setting up development environment"
	@echo "Initializing GPU pool (assuming Redis is running)"
	@$(BUILD_DIR)/$(BINARY_NAME) admin --gpus 1 --force

.PHONY: validate
validate:
	@echo "=== k8shazgpu Validation Commands ==="
	@echo "# Build all components:"
	@echo "make build-all"
	@echo ""
	@echo "# Deploy to Kubernetes:"
	@echo "make deploy"
	@echo ""
	@echo "# Test end-to-end:"
	@echo "make hello"
	@echo ""
	@echo "# Check status:"
	@echo "kubectl get resourceclaims"
	@echo "kubectl get pods"
	@echo ""
	@echo "# Check interop with local canhazgpu:"
	@echo "./build/canhazgpu status"
