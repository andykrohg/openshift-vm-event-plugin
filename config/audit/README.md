# Audit Webhook Configuration

> **⚠️ DEPRECATED**: This approach has been replaced by [Admission Webhooks](../webhook/README.md), which work on all Kubernetes/OpenShift platforms including managed services (ROSA HCP, ARO, OSD) without requiring control plane configuration.
>
> **Use admission webhooks instead** - they are simpler, more reliable, and platform-agnostic.

This directory contains legacy configuration files for enabling Kubernetes audit logging to capture username information for VM operations.

## Overview

The audit webhook sends VirtualMachine and VirtualMachineInstance operations (create, update, patch, delete) from the Kubernetes API server to our processor, which caches the username and associates it with corresponding Events.

**Why this is deprecated**: Audit webhooks require control plane configuration, which is not available on managed OpenShift platforms. Admission webhooks provide the same functionality without this limitation.

## Files

- **`audit-policy.yaml`**: Defines which audit events to capture (VM/VMI operations only)
- **`webhook-config.yaml`**: Configures the webhook endpoint (our processor)

## Configuration Steps

### For Managed OpenShift (ROSA, ARO, OSD)

Managed OpenShift clusters don't allow modification of control plane configuration. For these environments:

1. **Contact Red Hat Support** to request audit webhook configuration
2. Provide the `audit-policy.yaml` and `webhook-config.yaml` files
3. They will configure the API server to send audit events to your endpoint

**Alternative**: Use OpenShift audit logs instead:
- OpenShift already captures all audit events
- Query audit logs via `oc adm node-logs` or from the cluster logging stack
- Parse audit logs to extract username information

### For Self-Managed OpenShift / Kubernetes

For clusters where you control the control plane:

#### Step 1: Create ConfigMaps

```bash
# Create audit policy ConfigMap
oc create configmap audit-policy \
  --from-file=policy.yaml=audit-policy.yaml \
  -n kube-system

# Create webhook config ConfigMap  
oc create configmap audit-webhook-config \
  --from-file=webhook-config.yaml=webhook-config.yaml \
  -n kube-system
```

#### Step 2: Update API Server Configuration

Edit the kube-apiserver static pod manifest on each control plane node:

```yaml
# /etc/kubernetes/manifests/kube-apiserver.yaml
spec:
  containers:
  - name: kube-apiserver
    command:
    - kube-apiserver
    # ... existing flags ...
    - --audit-policy-file=/etc/kubernetes/audit/policy.yaml
    - --audit-webhook-config-file=/etc/kubernetes/audit/webhook-config.yaml
    - --audit-webhook-mode=batch
    - --audit-webhook-batch-max-size=100
    - --audit-webhook-batch-max-wait=5s
    volumeMounts:
    - name: audit-policy
      mountPath: /etc/kubernetes/audit
      readOnly: true
  volumes:
  - name: audit-policy
    configMap:
      name: audit-policy
  - name: audit-webhook-config
    configMap:
      name: audit-webhook-config
```

#### Step 3: Restart API Server

The kubelet will automatically restart the API server pod when the manifest changes.

## Verification

1. Check processor logs for audit events:
   ```bash
   kubectl logs -n vm-activity-plugin deployment/vm-activity-processor -c processor | grep "Cached user"
   ```

2. Start/stop a VM and verify username appears in event details:
   ```bash
   # Via the console UI - expand event details and look for "user" field
   # Or query the API directly:
   curl -s http://localhost:8080/api/v1/namespaces/NAMESPACE/virtualmachines/VM_NAME/events | jq '.events[0].enrichment.user'
   ```

## Troubleshooting

### Audit events not arriving

1. Check API server logs for webhook errors:
   ```bash
   kubectl logs -n kube-system kube-apiserver-xxx | grep audit
   ```

2. Verify network connectivity:
   ```bash
   # From a debug pod in kube-system namespace:
   curl -X POST http://vm-activity-api.vm-activity-plugin.svc:8080/audit \
     -H "Content-Type: application/json" \
     -d '{"kind":"Event","apiVersion":"audit.k8s.io/v1"}'
   ```

3. Check processor logs:
   ```bash
   kubectl logs -n vm-activity-plugin deployment/vm-activity-processor -c processor
   ```

### Username not appearing in events

1. Verify audit events are being cached:
   ```bash
   kubectl logs -n vm-activity-plugin deployment/vm-activity-processor -c processor | grep "Cached user"
   ```

2. Check cache timing - events must arrive within 10 minutes of the audit event

3. Verify the VM operation matches what's being audited (create/update/patch/delete)

## Security Considerations

- The audit webhook endpoint is internal-only (no authentication required from API server)
- For production, consider using HTTPS with proper certificate verification
- Audit policy is scoped to only VM/VMI resources to minimize overhead
- Webhook uses batch mode to reduce API server load (batches up to 100 events with 5s max wait)

## Performance Impact

The audit webhook adds minimal overhead:
- Only VM/VMI operations are audited (not general cluster traffic)
- Webhook runs in batch mode, reducing requests
- Processor caches user info in memory (10-minute TTL)
- No database writes for audit events (cache only)
