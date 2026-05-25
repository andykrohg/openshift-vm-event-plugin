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

package aggregator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"

	"github.com/andykrohg/openshift-vm-activity-plugin/processor/internal/audit"
	"github.com/andykrohg/openshift-vm-activity-plugin/processor/internal/storage"
)

const (
	// EventChannelCapacity is the size of the event processing queue
	EventChannelCapacity = 10000
	// WorkerCount is the number of goroutines processing events
	WorkerCount = 10
	// BatchSize is the number of events to batch per database transaction
	BatchSize = 100
	// BatchTimeout is how long to wait for a full batch before flushing
	BatchTimeout = 5 * time.Second
)

// ProcessedEvent represents an enriched event ready for storage
type ProcessedEvent struct {
	EventUID         string
	VMName           string
	VMNamespace      string
	EventType        string
	Reason           string
	Message          string
	SourceComponent  string
	FirstTimestamp   time.Time
	LastTimestamp    time.Time
	Count            int32
	Enrichment       map[string]interface{}
	DeduplicationKey string
}

// EventProcessor handles event aggregation, deduplication, and batching
type EventProcessor struct {
	repository        *storage.Repository
	dynamicClient     dynamic.Interface
	userCache         *audit.UserCache
	eventQueue        chan *corev1.Event
	processedEvents   map[string]*ProcessedEvent // keyed by deduplication key
	mu                sync.RWMutex
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	aggregationWindow time.Duration
}

// NewEventProcessor creates a new event processor
func NewEventProcessor(repo *storage.Repository, dynamicClient dynamic.Interface, userCache *audit.UserCache, aggregationWindowMinutes int32) *EventProcessor {
	ctx, cancel := context.WithCancel(context.Background())

	processor := &EventProcessor{
		repository:        repo,
		dynamicClient:     dynamicClient,
		userCache:         userCache,
		eventQueue:        make(chan *corev1.Event, EventChannelCapacity),
		processedEvents:   make(map[string]*ProcessedEvent),
		ctx:               ctx,
		cancel:            cancel,
		aggregationWindow: time.Duration(aggregationWindowMinutes) * time.Minute,
	}

	// Start worker goroutines
	for i := 0; i < WorkerCount; i++ {
		processor.wg.Add(1)
		go processor.worker(i)
	}

	// Start batch flusher
	processor.wg.Add(1)
	go processor.batchFlusher()

	return processor
}

// ProcessEvent queues an event for processing
func (p *EventProcessor) ProcessEvent(ctx context.Context, event *corev1.Event) error {
	select {
	case p.eventQueue <- event:
		klog.V(2).Infof("Queued event: %s (reason: %s, kind: %s)",
			event.Name, event.Reason, event.InvolvedObject.Kind)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return fmt.Errorf("event queue is full, dropping event")
	}
}

// worker processes events from the queue
func (p *EventProcessor) worker(id int) {
	defer p.wg.Done()

	for {
		select {
		case event := <-p.eventQueue:
			if err := p.processEventInternal(event); err != nil {
				klog.Errorf("Worker %d: Failed to process event %s: %v", id, event.Name, err)
			}
		case <-p.ctx.Done():
			klog.Infof("Worker %d shutting down", id)
			return
		}
	}
}

// processEventInternal handles event deduplication and enrichment
func (p *EventProcessor) processEventInternal(event *corev1.Event) error {
	// Generate deduplication key
	dedupKey := p.generateDeduplicationKey(event)

	p.mu.Lock()
	defer p.mu.Unlock()

	existing, exists := p.processedEvents[dedupKey]

	if exists {
		// Update existing event
		existing.Count += event.Count
		if event.LastTimestamp.Time.After(existing.LastTimestamp) {
			existing.LastTimestamp = event.LastTimestamp.Time
		}
		existing.Message = event.Message // Update with latest message
	} else {
		// Create new processed event
		enrichment := p.enrichEvent(event)

		p.processedEvents[dedupKey] = &ProcessedEvent{
			EventUID:         string(event.UID),
			VMName:           event.InvolvedObject.Name,
			VMNamespace:      event.InvolvedObject.Namespace,
			EventType:        event.Type,
			Reason:           event.Reason,
			Message:          event.Message,
			SourceComponent:  fmt.Sprintf("%s/%s", event.Source.Component, event.Source.Host),
			FirstTimestamp:   event.FirstTimestamp.Time,
			LastTimestamp:    event.LastTimestamp.Time,
			Count:            event.Count,
			Enrichment:       enrichment,
			DeduplicationKey: dedupKey,
		}
	}

	return nil
}

