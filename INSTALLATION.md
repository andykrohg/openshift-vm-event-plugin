# OpenShift VM Activity Processor - Installation Guide

## What This Is

A simple Kubernetes application that watches VirtualMachine events and stores them in PostgreSQL for long-term retention and analysis. Includes a console plugin for viewing event history in the OpenShift Console.

## Prerequisites

- OpenShift 4.14+ or Kubernetes 1.28+
- OpenShift Virtualization (KubeVirt) installed
- `kubectl` or `oc` CLI configured

## Quick Start

### 1. Deploy Everything (No Cloning Required!)

```bash
# Deploy everything directly from GitHub
kubectl apply -k https://github.com/andykrohg/openshift-vm-activity-plugin/config

# OR deploy without kustomize
kubectl apply -f https://raw.githubusercontent.com/andykrohg/openshift-vm-activity-plugin/main/config/namespace/namespace.yaml
kubectl apply -f https://raw.githubusercontent.com/andykrohg/openshift-vm-activity-plugin/main/config/database/postgres.yaml
kubectl apply -f https://raw.githubusercontent.com/andykrohg/openshift-vm-activity-plugin/main/config/deploy/rbac.yaml
kubectl apply -f https://raw.githubusercontent.com/andykrohg/openshift-vm-activity-plugin/main/config/deploy/configmap.yaml
kubectl apply -f https://raw.githubusercontent.com/andykrohg/openshift-vm-activity-plugin/main/config/deploy/tls-proxy-config.yaml
kubectl apply -f https://raw.githubusercontent.com/andykrohg/openshift-vm-activity-plugin/main/config/deploy/deployment.yaml
kubectl apply -f https://raw.githubusercontent.com/andykrohg/openshift-vm-activity-plugin/main/config/deploy/service.yaml
kubectl apply -f https://raw.githubusercontent.com/andykrohg/openshift-vm-activity-plugin/main/config/console/plugin.yaml
kubectl apply -f https://raw.githubusercontent.com/andykrohg/openshift-vm-activity-plugin/main/config/webhook/mutating-webhook.yaml
kubectl apply -f https://raw.githubusercontent.com/andykrohg/openshift-vm-activity-plugin/main/config/samples/retention-cronjob.yaml
```

**Using Pre-built Images:**
The deployment uses pre-built images from `quay.io/andy_krohg/`:
- `vm-activity-processor:latest` - Event processor and API server
- `vm-activity-plugin:latest` - Console plugin UI

No need to build images unless you're developing custom changes!

That's it! This will deploy:
- Namespace (`vm-activity-plugin`)
- PostgreSQL database
- VM Activity Processor application (with admission webhook)
- API service (HTTPS on port 8443)
- Console plugin
- Admission webhook configuration
- Retention CronJob

### 2. Verify Installation

```bash
# Check all pods are running
kubectl get pods -n vm-activity-plugin

# Expected output:
# NAME                                  READY   STATUS    RESTARTS   AGE
# vm-activity-db-0                         1/1     Running   0          2m
# vm-activity-processor-xxxxx              2/2     Running   0          2m  (processor + nginx sidecar)
# vm-activity-plugin-xxxxx                1/1     Running   0          2m

# Check the processor logs
kubectl logs -n vm-activity-plugin deployment/vm-activity-processor -c processor

# Verify admission webhook is configured
kubectl get mutatingwebhookconfiguration vm-activity-webhook
```

### 3. Enable Console Plugin

```bash
# Enable the plugin in OpenShift Console
kubectl patch consoles.operator.openshift.io cluster \
  --type=merge \
  --patch '{"spec":{"plugins":["vm-activity-plugin"]}}'
```

### 4. Access the UI

1. Navigate to the OpenShift Console
2. Go to **Virtualization** → **VirtualMachines**
3. Click on any VirtualMachine
4. Select the **"Activity"** tab

## Configuration

All configuration is done via the ConfigMap. Edit `config/deploy/configmap.yaml` before deploying:

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

To update configuration after deployment:

```bash
# Edit the ConfigMap
kubectl edit configmap vm-activity-config -n vm-activity-plugin

# Restart the processor to pick up changes
kubectl rollout restart deployment/vm-activity-processor -n vm-activity-plugin
```

## Database Options

### Option A: Simple PostgreSQL (Default)

The default deployment uses a single PostgreSQL pod with a PersistentVolume. This is included in `make deploy`.

### Option B: Bring Your Own Database

To use an external PostgreSQL database:

1. Create a secret with credentials:
```bash
kubectl create secret generic vm-activity-db-secret \
  --from-literal=username=myuser \
  --from-literal=password=mypassword \
  --from-literal=database=vmactivity \
  -n vm-activity-plugin
```

2. Update the deployment's `DB_CONNECTION` environment variable:
```bash
kubectl set env deployment/vm-activity-processor \
  DB_CONNECTION="postgresql://myuser:mypassword@external-db.example.com:5432/vmactivity" \
  -n vm-activity-plugin
```

