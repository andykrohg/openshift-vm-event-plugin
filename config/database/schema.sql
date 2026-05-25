-- VM Activity table for storing activity history
CREATE TABLE IF NOT EXISTS vm_activity (
    id BIGSERIAL PRIMARY KEY,
    event_uid VARCHAR(36) UNIQUE NOT NULL,
    vm_name VARCHAR(253) NOT NULL,
    vm_namespace VARCHAR(63) NOT NULL,
    event_type VARCHAR(20) NOT NULL,  -- Normal/Warning
    reason VARCHAR(100) NOT NULL,
    message TEXT,
    source_component VARCHAR(100),
    first_timestamp TIMESTAMPTZ NOT NULL,
    last_timestamp TIMESTAMPTZ NOT NULL,
    count INT DEFAULT 1,
    enrichment JSONB,  -- {user, relatedVMI, node, annotations}
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Index for querying activity by VM
CREATE INDEX IF NOT EXISTS idx_vm_lookup ON vm_activity(vm_namespace, vm_name, last_timestamp DESC);

-- Index for time-based queries
CREATE INDEX IF NOT EXISTS idx_timestamp ON vm_activity(last_timestamp DESC);

-- Index for retention cleanup
CREATE INDEX IF NOT EXISTS idx_retention ON vm_activity(created_at);

-- Index for querying by reason
CREATE INDEX IF NOT EXISTS idx_reason ON vm_activity(reason);

-- Index for JSONB enrichment queries
CREATE INDEX IF NOT EXISTS idx_enrichment ON vm_activity USING GIN (enrichment);

-- Comments for documentation
COMMENT ON TABLE vm_activity IS 'Stores historical VM activity from OpenShift Virtualization';
COMMENT ON COLUMN vm_activity.event_uid IS 'Kubernetes Event UID for deduplication';
COMMENT ON COLUMN vm_activity.enrichment IS 'Additional context extracted from the activity (user, node, etc.)';
COMMENT ON COLUMN vm_activity.count IS 'Number of times this activity occurred (for aggregated events)';
