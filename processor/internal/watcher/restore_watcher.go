/*
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
*/

package watcher

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"

	"github.com/andykrohg/openshift-vm-activity-plugin/processor/internal/aggregator"
	"github.com/andykrohg/openshift-vm-activity-plugin/processor/internal/audit"
)

// RestoreWatcher watches VirtualMachineRestore resources
type RestoreWatcher struct {
	dynamicClient dynamic.Interface
	processor     *aggregator.EventProcessor
	userCache     *audit.UserCache
}

// NewRestoreWatcher creates a new RestoreWatcher
func NewRestoreWatcher(dynamicClient dynamic.Interface, processor *aggregator.EventProcessor, userCache *audit.UserCache) *RestoreWatcher {
	return &RestoreWatcher{
		dynamicClient: dynamicClient,
		processor:     processor,
		userCache:     userCache,
	}
}

// Start starts watching VirtualMachineRestore resources
func (w *RestoreWatcher) Start(ctx context.Context) error {
	klog.Info("Starting VirtualMachineRestore resource watcher")

	restoreGVR := schema.GroupVersionResource{
		Group:    "snapshot.kubevirt.io",
		Version:  "v1beta1",
		Resource: "virtualmachinerestores",
	}

	// Watch all namespaces
	watcher, err := w.dynamicClient.Resource(restoreGVR).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to start restore watcher: %w", err)
	}
	defer watcher.Stop()

	klog.Info("VirtualMachineRestore resource watcher started")

	// Track restore status to detect completion
	restoreStatus := make(map[string]string)

	for {
		select {
		case <-ctx.Done():
			klog.Info("Restore watcher shutting down")
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				klog.Warning("Restore watch channel closed, restarting...")
				// Recreate watcher
				watcher, err = w.dynamicClient.Resource(restoreGVR).Watch(ctx, metav1.ListOptions{})
				if err != nil {
					klog.Errorf("Failed to restart restore watcher: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}
				continue
			}

			restore, ok := event.Object.(*unstructured.Unstructured)
			if !ok {
				continue
			}

			namespace := restore.GetNamespace()
			name := restore.GetName()
			key := namespace + "/" + name

			switch event.Type {
			case watch.Added:
				w.handleRestoreStarted(ctx, restore)
				restoreStatus[key] = "inprogress"

			case watch.Modified:
				oldStatus := restoreStatus[key]
				newStatus := w.getRestoreStatus(restore)

				// Detect status transitions
				if oldStatus != newStatus && newStatus != "" {
					if newStatus == "complete" {
						w.handleRestoreCompleted(ctx, restore)
					} else if newStatus == "failed" {
						w.handleRestoreFailed(ctx, restore)
					}
				}
				restoreStatus[key] = newStatus

			case watch.Deleted:
				delete(restoreStatus, key)
			}
		}
	}
}

// getRestoreStatus extracts the restore status
func (w *RestoreWatcher) getRestoreStatus(restore *unstructured.Unstructured) string {
	complete, found, _ := unstructured.NestedBool(restore.Object, "status", "complete")
	if found && complete {
		return "complete"
	}

	conditions, found, _ := unstructured.NestedSlice(restore.Object, "status", "conditions")
	if found {
		for _, cond := range conditions {
			condMap, ok := cond.(map[string]interface{})
			if !ok {
				continue
			}
			condType, _ := condMap["type"].(string)
			status, _ := condMap["status"].(string)

			// Look for Failed condition
			if condType == "Ready" && status == "False" {
				reason, _ := condMap["reason"].(string)
				if reason == "Error" {
					return "failed"
				}
			}
		}
	}

	return "inprogress"
}

