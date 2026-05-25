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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/andykrohg/openshift-vm-event-plugin/processor/internal/aggregator"
)

// EventWatcher watches Kubernetes Events related to VirtualMachines
type EventWatcher struct {
	clientset     kubernetes.Interface
	processor     *aggregator.EventProcessor
	filterReasons []string
}

// NewEventWatcher creates a new EventWatcher
func NewEventWatcher(clientset kubernetes.Interface, processor *aggregator.EventProcessor, filterReasons []string) *EventWatcher {
	return &EventWatcher{
		clientset:     clientset,
		processor:     processor,
		filterReasons: filterReasons,
	}
}

// Start starts watching events
func (w *EventWatcher) Start(ctx context.Context) error {
	klog.Info("Starting event watcher for VM-related events")

	// Create informer factory for all namespaces
	factory := informers.NewSharedInformerFactory(w.clientset, 10*time.Minute)

	// Get the Event informer
	eventInformer := factory.Core().V1().Events().Informer()

	// Add event handlers
	eventInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			event, ok := obj.(*corev1.Event)
			if !ok {
				return
			}
			w.handleEvent(ctx, event)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			event, ok := newObj.(*corev1.Event)
			if !ok {
				return
			}
			w.handleEvent(ctx, event)
		},
		// Ignore deletions - we keep historical events even if K8s deletes them
	})

	// Start the informer
	factory.Start(ctx.Done())

	// Wait for cache sync
	if !cache.WaitForCacheSync(ctx.Done(), eventInformer.HasSynced) {
		return ctx.Err()
	}

	klog.Info("Event watcher cache synced and ready")

	// Wait for context cancellation
	<-ctx.Done()
	klog.Info("Event watcher shutting down")

	return nil
}

// handleEvent processes an event
func (w *EventWatcher) handleEvent(ctx context.Context, event *corev1.Event) {
	// Only process VM and VMI events
	if event.InvolvedObject.Kind != "VirtualMachine" &&
		event.InvolvedObject.Kind != "VirtualMachineInstance" {
		return
	}

	// Check if event reason should be filtered
	if w.shouldFilter(event.Reason) {
		klog.V(2).Infof("Filtering event: %s/%s (reason: %s)",
			event.Namespace, event.Name, event.Reason)
		return
	}

	klog.V(1).Infof("Processing event: %s/%s for %s/%s (reason: %s, type: %s)",
		event.Namespace, event.Name,
		event.InvolvedObject.Kind, event.InvolvedObject.Name,
		event.Reason, event.Type)

	// Send to processor
	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process event %s/%s: %v", event.Namespace, event.Name, err)
	}
}

// shouldFilter checks if an event reason should be filtered out
func (w *EventWatcher) shouldFilter(reason string) bool {
	for _, filterReason := range w.filterReasons {
		if reason == filterReason {
			return true
		}
	}
	return false
}
