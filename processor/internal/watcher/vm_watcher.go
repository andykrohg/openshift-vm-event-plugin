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
	"encoding/json"
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

// VMWatcher watches VirtualMachine resources for create/update/delete
type VMWatcher struct {
	dynamicClient dynamic.Interface
	processor     *aggregator.EventProcessor
	userCache     *audit.UserCache
}

// NewVMWatcher creates a new VMWatcher
func NewVMWatcher(dynamicClient dynamic.Interface, processor *aggregator.EventProcessor, userCache *audit.UserCache) *VMWatcher {
	return &VMWatcher{
		dynamicClient: dynamicClient,
		processor:     processor,
		userCache:     userCache,
	}
}

// Start starts watching VirtualMachine resources
func (w *VMWatcher) Start(ctx context.Context) error {
	klog.Info("Starting VirtualMachine resource watcher")

	vmGVR := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	// Watch all namespaces
	watcher, err := w.dynamicClient.Resource(vmGVR).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to start VM watcher: %w", err)
	}
	defer watcher.Stop()

	klog.Info("VirtualMachine resource watcher started")

	// Track previous state for detecting changes
	previousState := make(map[string]*unstructured.Unstructured)

	for {
		select {
		case <-ctx.Done():
			klog.Info("VirtualMachine watcher shutting down")
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				klog.Warning("VirtualMachine watch channel closed, restarting...")
				// Recreate watcher
				watcher, err = w.dynamicClient.Resource(vmGVR).Watch(ctx, metav1.ListOptions{})
				if err != nil {
					klog.Errorf("Failed to restart VM watcher: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}
				continue
			}

			vm, ok := event.Object.(*unstructured.Unstructured)
			if !ok {
				continue
			}

			namespace := vm.GetNamespace()
			name := vm.GetName()
			key := namespace + "/" + name

			switch event.Type {
			case watch.Added:
				w.handleVMCreated(ctx, vm)
				previousState[key] = vm.DeepCopy()

			case watch.Modified:
				if prev, exists := previousState[key]; exists {
					w.handleVMUpdated(ctx, prev, vm)
				}
				previousState[key] = vm.DeepCopy()

			case watch.Deleted:
				w.handleVMDeleted(ctx, vm)
				delete(previousState, key)
			}
		}
	}
}

// handleVMCreated handles VM creation events
func (w *VMWatcher) handleVMCreated(ctx context.Context, vm *unstructured.Unstructured) {
	namespace := vm.GetNamespace()
	name := vm.GetName()

	// Check if we have user info from admission webhook
	userInfo := w.userCache.Get("VirtualMachine", namespace, name)
	if userInfo == nil {
		klog.V(2).Infof("No user info cached for VM %s/%s creation", namespace, name)
		return
	}

	// Skip service account actions
	if strings.HasPrefix(userInfo.Username, "system:serviceaccount:") {
		klog.V(2).Infof("Skipping VM creation by service account: %s", userInfo.Username)
		return
	}

	klog.Infof("VM %s/%s created by user %s - creating VMCreated event", namespace, name, userInfo.Username)

	// Create synthetic event
	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.vm-created.%d", name, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/user": userInfo.Username,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       name,
			UID:        vm.GetUID(),
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "VMCreated",
		Message: fmt.Sprintf("VirtualMachine %s created", name),
		Type:    "Normal",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "vm-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process VM creation event: %v", err)
	}
}

// handleVMUpdated handles VM update events
func (w *VMWatcher) handleVMUpdated(ctx context.Context, oldVM, newVM *unstructured.Unstructured) {
	namespace := newVM.GetNamespace()
	name := newVM.GetName()

	// Check if we have user info from admission webhook
	userInfo := w.userCache.Get("VirtualMachine", namespace, name)
	if userInfo == nil {
		klog.V(2).Infof("No user info cached for VM %s/%s update", namespace, name)
		return
	}

	// Skip service account actions
	if strings.HasPrefix(userInfo.Username, "system:serviceaccount:") {
		klog.V(2).Infof("Skipping VM update by service account: %s", userInfo.Username)
		return
	}

	// Compare specs to see if there was an actual configuration change
	oldSpec, oldSpecExists, _ := unstructured.NestedMap(oldVM.Object, "spec")
	newSpec, newSpecExists, _ := unstructured.NestedMap(newVM.Object, "spec")

	if !oldSpecExists || !newSpecExists {
		return
	}

	// Calculate diff
	patch := calculateDiff(oldSpec, newSpec)
	if len(patch) == 0 {
		// No meaningful changes
		return
	}

	klog.Infof("VM %s/%s updated by user %s", namespace, name, userInfo.Username)

	// Create patch JSON
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		klog.Errorf("Failed to marshal patch: %v", err)
		patchJSON = []byte("{}")
	}

	message := fmt.Sprintf("VirtualMachine %s configuration updated", name)

	// Create synthetic event with patch in message
	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.vm-updated.%d", name, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/patch": string(patchJSON),
				"vm-activity.openshift.io/user":  userInfo.Username,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       name,
			UID:        newVM.GetUID(),
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "VMUpdated",
		Message: message,
		Type:    "Normal",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "vm-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process VM update event: %v", err)
	}
}

// handleVMDeleted handles VM deletion events
func (w *VMWatcher) handleVMDeleted(ctx context.Context, vm *unstructured.Unstructured) {
	namespace := vm.GetNamespace()
	name := vm.GetName()

	// Check if we have user info from admission webhook
	userInfo := w.userCache.Get("VirtualMachine", namespace, name)
	if userInfo == nil {
		klog.V(2).Infof("No user info cached for VM %s/%s deletion", namespace, name)
		return
	}

	// Skip service account actions
	if strings.HasPrefix(userInfo.Username, "system:serviceaccount:") {
		klog.V(2).Infof("Skipping VM deletion by service account: %s", userInfo.Username)
		return
	}

	klog.Infof("VM %s/%s deleted by user %s", namespace, name, userInfo.Username)

	// Create synthetic event
	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.vm-deleted.%d", name, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/user": userInfo.Username,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       name,
			UID:        vm.GetUID(),
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "VMDeleted",
		Message: fmt.Sprintf("VirtualMachine %s deleted", name),
		Type:    "Normal",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "vm-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process VM deletion event: %v", err)
	}
}

// calculateDiff compares two specs and returns changed fields
func calculateDiff(old, new map[string]interface{}) map[string]interface{} {
	diff := make(map[string]interface{})

	// Check for changed or new fields
	for key, newVal := range new {
		oldVal, exists := old[key]
		if !exists {
			diff[key] = map[string]interface{}{
				"added": newVal,
			}
			continue
		}

		// Deep comparison for nested objects
		oldJSON, _ := json.Marshal(oldVal)
		newJSON, _ := json.Marshal(newVal)
		if string(oldJSON) != string(newJSON) {
			diff[key] = map[string]interface{}{
				"old": oldVal,
				"new": newVal,
			}
		}
	}

	// Check for removed fields
	for key := range old {
		if _, exists := new[key]; !exists {
			diff[key] = map[string]interface{}{
				"removed": old[key],
			}
		}
	}

	return diff
}
