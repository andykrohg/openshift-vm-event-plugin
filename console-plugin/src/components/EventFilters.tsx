import React from 'react';
import {
  FormGroup,
  FormSelect,
  FormSelectOption,
  Flex,
  FlexItem,
  Dropdown,
  DropdownItem,
  DropdownList,
  MenuToggle,
} from '@patternfly/react-core';
import { DownloadIcon } from '@patternfly/react-icons';

export interface FilterValues {
  since?: string;
  severity?: 'normal' | 'warning';
  reason?: string;
  eventType?: string;
}

interface EventFiltersProps {
  filters: FilterValues;
  onFilterChange: (filters: FilterValues) => void;
  onExport: (format: 'json' | 'csv') => void;
}

export const EventFilters: React.FC<EventFiltersProps> = ({
  filters,
  onFilterChange,
  onExport,
}) => {
  const [isExportOpen, setIsExportOpen] = React.useState(false);

  const handleSinceChange = (_event: any, value: string) => {
    onFilterChange({ ...filters, since: value });
  };

  const handleSeverityChange = (_event: any, value: string) => {
    onFilterChange({
      ...filters,
      severity: value === 'all' ? undefined : (value as 'normal' | 'warning'),
    });
  };

  const handleEventTypeChange = (_event: any, value: string) => {
    onFilterChange({
      ...filters,
      eventType: value === 'all' ? undefined : value,
    });
  };

  return (
    <Flex>
      <FlexItem>
        <FormGroup label="Time range">
          <FormSelect value={filters.since || '24h'} onChange={handleSinceChange}>
            <FormSelectOption key="1h" value="1h" label="Last 1 hour" />
            <FormSelectOption key="6h" value="6h" label="Last 6 hours" />
            <FormSelectOption key="24h" value="24h" label="Last 24 hours" />
            <FormSelectOption key="7d" value="7d" label="Last 7 days" />
            <FormSelectOption key="30d" value="30d" label="Last 30 days" />
          </FormSelect>
        </FormGroup>
      </FlexItem>

      <FlexItem>
        <FormGroup label="Activity type">
          <FormSelect
            value={filters.eventType || 'all'}
            onChange={handleEventTypeChange}
          >
            <FormSelectOption key="all" value="all" label="All activities" />
            <FormSelectOption key="VMCreated" value="VMCreated" label="VM Created" />
            <FormSelectOption key="VMUpdated" value="VMUpdated" label="VM Updated" />
            <FormSelectOption key="VMDeleted" value="VMDeleted" label="VM Deleted" />
            <FormSelectOption key="Started" value="Started" label="VM Started" />
            <FormSelectOption key="Stopped" value="Stopped" label="VM Stopped" />
            <FormSelectOption key="Restarted" value="Restarted" label="VM Restarted" />
            <FormSelectOption key="ShuttingDown" value="ShuttingDown" label="VM Shutting down" />
            <FormSelectOption key="MigrationStarted" value="MigrationStarted" label="Migration Started" />
            <FormSelectOption key="MigrationSucceeded" value="MigrationSucceeded" label="Migration Succeeded" />
            <FormSelectOption key="MigrationFailed" value="MigrationFailed" label="Migration Failed" />
            <FormSelectOption key="CloneStarted" value="CloneStarted" label="Clone Started" />
            <FormSelectOption key="CloneSucceeded" value="CloneSucceeded" label="Clone Succeeded" />
            <FormSelectOption key="CloneFailed" value="CloneFailed" label="Clone Failed" />
            <FormSelectOption key="SnapshotCreated" value="SnapshotCreated" label="Snapshot Created" />
            <FormSelectOption key="SnapshotReady" value="SnapshotReady" label="Snapshot Ready" />
            <FormSelectOption key="SnapshotFailed" value="SnapshotFailed" label="Snapshot Failed" />
            <FormSelectOption key="SnapshotDeleted" value="SnapshotDeleted" label="Snapshot Deleted" />
            <FormSelectOption key="RestoreStarted" value="RestoreStarted" label="Restore Started" />
            <FormSelectOption key="RestoreComplete" value="RestoreComplete" label="Restore Complete" />
            <FormSelectOption key="RestoreFailed" value="RestoreFailed" label="Restore Failed" />
          </FormSelect>
        </FormGroup>
      </FlexItem>

      <FlexItem>
        <FormGroup label="Severity">
          <FormSelect
            value={filters.severity || 'all'}
            onChange={handleSeverityChange}
          >
            <FormSelectOption key="all" value="all" label="All" />
            <FormSelectOption key="normal" value="normal" label="Normal" />
            <FormSelectOption key="warning" value="warning" label="Warning" />
          </FormSelect>
        </FormGroup>
      </FlexItem>

      <FlexItem alignSelf={{ default: 'alignSelfFlexEnd' }}>
        <Dropdown
          isOpen={isExportOpen}
          onOpenChange={setIsExportOpen}
          toggle={(toggleRef) => (
            <MenuToggle
              ref={toggleRef}
              onClick={() => setIsExportOpen(!isExportOpen)}
              variant="secondary"
              icon={<DownloadIcon />}
            >
              Export
            </MenuToggle>
          )}
        >
          <DropdownList>
            <DropdownItem
              key="json"
              onClick={() => {
                onExport('json');
                setIsExportOpen(false);
              }}
            >
              Export as JSON
            </DropdownItem>
            <DropdownItem
              key="csv"
              onClick={() => {
                onExport('csv');
                setIsExportOpen(false);
              }}
            >
              Export as CSV
            </DropdownItem>
          </DropdownList>
        </Dropdown>
      </FlexItem>
    </Flex>
  );
};
