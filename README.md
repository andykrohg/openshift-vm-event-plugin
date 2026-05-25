# OpenShift VM Event Plugin

Extended event history for OpenShift Virtualization VMs with a rich console UI.

## Features

- **Long-term event retention**: Store VM events for 30+ days (configurable)
- **Event aggregation**: Reduce noise by aggregating duplicate events
- **Event enrichment**: Add context like user, node, and related resources
- **Rich console UI**: Timeline view with filtering, search, and export capabilities
- **PostgreSQL storage**: Scalable storage for millions of events
- **Flexible database options**: Simple StatefulSet (default), HA cluster, or bring your own database
- **Red Hat certified images**: Built entirely on Red Hat Universal Base Images (UBI)

## Architecture

- **Event Processor** (Go) - Watches VirtualMachine/VirtualMachineInstance events and stores them in PostgreSQL
- **PostgreSQL Database** - Configurable storage (simple, HA, or external)
- **REST API** - Serves events to the console plugin
- **Console Plugin** (React/TypeScript) - Timeline UI integrated into OpenShift Console

## Project Structure

```
openshift-vm-event-plugin/
├── processor/              # Go application
│   ├── cmd/               # Main entrypoint
│   ├── internal/          # Application logic
│   │   ├── aggregator/   # Event processing
│   │   ├── api/          # REST API
│   │   ├── storage/      # PostgreSQL repository
│   │   └── watcher/      # Event watcher
│   ├── Dockerfile
│   ├── Makefile
│   └── go.mod
├── console-plugin/        # React UI
│   ├── src/
│   ├── package.json
│   └── webpack.config.js
└── config/                # Kubernetes manifests
    ├── deploy/           # Application deployment
    ├── database/         # PostgreSQL
    ├── console/          # Console plugin
    └── samples/          # Examples
```

## Container Images

All container images use Red Hat Universal Base Images (UBI) and Red Hat certified images:

- **Processor**: `registry.access.redhat.com/ubi9/ubi-micro` (runtime) + `ubi9/go-toolset` (builder)
- **PostgreSQL**: `registry.redhat.io/rhel9/postgresql-16`
- **Console Plugin**: `registry.access.redhat.com/ubi9/nginx-124`

## Prerequisites

- OpenShift 4.14+ or Kubernetes 1.28+
- OpenShift Virtualization (KubeVirt) installed
- Red Hat registry access (pre-configured on OpenShift clusters)

## Quick Start

```bash
# Clone the repository
git clone https://github.com/andykrohg/openshift-vm-event-plugin.git
cd openshift-vm-event-plugin

# Build and push images (auto-detects podman or docker)
make docker-build docker-push IMG=quay.io/youruser/vm-event-processor:latest
make console-image-build console-image-push CONSOLE_IMG=quay.io/youruser/vm-events-plugin:latest

# Deploy everything
make deploy IMG=quay.io/youruser/vm-event-processor:latest CONSOLE_IMG=quay.io/youruser/vm-events-plugin:latest

# Enable console plugin
kubectl patch consoles.operator.openshift.io cluster \
  --type=merge \
  --patch '{"spec":{"plugins":["vm-events-plugin"]}}'
```

See [INSTALLATION.md](INSTALLATION.md) for detailed installation instructions.

## Configuration

All configuration is done via ConfigMap (`config/deploy/configmap.yaml`):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: vm-event-config
data:
  RETENTION_DAYS: "30"                    # How long to keep events
  AGGREGATION_WINDOW_MINUTES: "5"        # Time window for deduplication
  API_PORT: "8080"                       # API server port
  FILTER_REASONS: "Pulling,Pulled"       # Comma-separated reasons to ignore
```

## Usage

### Viewing Events in the Console

1. Navigate to Virtualization → VirtualMachines
2. Click on a VirtualMachine
3. Select the "Event History" tab

### Querying Events via API

```bash
kubectl port-forward -n vm-event-operator-system svc/vm-events-api 8080:8080

curl http://localhost:8080/api/v1/namespaces/default/virtualmachines/my-vm/events?since=24h
```

Export events:

```bash
curl "http://localhost:8080/api/v1/events/export?format=csv&namespace=default" > events.csv
```

## Development

### Building Components

```bash
# Build processor binary
make build

# Build processor container image
# Full build in container (slower, reproducible)
make docker-build IMG=quay.io/youruser/vm-event-processor:latest
# OR: Build locally then containerize (faster)
make image-local IMG=quay.io/youruser/vm-event-processor:latest

# Build console plugin (local development)
make console-build

# Build console plugin container image
# Full build in container
make console-image-build CONSOLE_IMG=quay.io/youruser/vm-events-plugin:latest
# OR: Build locally then containerize (faster)
make console-image-local CONSOLE_IMG=quay.io/youruser/vm-events-plugin:latest
```

### Running Locally

```bash
# Set up environment
export DB_CONNECTION="postgresql://vmevent:changeme@localhost:5432/vmevent?sslmode=disable"
export RETENTION_DAYS=30
export AGGREGATION_WINDOW_MINUTES=5

# Port-forward to database
kubectl port-forward -n vm-event-operator-system vm-event-db-0 5432:5432

# Run processor
cd processor
go run cmd/main.go
```

## Troubleshooting

### Processor Not Starting

```bash
kubectl logs -n vm-event-operator-system deployment/vm-event-processor
```

Common issues:
- Database not ready: Wait for `vm-event-db-0` pod
- Missing secret: Check `vm-event-db-secret` exists
- RBAC issues: Verify ServiceAccount permissions

### Database Connection Issues

```bash
kubectl exec -n vm-event-operator-system vm-event-db-0 -- \
  psql -U vmevent -c "SELECT COUNT(*) FROM vm_events;"
```

### Console Plugin Not Showing

```bash
kubectl get consoles.operator.openshift.io cluster -o jsonpath='{.spec.plugins}'
kubectl logs -n vm-event-operator-system deployment/vm-events-plugin
```

## Uninstall

```bash
make undeploy
```

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
