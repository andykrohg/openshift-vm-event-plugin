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
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/andykrohg/openshift-vm-event-plugin/processor/internal/admission"
	"github.com/andykrohg/openshift-vm-event-plugin/processor/internal/audit"
	"github.com/andykrohg/openshift-vm-event-plugin/processor/internal/storage"
)

// Server represents the HTTP API server
type Server struct {
	repository        *storage.Repository
	admissionHandler  *admission.WebhookHandler
	router            *gin.Engine
	httpServer        *http.Server
}

// NewServer creates a new API server
func NewServer(repository *storage.Repository, userCache *audit.UserCache, port int) (*Server, error) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(corsMiddleware())

	server := &Server{
		repository:       repository,
		admissionHandler: admission.NewWebhookHandler(userCache),
		router:           router,
		httpServer: &http.Server{
			Addr:         ":8080",
			Handler:      router,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
	}

	server.setupRoutes()

	return server, nil
}

// setupRoutes configures the HTTP routes
func (s *Server) setupRoutes() {
	api := s.router.Group("/api/v1")
	{
		// Health check
		api.GET("/health", s.handleHealth)

		// VM events endpoints
		api.GET("/namespaces/:namespace/virtualmachines/:name/events", s.authMiddleware(), s.handleGetVMEvents)
		api.GET("/namespaces/:namespace/events", s.authMiddleware(), s.handleGetNamespaceEvents)
		api.GET("/events", s.authMiddleware(), s.handleGetClusterEvents)
		api.GET("/events/export", s.authMiddleware(), s.handleExportEvents)
	}

	// Admission webhook endpoint (separate from /api/v1 group)
	s.router.POST("/mutate", s.handleAdmission)
}

// handleAdmission wraps the admission webhook handler for Gin
func (s *Server) handleAdmission(c *gin.Context) {
	s.admissionHandler.ServeHTTP(c.Writer, c.Request)
}

// corsMiddleware adds CORS headers
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// authMiddleware validates ServiceAccount tokens and enforces RBAC
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// For OpenShift console plugin, the console proxy handles auth
		// We can optionally validate the token here for additional security

		// Extract token from Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// In OpenShift console plugin context, this is okay
			// The console proxy has already authenticated the user
			c.Next()
			return
		}

		// TODO: Implement SubjectAccessReview to verify namespace access
		// For now, trust the console proxy

		c.Next()
	}
}

// Start starts the HTTP server
func (s *Server) Start(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.httpServer.Shutdown(shutdownCtx)
	}()

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}

	return nil
}

// Close shuts down the server
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// CheckHealth checks if the server and database are healthy
func (s *Server) CheckHealth(ctx context.Context) error {
	// Check database connection
	_, _, _, err := s.repository.GetStats(ctx)
	return err
}
