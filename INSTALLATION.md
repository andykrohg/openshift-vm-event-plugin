# OpenShift VM Event Processor - Installation Guide

## What This Is

A simple Kubernetes application that watches VirtualMachine events and stores them in PostgreSQL for long-term retention and analysis. Includes a console plugin for viewing event history in the OpenShift Console.

## Prerequisites

- OpenShift 4.14+ or Kubernetes 1.28+
- OpenShift Virtualization (KubeVirt) installed
- Red Hat registry access (pre-configured on OpenShift clusters)
- `kubectl` or `oc` CLI configured
- `kustomize` installed (or use `kubectl apply -k`)
- `podman` or `docker` for building container images

## Quick Start

### 1. Deploy Everything

```bash
# Clone the repository
git clone https://github.com/andykrohg/openshift-vm-event-plugin.git
cd openshift-vm-event-plugin

# Build and push images (auto-detects podman or docker)
make docker-build docker-push IMG=quay.io/youruser/vm-event-processor:latest
make console-image-build console-image-push CONSOLE_IMG=quay.io/youruser/vm-events-plugin:latest

# Deploy everything with kustomize
make deploy IMG=quay.io/youruser/vm-event-processor:latest CONSOLE_IMG=quay.io/youruser/vm-events-plugin:latest
```

That's it! This will deploy:
- Namespace (`vm-event-operator-system`)
- PostgreSQL database
- VM Event Processor application
- API service
- Console plugin
- Retention CronJob

### 2. Verify Installation

```bash
# Check all pods are running
kubectl get pods -n vm-event-operator-system

# Expected output:
# NAME                                  READY   STATUS    RESTARTS   AGE
# vm-event-db-0                         1/1     Running   0          2m
# vm-event-processor-xxxxx              1/1     Running   0          2m
# vm-events-plugin-xxxxx                1/1     Running   0          2m

# Check the processor logs
kubectl logs -n vm-event-operator-system deployment/vm-event-processor
```

### 3. Enable Console Plugin

```bash
# Enable the plugin in OpenShift Console
kubectl patch consoles.operator.openshift.io cluster \
  --type=merge \
  --patch '{"spec":{"plugins":["vm-events-plugin"]}}'
```

### 4. Access the UI

1. Navigate to the OpenShift Console
2. Go to **Virtualization** → **VirtualMachines**
3. Click on any VirtualMachine
4. Select the **"Event History"** tab

## Configuration

All configuration is done via the ConfigMap. Edit `config/deploy/configmap.yaml` before deploying:

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

To update configuration after deployment:

```bash
# Edit the ConfigMap
kubectl edit configmap vm-event-config -n vm-event-operator-system

# Restart the processor to pick up changes
kubectl rollout restart deployment/vm-event-processor -n vm-event-operator-system
```

## Database Options

### Option A: Simple PostgreSQL (Default)

The default deployment uses a single PostgreSQL pod with a PersistentVolume. This is included in `make deploy`.

### Option B: Bring Your Own Database

To use an external PostgreSQL database:

1. Create a secret with credentials:
```bash
kubectl create secret generic vm-event-db-secret \
  --from-literal=username=myuser \
  --from-literal=password=mypassword \
  --from-literal=database=vmevent \
  -n vm-event-operator-system
```

2. Update the deployment's `DB_CONNECTION` environment variable:
```bash
kubectl set env deployment/vm-event-processor \
  DB_CONNECTION="postgresql://myuser:mypassword@external-db.example.com:5432/vmevent" \
  -n vm-event-operator-system
```

3. Remove the included PostgreSQL deployment:
```bash
kubectl delete statefulset vm-event-db -n vm-event-operator-system
```

### Option C: High-Availability PostgreSQL

For production, use CloudNativePG:

```bash
# Install CloudNativePG operator
kubectl apply -f https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.23/releases/cnpg-1.23.0.yaml

# Deploy 3-node HA cluster
kubectl apply -f config/database/postgres-cluster-ha.yaml

# Update secret name in deployment if needed
```

## Manual Deployment Steps

If you prefer to deploy components individually:

