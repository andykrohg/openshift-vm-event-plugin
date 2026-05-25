# Admission Webhook Configuration

This directory contains the MutatingWebhookConfiguration that enables the VM event processor to capture username information for VM operations.

## Overview

The admission webhook intercepts VirtualMachine, VirtualMachineInstance, and VirtualMachineSnapshot create/update/delete operations and caches the user information. This approach works on **any** Kubernetes/OpenShift platform, including managed services like ROSA HCP, ARO, and OSD.

**Key behavior**: The webhook only caches **human users**, not service accounts. This prevents controller operations from overwriting the original human user who triggered an action.

## Advantages over Audit Webhooks

- ✅ **No control plane configuration needed** - works on managed OpenShift
- ✅ **Simpler setup** - just apply a WebhookConfiguration resource
- ✅ **Platform agnostic** - works on any Kubernetes 1.16+
- ✅ **More direct** - captures user at the exact moment of VM modification
- ✅ **Lower overhead** - only processes VM/VMI resources

## Files

- **`mutating-webhook.yaml`**: MutatingWebhookConfiguration resource

## Setup

### Prerequisites

1. VM event processor deployed and running
2. Service `vm-activity-api` exposed on port 8443 with TLS

### Deploy the Webhook

```bash
# Apply the webhook configuration
kubectl apply -f mutating-webhook.yaml
```

The webhook configuration uses OpenShift's automatic CA bundle injection via the annotation:
```yaml
service.beta.openshift.io/inject-cabundle: "true"
```

This automatically configures the webhook to trust the service's TLS certificate.

### For vanilla Kubernetes

If you're not on OpenShift, you'll need to manually set the `caBundle` field:

```bash
# Get the CA bundle from the service certificate
CA_BUNDLE=$(kubectl get secret vm-activity-api-cert -n vm-activity-plugin -o jsonpath='{.data.ca\.crt}')

# Update the webhook configuration
kubectl patch mutatingwebhookconfiguration vm-activity-webhook \
  --type='json' \
  -p="[{'op': 'add', 'path': '/webhooks/0/clientConfig/caBundle', 'value':'$CA_BUNDLE'}]"
```

## Verification

1. Check webhook is registered:
   ```bash
   kubectl get mutatingwebhookconfiguration vm-activity-webhook
   ```

2. Create or update a VirtualMachine:
   ```bash
   kubectl apply -f - <<EOF
   apiVersion: kubevirt.io/v1
   kind: VirtualMachine
   metadata:
     name: test-vm
     namespace: default
   spec:
     running: false
     template:
       metadata:
         labels:
           kubevirt.io/vm: test-vm
       spec:
         domain:
           devices:
             disks:
             - disk:
                 bus: virtio
               name: containerdisk
           resources:
             requests:
               memory: 64M
         volumes:
         - containerDisk:
             image: quay.io/kubevirt/cirros-container-disk-demo
           name: containerdisk
   EOF
   ```

3. Check processor logs for cached user:
   ```bash
   kubectl logs -n vm-activity-plugin deployment/vm-activity-processor -c processor | grep "Cached user"
   ```

   You should see output like:
   ```
   I0524 10:15:30.123456 webhook.go:120] Cached user system:admin for VirtualMachine/default/test-vm (operation: CREATE)
   ```

4. Verify username appears in event enrichment:
   ```bash
   # Start/stop the VM to generate events
   kubectl patch vm test-vm --type merge -p '{"spec":{"running":true}}'
   
   # Check events via the console UI or API
   curl -s https://vm-activity-api.vm-activity-plugin.svc:8443/api/v1/namespaces/default/virtualmachines/test-vm/events | jq '.events[0].enrichment.user'
   ```

## How It Works

1. When a user creates, updates, or deletes a VirtualMachine/VirtualMachineInstance/VirtualMachineSnapshot, the Kubernetes API server sends an AdmissionReview request to the webhook endpoint
2. The webhook handler extracts user information from the AdmissionRequest:
   - Username (e.g., `user@example.com`)
   - UID
   - Groups
3. **Service accounts are filtered out** - only human users are cached (prevents controllers from overwriting the original user)
4. User info is cached in memory with a 10-minute TTL, keyed by resource kind, namespace, and name
5. The webhook responds with "Allowed: true" (it observes but doesn't mutate)
6. When the resource watchers generate synthetic events (VMCreated, SnapshotDeleted, etc.) or when Kubernetes Events are processed, the enrichment logic looks up the cached user info
7. Events are enriched with the username before being stored and displayed

## Troubleshooting

### Webhook not being called

Check webhook configuration:
```bash
kubectl describe mutatingwebhookconfiguration vm-activity-webhook
```

Verify the service and endpoint are accessible:
```bash
# From within the cluster
kubectl run test-curl --rm -i --restart=Never --image=registry.access.redhat.com/ubi9/ubi-minimal -- \
  curl -k https://vm-activity-api.vm-activity-plugin.svc:8443/api/v1/health
```

### CA bundle not injected (OpenShift)

Check the annotation is present:
```bash
kubectl get mutatingwebhookconfiguration vm-activity-webhook -o yaml | grep inject-cabundle
```

### Timeout errors

The webhook has a 5-second timeout. If you see timeout errors in API server logs, check:
- Processor pod is running and healthy
- Network connectivity between API server and service
- TLS certificate is valid

### Username not appearing

1. Verify webhook is caching users:
   ```bash
   kubectl logs -n vm-activity-plugin deployment/vm-activity-processor -c processor | grep "Cached user"
   ```

2. Check cache timing - events must arrive within 10 minutes of the VM operation

3. Verify the operation type (CREATE/UPDATE) matches webhook rules

## Security Considerations

- The webhook uses `failurePolicy: Ignore`, so VM operations won't be blocked if the webhook is unavailable
- `sideEffects: None` declares the webhook has no side effects
- The webhook only **observes** requests and doesn't modify them
- User information is cached in memory only (not persisted to database)
- Cache entries expire after 10 minutes

## Performance Impact

The admission webhook adds minimal overhead:
- Only VM/VMI resources trigger the webhook (not general cluster traffic)
- Webhook responds in <50ms typically
- No external calls or database writes during admission
- `failurePolicy: Ignore` ensures VM operations aren't blocked if webhook is slow/down