// generateDeduplicationKey creates a unique key for event deduplication
func (p *EventProcessor) generateDeduplicationKey(event *corev1.Event) string {
	// Create key based on: namespace, VM name, reason, and time window
	windowStart := event.FirstTimestamp.Time.Truncate(p.aggregationWindow)

	data := fmt.Sprintf("%s/%s/%s/%s/%d",
		event.InvolvedObject.Namespace,
		event.InvolvedObject.Name,
		event.InvolvedObject.Kind,
		event.Reason,
		windowStart.Unix(),
	)

	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash[:16])
}

// enrichEvent extracts additional context from the event
func (p *EventProcessor) enrichEvent(event *corev1.Event) map[string]interface{} {
	enrichment := make(map[string]interface{})

	// Extract patch information from annotations (for VMUpdated events)
	if event.Annotations != nil {
		if patch, ok := event.Annotations["vm-activity.openshift.io/patch"]; ok && patch != "" {
			// Store the raw patch JSON
			enrichment["patch"] = patch
		}
		if snapshotName, ok := event.Annotations["vm-activity.openshift.io/snapshot-name"]; ok && snapshotName != "" {
			enrichment["snapshotName"] = snapshotName
		}
	}

	// Extract annotations if present
	if event.InvolvedObject.UID != "" {
		enrichment["involvedObjectUID"] = string(event.InvolvedObject.UID)
	}

	// Extract node information if available
	if event.Source.Host != "" {
		enrichment["node"] = event.Source.Host
	}

	// Extract field path if present (shows which field changed)
	if event.InvolvedObject.FieldPath != "" {
		enrichment["fieldPath"] = event.InvolvedObject.FieldPath
	}

	// Extract reporting component
	if event.ReportingController != "" {
		enrichment["reportingController"] = event.ReportingController
	}
	if event.ReportingInstance != "" {
		enrichment["reportingInstance"] = event.ReportingInstance
	}

	// Fetch VM metadata for additional context
	if vmInfo := p.fetchVMMetadata(event); len(vmInfo) > 0 {
		for k, v := range vmInfo {
			enrichment[k] = v
		}
	}

	return enrichment
}

// fetchVMMetadata retrieves additional metadata from the VM object
func (p *EventProcessor) fetchVMMetadata(event *corev1.Event) map[string]string {
	result := make(map[string]string)

	// Only fetch for VirtualMachine and VirtualMachineInstance events
	if event.InvolvedObject.Kind != "VirtualMachine" && event.InvolvedObject.Kind != "VirtualMachineInstance" {
		return result
	}

	// First, check the admission webhook cache for user information
	// Cache only contains human users (service accounts are not cached)
	if p.userCache != nil {
		var userInfo *audit.UserInfo

		// For VMI events, try the VM cache first (since VMI name usually matches VM name)
		if event.InvolvedObject.Kind == "VirtualMachineInstance" {
			// Check VM cache for the parent VM's user
			userInfo = p.userCache.Get("VirtualMachine", event.InvolvedObject.Namespace, event.InvolvedObject.Name)
			if userInfo != nil {
				klog.V(2).Infof("Found VM user %s for VMI %s/%s",
					userInfo.Username, event.InvolvedObject.Namespace, event.InvolvedObject.Name)
			}
		}

		// If not found yet, check cache for this specific resource type
		if userInfo == nil {
			userInfo = p.userCache.Get(event.InvolvedObject.Kind, event.InvolvedObject.Namespace, event.InvolvedObject.Name)
			if userInfo != nil {
				klog.V(2).Infof("Found cached user %s for %s %s/%s",
					userInfo.Username, event.InvolvedObject.Kind, event.InvolvedObject.Namespace, event.InvolvedObject.Name)
			}
		}

		// If we found a user, add it to enrichment
		if userInfo != nil {
			result["user"] = userInfo.Username
			if len(userInfo.Groups) > 0 {
				// Store first non-system group as a hint about user type
				for _, group := range userInfo.Groups {
					if group != "system:authenticated" && group != "system:authenticated:oauth" {
						result["userGroup"] = group
						break
					}
				}
			}
		}
	}

	// Define GVR based on resource type
	var gvr schema.GroupVersionResource
	if event.InvolvedObject.Kind == "VirtualMachineInstance" {
		gvr = schema.GroupVersionResource{
			Group:    "kubevirt.io",
			Version:  "v1",
			Resource: "virtualmachineinstances",
		}
	} else {
		gvr = schema.GroupVersionResource{
			Group:    "kubevirt.io",
			Version:  "v1",
			Resource: "virtualmachines",
		}
	}

	// Fetch the resource object
	vm, err := p.dynamicClient.Resource(gvr).
		Namespace(event.InvolvedObject.Namespace).
		Get(context.Background(), event.InvolvedObject.Name, metav1.GetOptions{})
	if err != nil {
		klog.V(2).Infof("Failed to fetch %s %s/%s for metadata enrichment: %v",
			event.InvolvedObject.Kind, event.InvolvedObject.Namespace, event.InvolvedObject.Name, err)
		return result
	}

	// Try to get user from annotations
	annotations := vm.GetAnnotations()
	if annotations != nil {
		// OpenShift annotation for user who created/modified the resource
		if user, ok := annotations["openshift.io/created-by"]; ok {
			result["user"] = user
		} else if user, ok := annotations["kubevirt.io/latest-operated-by-user"]; ok {
			result["user"] = user
		}
	}

	// Get the most recent manager from managedFields (shows if console/CLI/API)
	managedFields := vm.GetManagedFields()
	if len(managedFields) > 0 {
		// Find most recent update
		var latestTime metav1.Time
		var latestManager string
		for _, field := range managedFields {
			if field.Time != nil && field.Time.After(latestTime.Time) {
				latestTime = *field.Time
				latestManager = field.Manager
			}
		}
		if latestManager != "" && latestManager != "kube-controller-manager" && latestManager != "virt-controller" {
			result["manager"] = latestManager
		}
	}

	return result
}