// handleRestoreStarted handles restore initiation
func (w *RestoreWatcher) handleRestoreStarted(ctx context.Context, restore *unstructured.Unstructured) {
	namespace := restore.GetNamespace()
	name := restore.GetName()

	// Get the target VM name
	vmName, found, _ := unstructured.NestedString(restore.Object, "spec", "target", "name")
	if !found {
		// Try virtualMachineName for older API versions
		vmName, found, _ = unstructured.NestedString(restore.Object, "spec", "virtualMachineName")
		if !found {
			klog.V(2).Infof("Could not determine VM name for restore %s/%s", namespace, name)
			return
		}
	}

	// Get snapshot name
	snapshotName, _, _ := unstructured.NestedString(restore.Object, "spec", "virtualMachineSnapshotName")

	// Check if we have user info from admission webhook
	userInfo := w.userCache.Get("VirtualMachineRestore", namespace, name)
	if userInfo == nil {
		klog.V(2).Infof("No user info cached for restore %s/%s", namespace, name)
		return
	}

	// Skip service account actions
	if strings.HasPrefix(userInfo.Username, "system:serviceaccount:") {
		klog.V(2).Infof("Skipping restore by service account: %s", userInfo.Username)
		return
	}

	klog.Infof("Restore %s/%s started for VM %s by user %s", namespace, name, vmName, userInfo.Username)

	// Create synthetic event for the VM
	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.restore-started.%d", vmName, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/snapshot-name": snapshotName,
				"vm-activity.openshift.io/restore-name":  name,
				"vm-activity.openshift.io/user":          userInfo.Username,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       vmName,
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "RestoreStarted",
		Message: fmt.Sprintf("Restoring from snapshot %s", snapshotName),
		Type:    "Normal",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "restore-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process restore started event: %v", err)
	}
}

// handleRestoreCompleted handles successful restore completion
func (w *RestoreWatcher) handleRestoreCompleted(ctx context.Context, restore *unstructured.Unstructured) {
	namespace := restore.GetNamespace()
	name := restore.GetName()

	// Get the target VM name
	vmName, found, _ := unstructured.NestedString(restore.Object, "spec", "target", "name")
	if !found {
		vmName, found, _ = unstructured.NestedString(restore.Object, "spec", "virtualMachineName")
		if !found {
			return
		}
	}

	// Get snapshot name
	snapshotName, _, _ := unstructured.NestedString(restore.Object, "spec", "virtualMachineSnapshotName")

	klog.Infof("Restore %s/%s for VM %s completed successfully", namespace, name, vmName)

	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.restore-complete.%d", vmName, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/snapshot-name": snapshotName,
				"vm-activity.openshift.io/restore-name":  name,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       vmName,
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "RestoreComplete",
		Message: fmt.Sprintf("Successfully restored from snapshot %s", snapshotName),
		Type:    "Normal",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "restore-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process restore complete event: %v", err)
	}
}

// handleRestoreFailed handles failed restores
func (w *RestoreWatcher) handleRestoreFailed(ctx context.Context, restore *unstructured.Unstructured) {
	namespace := restore.GetNamespace()
	name := restore.GetName()

	// Get the target VM name
	vmName, found, _ := unstructured.NestedString(restore.Object, "spec", "target", "name")
	if !found {
		vmName, found, _ = unstructured.NestedString(restore.Object, "spec", "virtualMachineName")
		if !found {
			return
		}
	}

	// Get snapshot name
	snapshotName, _, _ := unstructured.NestedString(restore.Object, "spec", "virtualMachineSnapshotName")

	// Try to get error message
	errorMsg := "Restore failed"
	conditions, found, _ := unstructured.NestedSlice(restore.Object, "status", "conditions")
	if found {
		for _, cond := range conditions {
			condMap, ok := cond.(map[string]interface{})
			if !ok {
				continue
			}
			message, _ := condMap["message"].(string)
			if message != "" {
				errorMsg = message
				break
			}
		}
	}

	klog.Warningf("Restore %s/%s for VM %s failed: %s", namespace, name, vmName, errorMsg)

	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.restore-failed.%d", vmName, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/snapshot-name": snapshotName,
				"vm-activity.openshift.io/restore-name":  name,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       vmName,
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "RestoreFailed",
		Message: fmt.Sprintf("Restore from snapshot %s failed: %s", snapshotName, errorMsg),
		Type:    "Warning",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "restore-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process restore failed event: %v", err)
	}
}
