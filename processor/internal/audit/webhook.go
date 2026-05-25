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

package audit

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// AuditEvent represents a Kubernetes audit event
type AuditEvent struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`
	Level      string `json:"level"`
	AuditID    string `json:"auditID"`
	Stage      string `json:"stage"`
	RequestURI string `json:"requestURI"`
	Verb       string `json:"verb"`
	User       struct {
		Username string   `json:"username"`
		UID      string   `json:"uid"`
		Groups   []string `json:"groups"`
	} `json:"user"`
	ObjectRef struct {
		Resource   string `json:"resource"`
		Namespace  string `json:"namespace"`
		Name       string `json:"name"`
		APIGroup   string `json:"apiGroup"`
		APIVersion string `json:"apiVersion"`
		UID        string `json:"uid"`
	} `json:"objectRef"`
	ResponseStatus struct {
		Code int `json:"code"`
	} `json:"responseStatus"`
	RequestReceivedTimestamp time.Time `json:"requestReceivedTimestamp"`
	StageTimestamp           time.Time `json:"stageTimestamp"`
}

// AuditEventList represents a batch of audit events
type AuditEventList struct {
	Kind       string       `json:"kind"`
	APIVersion string       `json:"apiVersion"`
	Items      []AuditEvent `json:"items"`
}

// UserInfo represents cached user information
type UserInfo struct {
	Username  string
	UID       string
	Groups    []string
	Timestamp time.Time
}

// UserCache caches user information from audit events
type UserCache struct {
	mu    sync.RWMutex
	cache map[string]*UserInfo // key: namespace/name
}

// NewUserCache creates a new user cache
func NewUserCache() *UserCache {
	cache := &UserCache{
		cache: make(map[string]*UserInfo),
	}

	// Start cleanup goroutine
	go cache.cleanup()

	return cache
}

// Set stores user information for a VM
func (c *UserCache) Set(namespace, name, username, uid string, groups []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := namespace + "/" + name
	c.cache[key] = &UserInfo{
		Username:  username,
		UID:       uid,
		Groups:    groups,
		Timestamp: time.Now(),
	}
}

// Get retrieves user information for a VM
func (c *UserCache) Get(namespace, name string) *UserInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := namespace + "/" + name
	return c.cache[key]
}

// cleanup removes stale entries (older than 10 minutes)
func (c *UserCache) cleanup() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for key, info := range c.cache {
			if now.Sub(info.Timestamp) > 10*time.Minute {
				delete(c.cache, key)
			}
		}
		c.mu.Unlock()
	}
}

// WebhookHandler handles audit webhook requests
type WebhookHandler struct {
	userCache *UserCache
}

// NewWebhookHandler creates a new audit webhook handler
func NewWebhookHandler(userCache *UserCache) *WebhookHandler {
	return &WebhookHandler{
		userCache: userCache,
	}
}

// ServeHTTP handles incoming audit events
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		klog.Errorf("Failed to read audit webhook request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Try to parse as EventList first (batch of events)
	var eventList AuditEventList
	if err := json.Unmarshal(body, &eventList); err == nil && eventList.Kind == "EventList" {
		for _, event := range eventList.Items {
			h.processAuditEvent(&event)
		}
	} else {
		// Single event
		var event AuditEvent
		if err := json.Unmarshal(body, &event); err != nil {
			klog.Errorf("Failed to unmarshal audit event: %v", err)
			http.Error(w, "Failed to parse audit event", http.StatusBadRequest)
			return
		}
		h.processAuditEvent(&event)
	}

	// Respond with 200 OK
	w.WriteHeader(http.StatusOK)
}

// processAuditEvent processes a single audit event
func (h *WebhookHandler) processAuditEvent(event *AuditEvent) {
	// Only process VirtualMachine and VirtualMachineInstance resources
	if event.ObjectRef.Resource != "virtualmachines" &&
		event.ObjectRef.Resource != "virtualmachineinstances" {
		return
	}

	// Only process relevant verbs (create, update, patch, delete)
	if event.Verb != "create" && event.Verb != "update" &&
		event.Verb != "patch" && event.Verb != "delete" {
		return
	}

	// Only process successful requests (2xx status codes)
	if event.ResponseStatus.Code < 200 || event.ResponseStatus.Code >= 300 {
		return
	}

	// Cache the user information
	h.userCache.Set(
		event.ObjectRef.Namespace,
		event.ObjectRef.Name,
		event.User.Username,
		event.User.UID,
		event.User.Groups,
	)

	klog.Infof("Cached user %s for %s/%s/%s (verb: %s)",
		event.User.Username,
		event.ObjectRef.Resource,
		event.ObjectRef.Namespace,
		event.ObjectRef.Name,
		event.Verb)
}
