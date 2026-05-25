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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/andykrohg/openshift-vm-event-plugin/processor/internal/aggregator"
	"github.com/andykrohg/openshift-vm-event-plugin/processor/internal/api"
	"github.com/andykrohg/openshift-vm-event-plugin/processor/internal/audit"
	"github.com/andykrohg/openshift-vm-event-plugin/processor/internal/storage"
	"github.com/andykrohg/openshift-vm-event-plugin/processor/internal/watcher"
)

type Config struct {
	DBConnection             string
	RetentionDays            int32
	AggregationWindowMinutes int32
	APIPort                  int
	FilterReasons            []string
}

func main() {
	klog.InitFlags(nil)
	var kubeconfig string
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (optional, uses in-cluster config if not specified)")
	flag.Parse()

	klog.Info("Starting VM Event Processor")

	// Load configuration from environment variables
	config := loadConfig()

	// Set up signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Create Kubernetes client
	k8sConfig, err := getK8sConfig(kubeconfig)
	if err != nil {
		klog.Fatalf("Failed to get Kubernetes config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		klog.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	dynamicClient, err := dynamic.NewForConfig(k8sConfig)
	if err != nil {
		klog.Fatalf("Failed to create dynamic client: %v", err)
	}

	// Initialize database repository
	repo, err := storage.NewRepository(config.DBConnection)
	if err != nil {
		klog.Fatalf("Failed to connect to database: %v", err)
	}
	defer repo.Close()

	// Initialize database schema
	if err := repo.InitializeSchema(ctx); err != nil {
		klog.Fatalf("Failed to initialize database schema: %v", err)
	}
	klog.Info("Database schema initialized")

	// Create user cache for audit events
	userCache := audit.NewUserCache()

	// Create event processor
	processor := aggregator.NewEventProcessor(repo, dynamicClient, userCache, config.AggregationWindowMinutes)
	defer processor.Shutdown()

	// Start event watcher
	eventWatcher := watcher.NewEventWatcher(clientset, processor, config.FilterReasons)
	go func() {
		if err := eventWatcher.Start(ctx); err != nil {
			klog.Errorf("Event watcher error: %v", err)
			cancel()
		}
	}()
	klog.Info("Event watcher started")

	// Start API server
	apiServer, err := api.NewServer(repo, userCache, config.APIPort)
	if err != nil {
		klog.Fatalf("Failed to create API server: %v", err)
	}

	go func() {
		klog.Infof("Starting API server on port %d", config.APIPort)
		if err := apiServer.Start(ctx); err != nil {
			klog.Errorf("API server error: %v", err)
			cancel()
		}
	}()

	// Log configuration
	klog.Infof("Configuration: RetentionDays=%d, AggregationWindow=%dm, APIPort=%d",
		config.RetentionDays, config.AggregationWindowMinutes, config.APIPort)
	if len(config.FilterReasons) > 0 {
		klog.Infof("Filtering event reasons: %v", config.FilterReasons)
	}

	// Wait for shutdown signal
	<-ctx.Done()
	klog.Info("Shutting down gracefully...")

	// Give components time to shut down
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := apiServer.Close(); err != nil {
		klog.Errorf("Error shutting down API server: %v", err)
	}

	<-shutdownCtx.Done()
	klog.Info("Shutdown complete")
}

func loadConfig() Config {
	config := Config{
		DBConnection:             getEnv("DB_CONNECTION", ""),
		RetentionDays:            getEnvInt32("RETENTION_DAYS", 30),
		AggregationWindowMinutes: getEnvInt32("AGGREGATION_WINDOW_MINUTES", 5),
		APIPort:                  getEnvInt("API_PORT", 8080),
	}

	// Parse filter reasons (comma-separated)
	if filterStr := os.Getenv("FILTER_REASONS"); filterStr != "" {
		config.FilterReasons = parseCommaSeparated(filterStr)
	}

	if config.DBConnection == "" {
		klog.Fatal("DB_CONNECTION environment variable is required")
	}

	return config
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvInt32(key string, defaultValue int32) int32 {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.ParseInt(value, 10, 32); err == nil {
			return int32(intVal)
		}
	}
	return defaultValue
}

func parseCommaSeparated(s string) []string {
	var result []string
	for i := 0; i < len(s); {
		// Skip leading spaces
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i >= len(s) {
			break
		}
		// Find next comma
		start := i
		for i < len(s) && s[i] != ',' {
			i++
		}
		// Add trimmed value
		if val := s[start:i]; val != "" {
			result = append(result, val)
		}
		if i < len(s) {
			i++ // skip comma
		}
	}
	return result
}

func getK8sConfig(kubeconfig string) (*rest.Config, error) {
	// Try in-cluster config first
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	// Fall back to kubeconfig file
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		kubeconfig = fmt.Sprintf("%s/.kube/config", home)
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
	}

	return config, nil
}
