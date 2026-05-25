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

package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/andykrohg/openshift-vm-activity-plugin/processor/internal/storage"
)

// EventResponse represents an event in the API response
type EventResponse struct {
	ID              int64                  `json:"id"`
	EventUID        string                 `json:"eventUID"`
	VMName          string                 `json:"vmName"`
	VMNamespace     string                 `json:"vmNamespace"`
	EventType       string                 `json:"eventType"`
	Reason          string                 `json:"reason"`
	Message         string                 `json:"message"`
	SourceComponent string                 `json:"sourceComponent"`
	FirstTimestamp  string                 `json:"firstTimestamp"`
	LastTimestamp   string                 `json:"lastTimestamp"`
	Count           int32                  `json:"count"`
	Enrichment      map[string]interface{} `json:"enrichment,omitempty"`
	CreatedAt       string                 `json:"createdAt"`
}

// EventsListResponse represents the response for event list queries
type EventsListResponse struct {
	Events []EventResponse `json:"events"`
	Total  int64           `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

// handleHealth handles health check requests
func (s *Server) handleHealth(c *gin.Context) {
	if err := s.CheckHealth(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unhealthy",
			"error":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
	})
}

// handleGetVMActivitys handles GET /api/v1/namespaces/:namespace/virtualmachines/:name/events
func (s *Server) handleGetVMActivitys(c *gin.Context) {
	namespace := c.Param("namespace")
	vmName := c.Param("name")

	// Parse query parameters
	opts := storage.QueryOptions{
		Namespace: namespace,
		VMName:    vmName,
		Limit:     100, // default limit
	}

	// Parse 'since' parameter (e.g., "1h", "24h", "7d")
	if since := c.Query("since"); since != "" {
		duration, err := parseDuration(since)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid since parameter: %v", err)})
			return
		}
		sinceTime := time.Now().Add(-duration)
		opts.Since = &sinceTime
	}

	// Parse 'severity' parameter
	if severity := c.Query("severity"); severity != "" {
		if severity != "normal" && severity != "warning" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "severity must be 'normal' or 'warning'"})
			return
		}
		opts.Severity = severity
	}

	// Parse 'reason' parameter
	if reason := c.Query("reason"); reason != "" {
		opts.Reason = reason
	}

	// Parse 'limit' parameter
	if limitStr := c.Query("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit < 1 || limit > 1000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 1000"})
			return
		}
		opts.Limit = limit
	}

	// Parse 'offset' parameter
	if offsetStr := c.Query("offset"); offsetStr != "" {
		offset, err := strconv.Atoi(offsetStr)
		if err != nil || offset < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "offset must be >= 0"})
			return
		}
		opts.Offset = offset
	}

	// Query events
	events, total, err := s.repository.QueryEvents(c.Request.Context(), opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to query events: %v", err)})
		return
	}

	// Convert to response format
	eventResponses := make([]EventResponse, len(events))
	for i, event := range events {
		var enrichment map[string]interface{}
		if event.Enrichment != nil {
			json.Unmarshal(event.Enrichment, &enrichment)
		}

		eventResponses[i] = EventResponse{
			ID:              event.ID,
			EventUID:        event.EventUID,
			VMName:          event.VMName,
			VMNamespace:     event.VMNamespace,
			EventType:       event.EventType,
			Reason:          event.Reason,
			Message:         event.Message,
			SourceComponent: event.SourceComponent,
			FirstTimestamp:  event.FirstTimestamp.Format(time.RFC3339),
			LastTimestamp:   event.LastTimestamp.Format(time.RFC3339),
			Count:           event.Count,
			Enrichment:      enrichment,
			CreatedAt:       event.CreatedAt.Format(time.RFC3339),
		}
	}

	c.JSON(http.StatusOK, EventsListResponse{
		Events: eventResponses,
		Total:  total,
		Limit:  opts.Limit,
		Offset: opts.Offset,
	})
}

// handleGetNamespaceEvents handles GET /api/v1/namespaces/:namespace/events
func (s *Server) handleGetNamespaceEvents(c *gin.Context) {
	namespace := c.Param("namespace")

	// Parse query parameters
	opts := storage.QueryOptions{
		Namespace: namespace,
		// VMName is empty - query all VMs in namespace
		Limit: 100, // default limit
	}

	// Parse 'since' parameter (e.g., "1h", "24h", "7d")
	if since := c.Query("since"); since != "" {
		duration, err := parseDuration(since)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid since parameter: %v", err)})
			return
		}
		sinceTime := time.Now().Add(-duration)
		opts.Since = &sinceTime
	}

	// Parse 'severity' parameter
	if severity := c.Query("severity"); severity != "" {
		if severity != "normal" && severity != "warning" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "severity must be 'normal' or 'warning'"})
			return
		}
		opts.Severity = severity
	}

	// Parse 'reason' parameter
	if reason := c.Query("reason"); reason != "" {
		opts.Reason = reason
	}

	// Parse 'limit' parameter
	if limitStr := c.Query("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit < 1 || limit > 1000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 1000"})
			return
		}
		opts.Limit = limit
	}

	// Parse 'offset' parameter
	if offsetStr := c.Query("offset"); offsetStr != "" {
		offset, err := strconv.Atoi(offsetStr)
		if err != nil || offset < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "offset must be >= 0"})
			return
		}
		opts.Offset = offset
	}

	// Query events
	events, total, err := s.repository.QueryEvents(c.Request.Context(), opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to query events: %v", err)})
		return
	}

	// Convert to response format
	eventResponses := make([]EventResponse, len(events))
	for i, event := range events {
		var enrichment map[string]interface{}
		if event.Enrichment != nil {
			json.Unmarshal(event.Enrichment, &enrichment)
		}

		eventResponses[i] = EventResponse{
			ID:              event.ID,
			EventUID:        event.EventUID,
			VMName:          event.VMName,
			VMNamespace:     event.VMNamespace,
			EventType:       event.EventType,
			Reason:          event.Reason,
			Message:         event.Message,
			SourceComponent: event.SourceComponent,
			FirstTimestamp:  event.FirstTimestamp.Format(time.RFC3339),
			LastTimestamp:   event.LastTimestamp.Format(time.RFC3339),
			Count:           event.Count,
			Enrichment:      enrichment,
			CreatedAt:       event.CreatedAt.Format(time.RFC3339),
		}
	}

	c.JSON(http.StatusOK, EventsListResponse{
		Events: eventResponses,
		Total:  total,
		Limit:  opts.Limit,
		Offset: opts.Offset,
	})
}

// handleGetClusterEvents handles GET /api/v1/events
func (s *Server) handleGetClusterEvents(c *gin.Context) {
	// Parse query parameters
	opts := storage.QueryOptions{
		// Both Namespace and VMName are empty - query all events cluster-wide
		Limit: 100, // default limit
	}

	// Parse 'since' parameter (e.g., "1h", "24h", "7d")
	if since := c.Query("since"); since != "" {
		duration, err := parseDuration(since)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid since parameter: %v", err)})
			return
		}
		sinceTime := time.Now().Add(-duration)
		opts.Since = &sinceTime
	}

	// Parse 'severity' parameter
	if severity := c.Query("severity"); severity != "" {
		if severity != "normal" && severity != "warning" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "severity must be 'normal' or 'warning'"})
			return
		}
		opts.Severity = severity
	}

	// Parse 'reason' parameter
	if reason := c.Query("reason"); reason != "" {
		opts.Reason = reason
	}

	// Parse 'limit' parameter
	if limitStr := c.Query("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit < 1 || limit > 1000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 1000"})
			return
		}
		opts.Limit = limit
	}

	// Parse 'offset' parameter
	if offsetStr := c.Query("offset"); offsetStr != "" {
		offset, err := strconv.Atoi(offsetStr)
		if err != nil || offset < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "offset must be >= 0"})
			return
		}
		opts.Offset = offset
	}

	// Query events
	events, total, err := s.repository.QueryEvents(c.Request.Context(), opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to query events: %v", err)})
		return
	}

	// Convert to response format
	eventResponses := make([]EventResponse, len(events))
	for i, event := range events {
		var enrichment map[string]interface{}
		if event.Enrichment != nil {
			json.Unmarshal(event.Enrichment, &enrichment)
		}

		eventResponses[i] = EventResponse{
			ID:              event.ID,
			EventUID:        event.EventUID,
			VMName:          event.VMName,
			VMNamespace:     event.VMNamespace,
			EventType:       event.EventType,
			Reason:          event.Reason,
			Message:         event.Message,
			SourceComponent: event.SourceComponent,
			FirstTimestamp:  event.FirstTimestamp.Format(time.RFC3339),
			LastTimestamp:   event.LastTimestamp.Format(time.RFC3339),
			Count:           event.Count,
			Enrichment:      enrichment,
			CreatedAt:       event.CreatedAt.Format(time.RFC3339),
		}
	}

	c.JSON(http.StatusOK, EventsListResponse{
		Events: eventResponses,
		Total:  total,
		Limit:  opts.Limit,
		Offset: opts.Offset,
	})
}

// handleExportEvents handles GET /api/v1/events/export
func (s *Server) handleExportEvents(c *gin.Context) {
	format := c.DefaultQuery("format", "json")
	namespace := c.Query("namespace")
	vmName := c.Query("vm")

	opts := storage.QueryOptions{
		Namespace: namespace,
		VMName:    vmName,
		Limit:     10000, // max export size
	}

	// Parse 'since' parameter
	if since := c.Query("since"); since != "" {
		duration, err := parseDuration(since)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid since parameter: %v", err)})
			return
		}
		sinceTime := time.Now().Add(-duration)
		opts.Since = &sinceTime
	}

	// Query events
	events, _, err := s.repository.QueryEvents(c.Request.Context(), opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to query events: %v", err)})
		return
	}

	switch format {
	case "csv":
		s.exportCSV(c, events)
	case "json":
		s.exportJSON(c, events)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "format must be 'json' or 'csv'"})
	}
}

// exportJSON exports events as JSON
func (s *Server) exportJSON(c *gin.Context, events []storage.VMActivity) {
	eventResponses := make([]EventResponse, len(events))
	for i, event := range events {
		var enrichment map[string]interface{}
		if event.Enrichment != nil {
			json.Unmarshal(event.Enrichment, &enrichment)
		}

		eventResponses[i] = EventResponse{
			ID:              event.ID,
			EventUID:        event.EventUID,
			VMName:          event.VMName,
			VMNamespace:     event.VMNamespace,
			EventType:       event.EventType,
			Reason:          event.Reason,
			Message:         event.Message,
			SourceComponent: event.SourceComponent,
			FirstTimestamp:  event.FirstTimestamp.Format(time.RFC3339),
			LastTimestamp:   event.LastTimestamp.Format(time.RFC3339),
			Count:           event.Count,
			Enrichment:      enrichment,
			CreatedAt:       event.CreatedAt.Format(time.RFC3339),
		}
	}

	c.Header("Content-Disposition", "attachment; filename=vm-activity.json")
	c.JSON(http.StatusOK, eventResponses)
}

// exportCSV exports events as CSV
func (s *Server) exportCSV(c *gin.Context, events []storage.VMActivity) {
	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment; filename=vm-activity.csv")

	writer := csv.NewWriter(c.Writer)
	defer writer.Flush()

	// Write header
	writer.Write([]string{
		"ID", "EventUID", "VMName", "VMNamespace", "EventType", "Reason",
		"Message", "SourceComponent", "FirstTimestamp", "LastTimestamp",
		"Count", "CreatedAt",
	})

	// Write rows
	for _, event := range events {
		writer.Write([]string{
			strconv.FormatInt(event.ID, 10),
			event.EventUID,
			event.VMName,
			event.VMNamespace,
			event.EventType,
			event.Reason,
			event.Message,
			event.SourceComponent,
			event.FirstTimestamp.Format(time.RFC3339),
			event.LastTimestamp.Format(time.RFC3339),
			strconv.FormatInt(int64(event.Count), 10),
			event.CreatedAt.Format(time.RFC3339),
		})
	}
}

// parseDuration parses duration strings like "1h", "24h", "7d"
func parseDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration format")
	}

	value, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, fmt.Errorf("invalid duration value: %w", err)
	}

	unit := s[len(s)-1:]
	switch unit {
	case "h":
		return time.Duration(value) * time.Hour, nil
	case "d":
		return time.Duration(value) * 24 * time.Hour, nil
	case "w":
		return time.Duration(value) * 7 * 24 * time.Hour, nil
	case "m":
		return time.Duration(value) * time.Minute, nil
	default:
		return 0, fmt.Errorf("unsupported duration unit: %s (use h, d, w, or m)", unit)
	}
}
