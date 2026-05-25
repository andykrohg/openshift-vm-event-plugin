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

package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VMEvent represents a VM event in the database
type VMEvent struct {
	ID              int64
	EventUID        string
	VMName          string
	VMNamespace     string
	EventType       string
	Reason          string
	Message         string
	SourceComponent string
	FirstTimestamp  time.Time
	LastTimestamp   time.Time
	Count           int32
	Enrichment      []byte // JSONB
	CreatedAt       time.Time
}

// QueryOptions defines options for querying events
type QueryOptions struct {
	Namespace   string
	VMName      string
	Since       *time.Time
	Severity    string // "normal" or "warning"
	Reason      string
	Limit       int
	Offset      int
}

// Repository handles database operations for VM events
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new repository instance
func NewRepository(connectionString string) (*Repository, error) {
	config, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse connection string: %w", err)
	}

	// Configure connection pool
	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test connection
	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &Repository{pool: pool}, nil
}

// InitializeSchema creates the database schema if it doesn't exist
func (r *Repository) InitializeSchema(ctx context.Context) error {
	query := `
		CREATE TABLE IF NOT EXISTS vm_events (
			id BIGSERIAL PRIMARY KEY,
			event_uid VARCHAR(36) UNIQUE NOT NULL,
			vm_name VARCHAR(253) NOT NULL,
			vm_namespace VARCHAR(63) NOT NULL,
			event_type VARCHAR(20) NOT NULL,
			reason VARCHAR(100) NOT NULL,
			message TEXT,
			source_component VARCHAR(100),
			first_timestamp TIMESTAMPTZ NOT NULL,
			last_timestamp TIMESTAMPTZ NOT NULL,
			count INT DEFAULT 1,
			enrichment JSONB,
			created_at TIMESTAMPTZ DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_vm_lookup ON vm_events(vm_namespace, vm_name, last_timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_timestamp ON vm_events(last_timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_retention ON vm_events(created_at);
	`

	_, err := r.pool.Exec(ctx, query)
	return err
}

// InsertEvents inserts a batch of events into the database
func (r *Repository) InsertEvents(ctx context.Context, events []VMEvent) error {
	if len(events) == 0 {
		return nil
	}

	batch := &pgx.Batch{}

	for _, event := range events {
		batch.Queue(`
			INSERT INTO vm_events (
				event_uid, vm_name, vm_namespace, event_type, reason, message,
				source_component, first_timestamp, last_timestamp, count, enrichment
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (event_uid) DO UPDATE SET
				count = vm_events.count + EXCLUDED.count,
				last_timestamp = EXCLUDED.last_timestamp,
				message = EXCLUDED.message
		`,
			event.EventUID,
			event.VMName,
			event.VMNamespace,
			event.EventType,
			event.Reason,
			event.Message,
			event.SourceComponent,
			event.FirstTimestamp,
			event.LastTimestamp,
			event.Count,
			event.Enrichment,
		)
	}

	results := r.pool.SendBatch(ctx, batch)
	defer results.Close()

	// Consume all results
	for i := 0; i < len(events); i++ {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("failed to insert event %d: %w", i, err)
		}
	}

	return nil
}

// QueryEvents retrieves events based on query options
func (r *Repository) QueryEvents(ctx context.Context, opts QueryOptions) ([]VMEvent, int64, error) {
	// Build dynamic query
	baseQuery := `
		SELECT id, event_uid, vm_name, vm_namespace, event_type, reason, message,
		       source_component, first_timestamp, last_timestamp, count, enrichment, created_at
		FROM vm_events
		WHERE 1=1
	`
	countQuery := "SELECT COUNT(*) FROM vm_events WHERE 1=1"

	args := []interface{}{}
	argIndex := 1

	if opts.Namespace != "" {
		clause := fmt.Sprintf(" AND vm_namespace = $%d", argIndex)
		baseQuery += clause
		countQuery += clause
		args = append(args, opts.Namespace)
		argIndex++
	}

	if opts.VMName != "" {
		clause := fmt.Sprintf(" AND vm_name = $%d", argIndex)
		baseQuery += clause
		countQuery += clause
		args = append(args, opts.VMName)
		argIndex++
	}

	if opts.Since != nil {
		clause := fmt.Sprintf(" AND last_timestamp >= $%d", argIndex)
		baseQuery += clause
		countQuery += clause
		args = append(args, *opts.Since)
		argIndex++
	}

	if opts.Severity != "" {
		severityType := "Normal"
		if opts.Severity == "warning" {
			severityType = "Warning"
		}
		clause := fmt.Sprintf(" AND event_type = $%d", argIndex)
		baseQuery += clause
		countQuery += clause
		args = append(args, severityType)
		argIndex++
	}

	if opts.Reason != "" {
		clause := fmt.Sprintf(" AND reason = $%d", argIndex)
		baseQuery += clause
		countQuery += clause
		args = append(args, opts.Reason)
		argIndex++
	}

	// Get total count
	var total int64
	if err := r.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count events: %w", err)
	}

	// Add ordering, limit, offset
	baseQuery += " ORDER BY last_timestamp DESC"
	if opts.Limit > 0 {
		baseQuery += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, opts.Limit)
		argIndex++
	}
	if opts.Offset > 0 {
		baseQuery += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, opts.Offset)
		argIndex++
	}

	// Execute query
	rows, err := r.pool.Query(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query events: %w", err)
	}
	defer rows.Close()

	events := []VMEvent{}
	for rows.Next() {
		var event VMEvent
		if err := rows.Scan(
			&event.ID,
			&event.EventUID,
			&event.VMName,
			&event.VMNamespace,
			&event.EventType,
			&event.Reason,
			&event.Message,
			&event.SourceComponent,
			&event.FirstTimestamp,
			&event.LastTimestamp,
			&event.Count,
			&event.Enrichment,
			&event.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan event: %w", err)
		}
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating events: %w", err)
	}

	return events, total, nil
}

// DeleteOldEvents deletes events older than the specified retention period
func (r *Repository) DeleteOldEvents(ctx context.Context, retentionDays int32) (int64, error) {
	query := `
		DELETE FROM vm_events
		WHERE created_at < NOW() - INTERVAL '1 day' * $1
	`

	result, err := r.pool.Exec(ctx, query, retentionDays)
	if err != nil {
		return 0, fmt.Errorf("failed to delete old events: %w", err)
	}

	return result.RowsAffected(), nil
}

// GetStats returns statistics about stored events
func (r *Repository) GetStats(ctx context.Context) (totalEvents int64, oldestEvent *time.Time, newestEvent *time.Time, err error) {
	query := `
		SELECT
			COUNT(*) as total,
			MIN(created_at) as oldest,
			MAX(last_timestamp) as newest
		FROM vm_events
	`

	var oldest, newest *time.Time
	err = r.pool.QueryRow(ctx, query).Scan(&totalEvents, &oldest, &newest)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("failed to get stats: %w", err)
	}

	return totalEvents, oldest, newest, nil
}

// Close closes the database connection pool
func (r *Repository) Close() {
	r.pool.Close()
}
