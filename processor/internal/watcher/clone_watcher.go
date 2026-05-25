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

// CloneWatcher watches VirtualMachineClone resources
type CloneWatcher struct {
	dynamicClient dynamic.Interface
	processor     *aggregator.EventProcessor
	userCache     *audit.UserCache
}

// NewCloneWatcher creates a new CloneWatcher
func NewCloneWatcher(dynamicClient dynamic.Interface, processor *aggregator.EventProcessor, userCache *audit.UserCache) *CloneWatcher {
	return &CloneWatcher{
		dynamicClient: dynamicClient,
		processor:     processor,
		userCache:     userCache,
	}
}

// Start starts watching VirtualMachineClone resources
func (w *CloneWatcher) Start(ctx context.Context) error {
	klog.Info("Starting VirtualMachineClone resource watcher")

	cloneGVR := schema.GroupVersionResource{
		Group:    "clone.kubevirt.io",
		Version:  "v1alpha1",
		Resource: "virtualmachineclones",
	}

	// Watch all namespaces
	watcher, err := w.dynamicClient.Resource(cloneGVR).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to start clone watcher: %w", err)
	}
	defer watcher.Stop()

	klog.Info("VirtualMachineClone resource watcher started")

	// Track clone status to detect completion/failure
	cloneStatus := make(map[string]string)

	for {
		select {
		case <-ctx.Done():
			klog.Info("Clone watcher shutting down")
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				klog.Warning("Clone watch channel closed, restarting...")
				// Recreate watcher
				watcher, err = w.dynamicClient.Resource(cloneGVR).Watch(ctx, metav1.ListOptions{})
				if err != nil {
					klog.Errorf("Failed to restart clone watcher: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}
				continue
			}

			clone, ok := event.Object.(*unstructured.Unstructured)
			if !ok {
				continue
			}

			namespace := clone.GetNamespace()
			name := clone.GetName()
			key := namespace + "/" + name

			switch event.Type {
			case watch.Added:
				w.handleCloneStarted(ctx, clone)
				cloneStatus[key] = "running"

			case watch.Modified:
				oldStatus := cloneStatus[key]
				newStatus := w.getCloneStatus(clone)

				// Detect status transitions
				if oldStatus != newStatus && newStatus != "" {
					if newStatus == "succeeded" {
						w.handleCloneSucceeded(ctx, clone)
					} else if newStatus == "failed" {
						w.handleCloneFailed(ctx, clone)
					}
				}
				cloneStatus[key] = newStatus

			case watch.Deleted:
				delete(cloneStatus, key)
			}
		}
	}
}

// getCloneStatus extracts the clone status
func (w *CloneWatcher) getCloneStatus(clone *unstructured.Unstructured) string {
	phase, found, _ := unstructured.NestedString(clone.Object, "status", "phase")
	if !found {
		return "unknown"
	}

	switch phase {
	case "Succeeded":
		return "succeeded"
	case "Failed":
		return "failed"
	case "Creating", "Cloning", "SnapshotInProgress":
		return "running"
	default:
		return "unknown"
	}
}

// handleCloneStarted handles clone initiation
func (w *CloneWatcher) handleCloneStarted(ctx context.Context, clone *unstructured.Unstructured) {
	namespace := clone.GetNamespace()
	name := clone.GetName()

	// Get the source and target VM names
	sourceVMName, found, _ := unstructured.NestedString(clone.Object, "spec", "source", "name")
	if !found {
		klog.V(2).Infof("Could not determine source VM name for clone %s/%s", namespace, name)
		return
	}

	targetVMName, found, _ := unstructured.NestedString(clone.Object, "spec", "target", "name")
	if !found {
		klog.V(2).Infof("Could not determine target VM name for clone %s/%s", namespace, name)
		return
	}

	// Check if we have user info from admission webhook
	userInfo := w.userCache.Get("VirtualMachineClone", namespace, name)
	if userInfo == nil {
		klog.V(2).Infof("No user info cached for clone %s/%s", namespace, name)
		return
	}

	// Skip service account actions
	if strings.HasPrefix(userInfo.Username, "system:serviceaccount:") {
		klog.V(2).Infof("Skipping clone by service account: %s", userInfo.Username)
		return
	}

	klog.Infof("Clone %s/%s started from %s to %s by user %s", namespace, name, sourceVMName, targetVMName, userInfo.Username)

	// Create synthetic event for the target VM
	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.clone-started.%d", targetVMName, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/clone-name": name,
				"vm-activity.openshift.io/source-vm":  sourceVMName,
				"vm-activity.openshift.io/user":       userInfo.Username,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       targetVMName,
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "CloneStarted",
		Message: fmt.Sprintf("Clone from %s started", sourceVMName),
		Type:    "Normal",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "clone-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process clone started event: %v", err)
	}
}

// handleCloneSucceeded handles successful clone completion
func (w *CloneWatcher) handleCloneSucceeded(ctx context.Context, clone *unstructured.Unstructured) {
	namespace := clone.GetNamespace()
	name := clone.GetName()

	// Get the source and target VM names
	sourceVMName, found, _ := unstructured.NestedString(clone.Object, "spec", "source", "name")
	if !found {
		return
	}

	targetVMName, found, _ := unstructured.NestedString(clone.Object, "spec", "target", "name")
	if !found {
		return
	}

	klog.Infof("Clone %s/%s from %s to %s succeeded", namespace, name, sourceVMName, targetVMName)

	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.clone-succeeded.%d", targetVMName, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/clone-name": name,
				"vm-activity.openshift.io/source-vm":  sourceVMName,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       targetVMName,
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "CloneSucceeded",
		Message: fmt.Sprintf("Clone from %s completed successfully", sourceVMName),
		Type:    "Normal",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "clone-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process clone succeeded event: %v", err)
	}
}

// handleCloneFailed handles failed clones
func (w *CloneWatcher) handleCloneFailed(ctx context.Context, clone *unstructured.Unstructured) {
	namespace := clone.GetNamespace()
	name := clone.GetName()

	// Get the source and target VM names
	sourceVMName, found, _ := unstructured.NestedString(clone.Object, "spec", "source", "name")
	if !found {
		return
	}

	targetVMName, found, _ := unstructured.NestedString(clone.Object, "spec", "target", "name")
	if !found {
		return
	}

	// Try to get error message
	errorMsg := "Clone failed"
	conditions, found, _ := unstructured.NestedSlice(clone.Object, "status", "conditions")
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

	klog.Warningf("Clone %s/%s from %s to %s failed: %s", namespace, name, sourceVMName, targetVMName, errorMsg)

	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.clone-failed.%d", targetVMName, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/clone-name": name,
				"vm-activity.openshift.io/source-vm":  sourceVMName,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       targetVMName,
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "CloneFailed",
		Message: fmt.Sprintf("Clone from %s failed: %s", sourceVMName, errorMsg),
		Type:    "Warning",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "clone-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process clone failed event: %v", err)
	}
}
