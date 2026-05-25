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

// MigrationWatcher watches VirtualMachineInstanceMigration resources
type MigrationWatcher struct {
	dynamicClient dynamic.Interface
	processor     *aggregator.EventProcessor
	userCache     *audit.UserCache
}

// NewMigrationWatcher creates a new MigrationWatcher
func NewMigrationWatcher(dynamicClient dynamic.Interface, processor *aggregator.EventProcessor, userCache *audit.UserCache) *MigrationWatcher {
	return &MigrationWatcher{
		dynamicClient: dynamicClient,
		processor:     processor,
		userCache:     userCache,
	}
}

// Start starts watching VirtualMachineInstanceMigration resources
func (w *MigrationWatcher) Start(ctx context.Context) error {
	klog.Info("Starting VirtualMachineInstanceMigration resource watcher")

	migrationGVR := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachineinstancemigrations",
	}

	// Watch all namespaces
	watcher, err := w.dynamicClient.Resource(migrationGVR).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to start migration watcher: %w", err)
	}
	defer watcher.Stop()

	klog.Info("VirtualMachineInstanceMigration resource watcher started")

	// Track migration status to detect completion/failure
	migrationStatus := make(map[string]string)

	for {
		select {
		case <-ctx.Done():
			klog.Info("Migration watcher shutting down")
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				klog.Warning("Migration watch channel closed, restarting...")
				// Recreate watcher
				watcher, err = w.dynamicClient.Resource(migrationGVR).Watch(ctx, metav1.ListOptions{})
				if err != nil {
					klog.Errorf("Failed to restart migration watcher: %v", err)
					time.Sleep(5 * time.Second)
					continue
				}
				continue
			}

			migration, ok := event.Object.(*unstructured.Unstructured)
			if !ok {
				continue
			}

			namespace := migration.GetNamespace()
			name := migration.GetName()
			key := namespace + "/" + name

			switch event.Type {
			case watch.Added:
				w.handleMigrationStarted(ctx, migration)
				migrationStatus[key] = "running"

			case watch.Modified:
				oldStatus := migrationStatus[key]
				newStatus := w.getMigrationStatus(migration)

				// Detect status transitions
				if oldStatus != newStatus && newStatus != "" {
					if newStatus == "succeeded" {
						w.handleMigrationSucceeded(ctx, migration)
					} else if newStatus == "failed" {
						w.handleMigrationFailed(ctx, migration)
					}
				}
				migrationStatus[key] = newStatus

			case watch.Deleted:
				delete(migrationStatus, key)
			}
		}
	}
}

// getMigrationStatus extracts the migration status
func (w *MigrationWatcher) getMigrationStatus(migration *unstructured.Unstructured) string {
	phase, found, _ := unstructured.NestedString(migration.Object, "status", "phase")
	if !found {
		return "unknown"
	}

	switch phase {
	case "Succeeded":
		return "succeeded"
	case "Failed":
		return "failed"
	case "Running", "Scheduling", "Scheduled", "PreparingTarget", "TargetReady":
		return "running"
	default:
		return "unknown"
	}
}

// handleMigrationStarted handles migration initiation
func (w *MigrationWatcher) handleMigrationStarted(ctx context.Context, migration *unstructured.Unstructured) {
	namespace := migration.GetNamespace()
	name := migration.GetName()

	// Get the VMI name
	vmiName, found, _ := unstructured.NestedString(migration.Object, "spec", "vmiName")
	if !found {
		klog.V(2).Infof("Could not determine VMI name for migration %s/%s", namespace, name)
		return
	}

	// Check if we have user info from admission webhook
	userInfo := w.userCache.Get("VirtualMachineInstanceMigration", namespace, name)
	if userInfo == nil {
		klog.V(2).Infof("No user info cached for migration %s/%s", namespace, name)
		return
	}

	// Skip service account actions
	if strings.HasPrefix(userInfo.Username, "system:serviceaccount:") {
		klog.V(2).Infof("Skipping migration by service account: %s", userInfo.Username)
		return
	}

	klog.Infof("Migration %s/%s started for VMI %s by user %s", namespace, name, vmiName, userInfo.Username)

	// Create synthetic event for the VM (use VMI name as VM name typically matches)
	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.migration-started.%d", vmiName, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/migration-name": name,
				"vm-activity.openshift.io/user":           userInfo.Username,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       vmiName,
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "MigrationStarted",
		Message: "Live migration started",
		Type:    "Normal",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "migration-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process migration started event: %v", err)
	}
}

// handleMigrationSucceeded handles successful migration completion
func (w *MigrationWatcher) handleMigrationSucceeded(ctx context.Context, migration *unstructured.Unstructured) {
	namespace := migration.GetNamespace()
	name := migration.GetName()

	// Get the VMI name
	vmiName, found, _ := unstructured.NestedString(migration.Object, "spec", "vmiName")
	if !found {
		return
	}

	// Get source and target nodes
	sourceNode, _, _ := unstructured.NestedString(migration.Object, "status", "migrationState", "sourceNode")
	targetNode, _, _ := unstructured.NestedString(migration.Object, "status", "migrationState", "targetNode")

	klog.Infof("Migration %s/%s for VMI %s succeeded (from %s to %s)", namespace, name, vmiName, sourceNode, targetNode)

	message := "Live migration completed successfully"
	if sourceNode != "" && targetNode != "" {
		message = fmt.Sprintf("Live migration completed from %s to %s", sourceNode, targetNode)
	}

	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.migration-succeeded.%d", vmiName, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/migration-name": name,
				"vm-activity.openshift.io/source-node":    sourceNode,
				"vm-activity.openshift.io/target-node":    targetNode,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       vmiName,
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "MigrationSucceeded",
		Message: message,
		Type:    "Normal",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "migration-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process migration succeeded event: %v", err)
	}
}

// handleMigrationFailed handles failed migrations
func (w *MigrationWatcher) handleMigrationFailed(ctx context.Context, migration *unstructured.Unstructured) {
	namespace := migration.GetNamespace()
	name := migration.GetName()

	// Get the VMI name
	vmiName, found, _ := unstructured.NestedString(migration.Object, "spec", "vmiName")
	if !found {
		return
	}

	// Try to get error message
	errorMsg := "Migration failed"
	conditions, found, _ := unstructured.NestedSlice(migration.Object, "status", "conditions")
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

	klog.Warningf("Migration %s/%s for VMI %s failed: %s", namespace, name, vmiName, errorMsg)

	eventTime := time.Now()
	eventName := fmt.Sprintf("%s.migration-failed.%d", vmiName, eventTime.Unix())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      eventName,
			Namespace: namespace,
			UID:       generateEventUID(eventName),
			Annotations: map[string]string{
				"vm-activity.openshift.io/migration-name": name,
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "VirtualMachine",
			Namespace:  namespace,
			Name:       vmiName,
			APIVersion: "kubevirt.io/v1",
		},
		Reason:  "MigrationFailed",
		Message: fmt.Sprintf("Live migration failed: %s", errorMsg),
		Type:    "Warning",
		Source: corev1.EventSource{
			Component: "vm-activity-operator",
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		Count:               1,
		ReportingController: "vm-activity-operator",
		ReportingInstance:   "migration-watcher",
	}

	if err := w.processor.ProcessEvent(ctx, event); err != nil {
		klog.Errorf("Failed to process migration failed event: %v", err)
	}
}
