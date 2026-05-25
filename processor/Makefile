# Image registry and tag
REGISTRY ?= quay.io
IMAGE_NAME ?= vm-activity-processor
IMAGE_TAG ?= latest
IMG ?= $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

# Container tool - podman or docker
CONTAINER_TOOL ?= $(shell command -v podman 2>/dev/null || echo docker)

# Build the binary
.PHONY: build
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/vm-activity-processor cmd/main.go

# Run tests
.PHONY: test
test:
	go test -v ./...

# Build the container image (full build in container)
.PHONY: image-build
image-build:
	$(CONTAINER_TOOL) build --platform linux/amd64 --no-cache -t $(IMG) -f Dockerfile .

# Build the container image using local binary
.PHONY: image-build-local
image-build-local:
	@if [ ! -f "bin/vm-activity-processor" ]; then \
		echo "Error: bin/vm-activity-processor not found. Run 'make build' first."; \
		exit 1; \
	fi
	$(CONTAINER_TOOL) build --platform linux/amd64 -t $(IMG) -f Containerfile.local .

# Push the container image
.PHONY: image-push
image-push:
	$(CONTAINER_TOOL) push $(IMG)

# Build and push (full build)
.PHONY: image
image: image-build image-push

# Build locally, then build and push image
.PHONY: image-local
image-local: build image-build-local image-push

# Legacy docker-* targets for backwards compatibility
.PHONY: docker-build
docker-build: image-build

.PHONY: docker-push
docker-push: image-push

# Run locally (requires kubeconfig and database)
.PHONY: run
run:
	go run cmd/main.go

# Install dependencies
.PHONY: deps
deps:
	go mod download

# Tidy dependencies
.PHONY: tidy
tidy:
	go mod tidy

# Help
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build              - Build the binary locally"
	@echo "  test               - Run tests"
	@echo "  image-build        - Build container image (full build in container)"
	@echo "  image-build-local  - Build container image (using local binary)"
	@echo "  image-push         - Push container image"
	@echo "  image              - Build and push (full build)"
	@echo "  image-local        - Build locally, then build and push image"
	@echo "  docker-build       - Alias for image-build"
	@echo "  docker-push        - Alias for image-push"
	@echo "  run                - Run locally (requires kubeconfig and database)"
	@echo "  deps               - Download dependencies"
	@echo "  tidy               - Tidy go.mod"
	@echo ""
	@echo "Environment variables:"
	@echo "  REGISTRY        - Container registry (default: quay.io)"
	@echo "  IMAGE_NAME      - Image name (default: vm-activity-processor)"
	@echo "  IMAGE_TAG       - Image tag (default: latest)"
	@echo "  IMG             - Full image reference (default: $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG))"
	@echo "  CONTAINER_TOOL  - Container tool (default: auto-detect podman or docker)"
	@echo ""
	@echo "Detected container tool: $(CONTAINER_TOOL)"
