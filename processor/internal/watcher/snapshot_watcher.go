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

// SnapshotWatcher watches VirtualMachineSnapshot resources
type SnapshotWatcher struct {
	dynamicClient dynamic.Interface
	processor     *aggregator.EventProcessor
	userCache     *audit.UserCache
}

// NewSnapshotWatcher creates a new SnapshotWatcher
func NewSnapshotWatcher(dynamicClient dynamic.Interface, processor *aggregator.EventProcessor, userCache *audit.UserCache) *SnapshotWatcher {
	return &SnapshotWatcher{
		dynamicClient: dynamicClient,
		processor:     processor,
		userCache:     userCache,
	}
}

// Start starts watching VirtualMachineSnapshot resources
func (w *SnapshotWatcher) Start(ctx context.Context) error {
	klog.Info("Starting VirtualMachineSnapshot resource watcher")

	snapshotGVR := schema.GroupVersionResource{
		Group:    "snapshot.kubevirt.io",
		Version:  "v1beta1",
		Resource: "virtualmachinesnapshots",
	}

	// Watch all namespaces
	watcher, err := w.dynamicClient.Resource(snapshotGVR).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to start snapshot watcher: %w", err)
	}
	defer watcher.Stop()

	klog.Info("VirtualMachineSnapshot resource watcher started")

	// Track snapshot status to detect completion/failure
	snapshotStatus := make(map[string]string)

	for {
		select {
		case <-ctx.Done():
			klog.Info("Snapshot watcher shutting down")
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				klog.Warning("Snapshot watch channel closed, restarting...")
				// Recreate watcher
				watcher, err = w.dynamicClient.Resource(snapshotGVR).Watch(ctx, metav1.ListOptions{})
				if err != nil {
					klog.Errorf("Failed to restart snapshot watcher: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}
				continue
			}

			snapshot, ok := event.Object.(*unstructured.Unstructured)
			if !ok {
				continue
			}

			namespace := snapshot.GetNamespace()
			name := snapshot.GetName()
			key := namespace + "/" + name

			switch event.Type {
			case watch.Added:
				w.handleSnapshotCreated(ctx, snapshot)
				snapshotStatus[key] = "creating"

			case watch.Modified:
				oldStatus := snapshotStatus[key]
				newStatus := w.getSnapshotStatus(snapshot)

				// Detect status transitions
				if oldStatus != newStatus && newStatus != "" {
					if newStatus == "ready" {
						w.handleSnapshotSucceeded(ctx, snapshot)
					} else if newStatus == "failed" {
						w.handleSnapshotFailed(ctx, snapshot)
					}
				}
				snapshotStatus[key] = newStatus

			case watch.Deleted:
				w.handleSnapshotDeleted(ctx, snapshot)
				delete(snapshotStatus, key)
			}
		}
	}
}

// getSnapshotStatus extracts the snapshot status
func (w *SnapshotWatcher) getSnapshotStatus(snapshot *unstructured.Unstructured) string {
	readyToUse, found, _ := unstructured.NestedBool(snapshot.Object, "status", "readyToUse")
	if found && readyToUse {
		return "ready"
	}

	conditions, found, _ := unstructured.NestedSlice(snapshot.Object, "status", "conditions")
	if found {
		for _, cond := range conditions {
			condMap, ok := cond.(map[string]interface{})
			if !ok {
				continue
			}
			condType, _ := condMap["type"].(string)
			status, _ := condMap["status"].(string)

			if condType == "Ready" && status == "False" {
				return "failed"
			}
		}
	}

	return "creating"
}