// batchFlusher periodically flushes processed events to the database
func (p *EventProcessor) batchFlusher() {
	defer p.wg.Done()

	ticker := time.NewTicker(BatchTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := p.flushBatch(); err != nil {
				klog.Errorf("Failed to flush batch: %v", err)
			}
		case <-p.ctx.Done():
			// Final flush before shutdown
			if err := p.flushBatch(); err != nil {
				klog.Errorf("Failed to flush final batch: %v", err)
			}
			klog.Info("Batch flusher shutting down")
			return
		}
	}
}

// flushBatch writes accumulated events to the database
func (p *EventProcessor) flushBatch() error {
	p.mu.Lock()
	if len(p.processedEvents) == 0 {
		p.mu.Unlock()
		return nil
	}

	// Copy events to flush
	eventsToFlush := make([]*ProcessedEvent, 0, len(p.processedEvents))
	for _, event := range p.processedEvents {
		eventsToFlush = append(eventsToFlush, event)
	}

	// Clear the map
	p.processedEvents = make(map[string]*ProcessedEvent)
	p.mu.Unlock()

	// Write to database in batches
	for i := 0; i < len(eventsToFlush); i += BatchSize {
		end := i + BatchSize
		if end > len(eventsToFlush) {
			end = len(eventsToFlush)
		}

		batch := eventsToFlush[i:end]
		if err := p.writeBatch(batch); err != nil {
			return fmt.Errorf("failed to write batch: %w", err)
		}
	}

	return nil
}

// writeBatch writes a batch of events to the database
func (p *EventProcessor) writeBatch(events []*ProcessedEvent) error {
	dbEvents := make([]storage.VMActivity, len(events))

	for i, event := range events {
		enrichmentJSON, err := json.Marshal(event.Enrichment)
		if err != nil {
			return fmt.Errorf("failed to marshal enrichment: %w", err)
		}

		dbEvents[i] = storage.VMActivity{
			EventUID:        event.EventUID,
			VMName:          event.VMName,
			VMNamespace:     event.VMNamespace,
			EventType:       event.EventType,
			Reason:          event.Reason,
			Message:         event.Message,
			SourceComponent: event.SourceComponent,
			FirstTimestamp:  event.FirstTimestamp,
			LastTimestamp:   event.LastTimestamp,
			Count:           event.Count,
			Enrichment:      enrichmentJSON,
		}
	}

	return p.repository.InsertEvents(p.ctx, dbEvents)
}

// Shutdown gracefully shuts down the event processor
func (p *EventProcessor) Shutdown() error {
	klog.Info("Shutting down event processor")

	p.cancel()
	p.wg.Wait()

	close(p.eventQueue)
	klog.Info("Event processor shut down complete")

	return nil
}