3. Remove the included PostgreSQL deployment:
```bash
kubectl delete statefulset vm-activity-db -n vm-activity-plugin
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

## Alternative: Clone and Deploy Locally

If you've cloned the repository:

```bash
# Using kustomize
make deploy

# OR without kustomize
make deploy-direct

# OR manually apply each file
kubectl apply -f config/namespace/namespace.yaml
kubectl apply -f config/database/postgres.yaml
kubectl apply -f config/deploy/rbac.yaml
kubectl apply -f config/deploy/configmap.yaml
kubectl apply -f config/deploy/tls-proxy-config.yaml
kubectl apply -f config/deploy/deployment.yaml
kubectl apply -f config/deploy/service.yaml
kubectl apply -f config/console/plugin.yaml
kubectl apply -f config/webhook/mutating-webhook.yaml
kubectl apply -f config/samples/retention-cronjob.yaml
```

## Testing the API

```bash
# Port-forward to the API service
kubectl port-forward -n vm-activity-plugin svc/vm-activity-api 8443:8443

# Query events for a specific VM
curl -sk https://localhost:8443/api/v1/namespaces/default/virtualmachines/my-vm/events?since=24h

# Query all events in a namespace
curl -sk https://localhost:8443/api/v1/namespaces/default/events?since=24h

# Query cluster-wide events  
curl -sk https://localhost:8443/api/v1/events?since=1h&severity=warning

# Filter by event type (VMCreated, VMUpdated, SnapshotCreated, etc.)
curl -sk https://localhost:8443/api/v1/events?reason=VMCreated

# Export events
curl -sk "https://localhost:8443/api/v1/events/export?format=csv&namespace=default" > events.csv
```

## Troubleshooting

### Processor Not Starting

Check logs:
```bash
kubectl logs -n vm-activity-plugin deployment/vm-activity-processor
```

Common issues:
- Database not ready: Wait for `vm-activity-db-0` pod to be running
- Missing secret: Ensure `vm-activity-db-secret` exists
- RBAC issues: Check ServiceAccount has proper permissions

### Database Connection Issues

Test database connectivity:
```bash
kubectl exec -n vm-activity-plugin vm-activity-db-0 -- \
  psql -U vmactivity -c "SELECT COUNT(*) FROM vm_activity;"
```

### No Events Appearing

Check if events are being filtered:
```bash
kubectl logs -n vm-activity-plugin deployment/vm-activity-processor | grep "Filtering event"
```

Review filter configuration:
```bash
kubectl get configmap vm-activity-config -n vm-activity-plugin -o yaml
```

### Console Plugin Not Showing

Verify plugin is enabled:
```bash
kubectl get consoles.operator.openshift.io cluster -o jsonpath='{.spec.plugins}'
```

Check plugin pod:
```bash
kubectl get pods -n vm-activity-plugin -l app=vm-activity-plugin
kubectl logs -n vm-activity-plugin deployment/vm-activity-plugin
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
export DB_CONNECTION="postgresql://vmactivity:changeme@localhost:5432/vmactivity?sslmode=disable"
export RETENTION_DAYS=30
export AGGREGATION_WINDOW_MINUTES=5
export FILTER_REASONS="Pulling,Pulled"

# Port-forward to database
kubectl port-forward -n vm-activity-plugin vm-activity-db-0 5432:5432

# Run locally
cd processor
go run cmd/main.go
```

### Building Custom Images (Optional)

**Note:** Building images is only required if you're making code changes. The default deployment uses pre-built images from `quay.io/andy_krohg/`.

To build and push your own images:

```bash
# Clone the repository first
git clone https://github.com/andykrohg/openshift-vm-activity-plugin.git
cd openshift-vm-activity-plugin

# Build processor binary (optional, for local testing)
make build

# Build processor container image (auto-detects podman or docker)
# Option 1: Full build in container (slower, reproducible)
make docker-build IMG=quay.io/youruser/vm-activity-processor:latest

# Option 2: Build locally then containerize (faster iteration)
make image-build-local IMG=quay.io/youruser/vm-activity-processor:latest

# Option 3: All-in-one local build + push
make image-local IMG=quay.io/youruser/vm-activity-processor:latest

# Push processor container image
make docker-push IMG=quay.io/youruser/vm-activity-processor:latest

# Build console plugin container image (full build in container)
make console-image-build CONSOLE_IMG=quay.io/youruser/vm-activity-plugin:latest

# OR: Build locally then create image (faster iteration)
make console-image-build-local CONSOLE_IMG=quay.io/youruser/vm-activity-plugin:latest

# Push console plugin container image
make console-image-push CONSOLE_IMG=quay.io/youruser/vm-activity-plugin:latest

# OR: All in one (local build + image build + push)
make console-image-local CONSOLE_IMG=quay.io/youruser/vm-activity-plugin:latest
```

**Console Plugin Build Options:**
- `console-image-build` - Full build inside container (slower, but reproducible)
- `console-image-build-local` - Build with yarn locally, copy dist/ into container (faster iteration)
- `console-image-local` - Combines `console-build` + `console-image-build-local` + `console-image-push`

