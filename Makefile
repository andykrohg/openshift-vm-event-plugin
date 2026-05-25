# Root Makefile - delegates to component makefiles

# Container tool - podman or docker
CONTAINER_TOOL ?= $(shell command -v podman 2>/dev/null || echo docker)

# Image registry and tag
REGISTRY ?= quay.io
PROCESSOR_IMAGE_NAME ?= vm-event-processor
CONSOLE_IMAGE_NAME ?= vm-events-plugin
IMAGE_TAG ?= latest
IMG ?= $(REGISTRY)/$(PROCESSOR_IMAGE_NAME):$(IMAGE_TAG)
CONSOLE_IMG ?= $(REGISTRY)/$(CONSOLE_IMAGE_NAME):$(IMAGE_TAG)

.PHONY: build
build:
	@echo "Building processor..."
	@cd processor && $(MAKE) build

.PHONY: test
test:
	@echo "Running processor tests..."
	@cd processor && $(MAKE) test

.PHONY: image-build
image-build:
	@echo "Building processor container image (full build in container)..."
	@cd processor && $(MAKE) image-build IMG=$(IMG) CONTAINER_TOOL=$(CONTAINER_TOOL)

.PHONY: image-build-local
image-build-local:
	@echo "Building processor container image (using local binary)..."
	@cd processor && $(MAKE) image-build-local IMG=$(IMG) CONTAINER_TOOL=$(CONTAINER_TOOL)

.PHONY: image-push
image-push:
	@echo "Pushing processor container image..."
	@cd processor && $(MAKE) image-push IMG=$(IMG) CONTAINER_TOOL=$(CONTAINER_TOOL)

.PHONY: image-local
image-local:
	@echo "Building processor locally, then building and pushing image..."
	@cd processor && $(MAKE) image-local IMG=$(IMG) CONTAINER_TOOL=$(CONTAINER_TOOL)

# Legacy docker-* targets for backwards compatibility
.PHONY: docker-build
docker-build: image-build

.PHONY: docker-push
docker-push: image-push

.PHONY: deploy
deploy:
	@echo "Deploying to Kubernetes..."
	@cd config && kustomize edit set image vm-event-processor=$(IMG)
	@cd config && kustomize edit set image vm-events-plugin=$(CONSOLE_IMG)
	@kubectl apply -k config

.PHONY: undeploy
undeploy:
	@echo "Removing from Kubernetes..."
	@kubectl delete -k config

.PHONY: console-install
console-install:
	@echo "Installing console plugin dependencies..."
	@cd console-plugin && yarn install

.PHONY: console-build
console-build:
	@echo "Building console plugin..."
	@cd console-plugin && yarn build

.PHONY: console-build-dev
console-build-dev:
	@echo "Building console plugin (development)..."
	@cd console-plugin && yarn build-dev

.PHONY: console-start
console-start:
	@echo "Starting console plugin dev server..."
	@cd console-plugin && yarn start

.PHONY: console-lint
console-lint:
	@echo "Linting console plugin..."
	@cd console-plugin && yarn lint

.PHONY: console-image-build
console-image-build:
	@echo "Building console plugin container image (full build in container)..."
	$(CONTAINER_TOOL) build --platform linux/amd64 -t $(CONSOLE_IMG) -f console-plugin/Containerfile console-plugin

.PHONY: console-image-build-local
console-image-build-local:
	@echo "Building console plugin container image (using local dist)..."
	@if [ ! -d "console-plugin/dist" ]; then \
		echo "Error: console-plugin/dist directory not found. Run 'make console-build' first."; \
		exit 1; \
	fi
	$(CONTAINER_TOOL) build --platform linux/amd64 -t $(CONSOLE_IMG) -f console-plugin/Containerfile.local console-plugin

.PHONY: console-image-push
console-image-push:
	@echo "Pushing console plugin container image..."
	$(CONTAINER_TOOL) push $(CONSOLE_IMG)

.PHONY: console-image
console-image: console-image-build console-image-push

.PHONY: console-image-local
console-image-local: console-build console-image-build-local console-image-push

.PHONY: help
help:
	@echo "Available targets:"
	@echo ""
	@echo "Processor:"
	@echo "  build                 - Build the processor binary"
	@echo "  test                  - Run processor tests"
	@echo "  image-build           - Build processor image (full build in container)"
	@echo "  image-build-local     - Build processor image (using local binary)"
	@echo "  image-push            - Push processor container image"
	@echo "  image-local           - Build locally, then build and push image"
	@echo "  docker-build          - Alias for image-build"
	@echo "  docker-push           - Alias for image-push"
	@echo ""
	@echo "Console Plugin:"
	@echo "  console-install          - Install console plugin dependencies"
	@echo "  console-build            - Build console plugin (production)"
	@echo "  console-build-dev        - Build console plugin (development)"
	@echo "  console-start            - Start console plugin dev server"
	@echo "  console-lint             - Lint console plugin code"
	@echo "  console-image-build      - Build console plugin image (full build in container)"
	@echo "  console-image-build-local - Build console plugin image (using local dist)"
	@echo "  console-image-push       - Push console plugin container image"
	@echo "  console-image            - Build and push (full build)"
	@echo "  console-image-local      - Build locally, then build and push image"
	@echo ""
	@echo "Deployment:"
	@echo "  deploy             - Deploy to Kubernetes"
	@echo "  undeploy           - Remove from Kubernetes"
	@echo ""
	@echo "Environment variables:"
	@echo "  IMG            - Processor image (default: $(REGISTRY)/$(PROCESSOR_IMAGE_NAME):$(IMAGE_TAG))"
	@echo "  CONSOLE_IMG    - Console image (default: $(REGISTRY)/$(CONSOLE_IMAGE_NAME):$(IMAGE_TAG))"
	@echo "  REGISTRY       - Container registry (default: quay.io)"
	@echo "  IMAGE_TAG      - Image tag (default: latest)"
	@echo "  CONTAINER_TOOL - Container tool (default: auto-detect podman or docker)"
	@echo ""
	@echo "Detected container tool: $(CONTAINER_TOOL)"
