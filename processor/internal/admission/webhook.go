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

package admission

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/andykrohg/openshift-vm-event-plugin/processor/internal/audit"
)

// WebhookHandler handles admission webhook requests for VirtualMachines
type WebhookHandler struct {
	userCache *audit.UserCache
}

// NewWebhookHandler creates a new admission webhook handler
func NewWebhookHandler(userCache *audit.UserCache) *WebhookHandler {
	return &WebhookHandler{
		userCache: userCache,
	}
}

// ServeHTTP handles incoming admission review requests
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		klog.Errorf("Failed to read admission webhook request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse AdmissionReview request
	admissionReview := admissionv1.AdmissionReview{}
	if err := json.Unmarshal(body, &admissionReview); err != nil {
		klog.Errorf("Failed to unmarshal admission review: %v", err)
		http.Error(w, "Failed to parse admission review", http.StatusBadRequest)
		return
	}

	// Process the admission request
	admissionResponse := h.processAdmissionRequest(admissionReview.Request)

	// Build response
	responseReview := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: admissionResponse,
	}

	// Set response UID to match request
	if admissionReview.Request != nil {
		responseReview.Response.UID = admissionReview.Request.UID
	}

	// Marshal and send response
	respBytes, err := json.Marshal(responseReview)
	if err != nil {
		klog.Errorf("Failed to marshal admission response: %v", err)
		http.Error(w, "Failed to create response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respBytes)
}

// processAdmissionRequest processes an admission request and caches user info
func (h *WebhookHandler) processAdmissionRequest(req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	if req == nil {
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: "Invalid admission request",
			},
		}
	}

	// Only process VirtualMachine, VirtualMachineInstance, and VirtualMachineSnapshot resources
	if req.Kind.Kind != "VirtualMachine" &&
	   req.Kind.Kind != "VirtualMachineInstance" &&
	   req.Kind.Kind != "VirtualMachineSnapshot" {
		// Allow but don't process
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	// Only process create, update, delete, and patch operations
	if req.Operation != admissionv1.Create &&
		req.Operation != admissionv1.Update &&
		req.Operation != admissionv1.Delete &&
		req.Operation != admissionv1.Connect {
		// Allow but don't process
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	// Only cache human users - skip service accounts
	// This ensures the cache always has the last *human* who touched the resource
	if !strings.HasPrefix(req.UserInfo.Username, "system:serviceaccount:") {
		h.userCache.Set(
			req.Kind.Kind,
			req.Namespace,
			req.Name,
			req.UserInfo.Username,
			req.UserInfo.UID,
			req.UserInfo.Groups,
		)

		klog.Infof("Cached user %s for %s/%s/%s (operation: %s)",
			req.UserInfo.Username,
			req.Kind.Kind,
			req.Namespace,
			req.Name,
			req.Operation)
	} else {
		klog.V(2).Infof("Skipping service account cache for %s/%s/%s (user: %s)",
			req.Kind.Kind,
			req.Namespace,
			req.Name,
			req.UserInfo.Username)
	}

	// Allow the request (we're just observing, not mutating)
	return &admissionv1.AdmissionResponse{
		Allowed: true,
	}
}