```bash
# 1. Create namespace
kubectl apply -f config/namespace/namespace.yaml

# 2. Deploy PostgreSQL
kubectl apply -f config/database/postgres.yaml

# 3. Deploy RBAC
kubectl apply -f config/deploy/rbac.yaml

# 4. Deploy ConfigMap
kubectl apply -f config/deploy/configmap.yaml

# 5. Deploy the processor
kubectl apply -f config/deploy/deployment.yaml

# 6. Deploy the API service
kubectl apply -f config/deploy/service.yaml

# 7. Deploy console plugin
# Note: Ensure you've built and pushed the console plugin image first
# See "Building" section below
kubectl apply -f config/console/plugin.yaml

# 8. Deploy retention CronJob
kubectl apply -f config/samples/retention-cronjob.yaml
```

## Testing the API

```bash
# Port-forward to the API service
kubectl port-forward -n vm-event-operator-system svc/vm-events-api 8080:8080

# Query events (in another terminal)
curl http://localhost:8080/api/v1/namespaces/default/virtualmachines/my-vm/events?since=24h

# Export events
curl "http://localhost:8080/api/v1/events/export?format=csv&namespace=default" > events.csv
```

## Troubleshooting

### Processor Not Starting

Check logs:
```bash
kubectl logs -n vm-event-operator-system deployment/vm-event-processor
```

Common issues:
- Database not ready: Wait for `vm-event-db-0` pod to be running
- Missing secret: Ensure `vm-event-db-secret` exists
- RBAC issues: Check ServiceAccount has proper permissions

### Database Connection Issues

Test database connectivity:
```bash
kubectl exec -n vm-event-operator-system vm-event-db-0 -- \
  psql -U vmevent -c "SELECT COUNT(*) FROM vm_events;"
```

### No Events Appearing

Check if events are being filtered:
```bash
kubectl logs -n vm-event-operator-system deployment/vm-event-processor | grep "Filtering event"
```

Review filter configuration:
```bash
kubectl get configmap vm-event-config -n vm-event-operator-system -o yaml
```

### Console Plugin Not Showing

Verify plugin is enabled:
```bash
kubectl get consoles.operator.openshift.io cluster -o jsonpath='{.spec.plugins}'
```

Check plugin pod:
```bash
kubectl get pods -n vm-event-operator-system -l app=vm-events-plugin
kubectl logs -n vm-event-operator-system deployment/vm-events-plugin
```

## Uninstall

```bash
# Remove everything
make undeploy

# Or manually
kubectl delete -k config
```

## Development

### Running Locally

```bash
# Set up database connection
export DB_CONNECTION="postgresql://vmevent:changeme@localhost:5432/vmevent?sslmode=disable"
export RETENTION_DAYS=30
export AGGREGATION_WINDOW_MINUTES=5
export FILTER_REASONS="Pulling,Pulled"

# Port-forward to database
kubectl port-forward -n vm-event-operator-system vm-event-db-0 5432:5432

# Run locally
cd processor
go run cmd/main.go
```

### Building

```bash
# Build processor binary
make build

# Build processor container image (auto-detects podman or docker)
# Option 1: Full build in container (slower, reproducible)
make docker-build IMG=quay.io/youruser/vm-event-processor:latest

# Option 2: Build locally then containerize (faster iteration)
make image-build-local IMG=quay.io/youruser/vm-event-processor:latest

# Option 3: All-in-one local build + push
make image-local IMG=quay.io/youruser/vm-event-processor:latest

# Push processor container image
make docker-push IMG=quay.io/youruser/vm-event-processor:latest

# Build console plugin container image (full build in container)
make console-image-build CONSOLE_IMG=quay.io/youruser/vm-events-plugin:latest

# OR: Build locally then create image (faster iteration)
make console-image-build-local CONSOLE_IMG=quay.io/youruser/vm-events-plugin:latest

# Push console plugin container image
make console-image-push CONSOLE_IMG=quay.io/youruser/vm-events-plugin:latest

# OR: All in one (local build + image build + push)
make console-image-local CONSOLE_IMG=quay.io/youruser/vm-events-plugin:latest
```

**Console Plugin Build Options:**
- `console-image-build` - Full build inside container (slower, but reproducible)
- `console-image-build-local` - Build with yarn locally, copy dist/ into container (faster iteration)
- `console-image-local` - Combines `console-build` + `console-image-build-local` + `console-image-push`

