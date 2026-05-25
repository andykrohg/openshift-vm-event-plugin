# Build the application binary
FROM registry.access.redhat.com/ubi9/go-toolset:latest AS builder

# Switch to root to set up the workspace
USER 0

WORKDIR /opt/app-root/src

# Copy the Go Modules manifests
COPY --chown=1001:0 go.mod go.mod
COPY --chown=1001:0 go.sum go.sum

# Switch back to default user
USER 1001

# Download dependencies
RUN go mod download

# Copy the go source
COPY --chown=1001:0 cmd/ cmd/
COPY --chown=1001:0 internal/ internal/

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -a -o vm-activity-processor cmd/main.go

# Use Red Hat Universal Base Image (UBI) Micro as minimal base image
FROM registry.access.redhat.com/ubi9/ubi-micro:latest

WORKDIR /

# Copy the binary from builder
COPY --from=builder /opt/app-root/src/vm-activity-processor .

# Run as non-root user
USER 65532:65532

ENTRYPOINT ["/vm-activity-processor"]
