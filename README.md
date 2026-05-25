# OpenShift VM Activity Plugin

Extended event history for OpenShift Virtualization VMs with a rich console UI.

## Features

- **Long-term event retention**: Store VM events for 30+ days (configurable)
- **Synthetic event generation**: Capture VM lifecycle events (create, update, delete) and snapshot operations
- **User attribution**: Track which user performed each action via admission webhook
- **Event aggregation**: Reduce noise by aggregating duplicate events
- **Event enrichment**: Add context like user, node, configuration patches, and related resources
- **Multi-scope views**: View events per-VM, per-namespace, or cluster-wide
- **Rich console UI**: Timeline view with filtering, search, and export capabilities
- **PostgreSQL storage**: Scalable storage for millions of events
- **Flexible database options**: Simple StatefulSet (default), HA cluster, or bring your own database
- **Red Hat certified images**: Built entirely on Red Hat Universal Base Images (UBI)

## Architecture

- **Event Processor** (Go) - Watches VirtualMachine/VirtualMachineInstance/VirtualMachineSnapshot resources and Kubernetes Events, stores them in PostgreSQL
  - Admission webhook for capturing user context
  - Resource watchers for generating synthetic lifecycle events
  - Event aggregator for deduplication and enrichment
- **PostgreSQL Database** - Configurable storage (simple, HA, or external)
- **REST API** - Serves events to the console plugin (per-VM, per-namespace, or cluster-wide)
- **Console Plugin** (React/TypeScript) - Timeline UI integrated into OpenShift Console

## Project Structure

```
openshift-vm-activity-plugin/
├── processor/              # Go application
│   ├── cmd/               # Main entrypoint
│   ├── internal/          # Application logic
│   │   ├── admission/    # Admission webhook handler
│   │   ├── aggregator/   # Event processing
│   │   ├── api/          # REST API
│   │   ├── audit/        # User cache
│   │   ├── storage/      # PostgreSQL repository
│   │   └── watcher/      # Resource watchers (VM, VMI, Snapshot)
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
    ├── webhook/          # Admission webhook
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

**No cloning required!** Deploy directly from GitHub:

```bash
# Deploy everything (uses pre-built images)
kubectl apply -k https://github.com/andykrohg/openshift-vm-activity-plugin/config

# Enable console plugin
kubectl patch consoles.operator.openshift.io cluster \
  --type=merge \
  --patch '{"spec":{"plugins":["vm-activity-plugin"]}}'
```

See [INSTALLATION.md](INSTALLATION.md) for detailed installation instructions.

## Configuration

All configuration is done via ConfigMap (`config/deploy/configmap.yaml`):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: vm-activity-config
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
3. Select the "Activity" tab

### Querying Events via API

```bash
# Port-forward to the API service
kubectl port-forward -n vm-activity-plugin svc/vm-activity-api 8443:8443

# Query events for a specific VM
curl -sk https://localhost:8443/api/v1/namespaces/default/virtualmachines/my-vm/events?since=24h

# Query all events in a namespace
curl -sk https://localhost:8443/api/v1/namespaces/default/events?since=24h

# Query cluster-wide events
curl -sk https://localhost:8443/api/v1/events?since=1h&severity=warning
```

Filter by event type:
```bash
curl -sk https://localhost:8443/api/v1/events?reason=VMCreated
curl -sk https://localhost:8443/api/v1/events?reason=SnapshotFailed
```

Export events:

```bash
curl -sk "https://localhost:8443/api/v1/events/export?format=csv&namespace=default" > events.csv
```

## Development

### Building Custom Images (Optional)

Only needed if you're making code changes:

```bash
# Clone the repository
git clone https://github.com/andykrohg/openshift-vm-activity-plugin.git
cd openshift-vm-activity-plugin

# Build and push processor image (fastest - build locally + containerize + push)
make image-local IMG=quay.io/youruser/vm-activity-processor:latest

# Build and push console plugin image (fastest)
make console-image-local CONSOLE_IMG=quay.io/youruser/vm-activity-plugin:latest

# Deploy with your custom images (update manifests first)
make deploy
```

### Running Locally

```bash
# Set up environment
export DB_CONNECTION="postgresql://vmactivity:changeme@localhost:5432/vmactivity?sslmode=disable"
export RETENTION_DAYS=30
export AGGREGATION_WINDOW_MINUTES=5

# Port-forward to database
kubectl port-forward -n vm-activity-plugin vm-activity-db-0 5432:5432

# Run processor
cd processor
go run cmd/main.go
```

## Troubleshooting

### Processor Not Starting

```bash
kubectl logs -n vm-activity-plugin deployment/vm-activity-processor
```

Common issues:
- Database not ready: Wait for `vm-activity-db-0` pod
- Missing secret: Check `vm-activity-db-secret` exists
- RBAC issues: Verify ServiceAccount permissions

### Database Connection Issues

```bash
kubectl exec -n vm-activity-plugin vm-activity-db-0 -- \
  psql -U vmactivity -c "SELECT COUNT(*) FROM vm_activity;"
```

### Console Plugin Not Showing

```bash
kubectl get consoles.operator.openshift.io cluster -o jsonpath='{.spec.plugins}'
kubectl logs -n vm-activity-plugin deployment/vm-activity-plugin
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