// handleSnapshotCreated handles snapshot creation
func (w *SnapshotWatcher) handleSnapshotCreated(ctx context.Context, snapshot *unstructured.Unstructured) {
	namespace := snapshot.GetNamespace()
	name := snapshot.GetName()

	// Get the source VM name
	vmName, found, _ := unstructured.NestedString(snapshot.Object, "spec", "source", "name")
	if !found {
		klog.V(2).Infof("Could not determine VM name for snapshot %s/%s", namespace, name)
		return
	}

	// Check if we have user info from admission webhook for the snapshot itself
	userInfo := w.userCache.Get("VirtualMachineSnapshot", namespace, name)
	if userInfo == nil {
		klog.V(2).Infof("No user info cached for snapshot %s/%s creation", namespace, name)
		return
	}

	// Skip service account actions
	if strings.HasPrefix(userInfo.Username, "system:serviceaccount:") {
		klog.V(2).Infof("Skipping snapshot creation by service account: %s", userInfo.Username)
		return
	}

	klog.Infof("Snapshot %s/%s created for VM %s by user %s", namespace, name, vmName, userInfo.Username)

	// Create synthetic event for the VM
	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.snapshot-created.%d", vmName, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/snapshot-name": name,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       vmName,
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "SnapshotCreated",
		Message: fmt.Sprintf("Snapshot %s created by %s", name, userInfo.Username),
		Type:    "Normal",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "snapshot-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process snapshot creation event: %v", err)
	}
}

// handleSnapshotSucceeded handles successful snapshot completion
func (w *SnapshotWatcher) handleSnapshotSucceeded(ctx context.Context, snapshot *unstructured.Unstructured) {
	namespace := snapshot.GetNamespace()
	name := snapshot.GetName()

	// Get the source VM name
	vmName, found, _ := unstructured.NestedString(snapshot.Object, "spec", "source", "name")
	if !found {
		return
	}

	klog.Infof("Snapshot %s/%s for VM %s succeeded", namespace, name, vmName)

	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.snapshot-ready.%d", vmName, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/snapshot-name": name,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       vmName,
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "SnapshotReady",
		Message: fmt.Sprintf("Snapshot %s is ready to use", name),
		Type:    "Normal",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "snapshot-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process snapshot ready event: %v", err)
	}
}

// handleSnapshotFailed handles failed snapshots
func (w *SnapshotWatcher) handleSnapshotFailed(ctx context.Context, snapshot *unstructured.Unstructured) {
	namespace := snapshot.GetNamespace()
	name := snapshot.GetName()

	// Get the source VM name
	vmName, found, _ := unstructured.NestedString(snapshot.Object, "spec", "source", "name")
	if !found {
		return
	}

	// Try to get error message
	errorMsg := "Snapshot failed"
	conditions, found, _ := unstructured.NestedSlice(snapshot.Object, "status", "conditions")
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

	klog.Warningf("Snapshot %s/%s for VM %s failed: %s", namespace, name, vmName, errorMsg)

	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.snapshot-failed.%d", vmName, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/snapshot-name": name,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       vmName,
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "SnapshotFailed",
		Message: fmt.Sprintf("Snapshot %s failed: %s", name, errorMsg),
		Type:    "Warning",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "snapshot-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process snapshot failed event: %v", err)
	}
}

// handleSnapshotDeleted handles snapshot deletion
func (w *SnapshotWatcher) handleSnapshotDeleted(ctx context.Context, snapshot *unstructured.Unstructured) {
	namespace := snapshot.GetNamespace()
	name := snapshot.GetName()

	// Get the source VM name
	vmName, found, _ := unstructured.NestedString(snapshot.Object, "spec", "source", "name")
	if !found {
		return
	}

	// Check if we have user info
	userInfo := w.userCache.Get("VirtualMachineSnapshot", namespace, name)
	if userInfo == nil {
		klog.V(2).Infof("No user info cached for snapshot %s/%s deletion", namespace, name)
		return
	}

	// Skip service account actions
	if strings.HasPrefix(userInfo.Username, "system:serviceaccount:") {
		klog.V(2).Infof("Skipping snapshot deletion by service account: %s", userInfo.Username)
		return
	}

	klog.Infof("Snapshot %s/%s for VM %s deleted by user %s", namespace, name, vmName, userInfo.Username)

	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.snapshot-deleted.%d", vmName, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/snapshot-name": name,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       vmName,
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "SnapshotDeleted",
		Message: fmt.Sprintf("Snapshot %s deleted by %s", name, userInfo.Username),
		Type:    "Normal",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "snapshot-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process snapshot deletion event: %v", err)
	}
}
