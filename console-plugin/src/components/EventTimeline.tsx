console.log('VM Events Plugin: EventTimeline module loading');

import React, { useState, ErrorInfo, Component } from 'react';
import {
  Card,
  CardBody,
  EmptyState,
  EmptyStateBody,
  Spinner,
  Alert,
  Pagination,
  Toolbar,
  ToolbarContent,
  ToolbarItem,
} from '@patternfly/react-core';
import { SearchIcon } from '@patternfly/react-icons';
import { QueryClient, QueryClientProvider } from 'react-query';
import { useVMEvents } from '../hooks/useVMEvents';
import { EventFilters, FilterValues } from './EventFilters';
import { EventList } from './EventList';
import { exportEvents } from '../utils/api';

const queryClient = new QueryClient();

class ErrorBoundary extends Component<
  { children: React.ReactNode },
  { hasError: boolean; error?: Error }
> {
  constructor(props: { children: React.ReactNode }) {
    super(props);
    this.state = { hasError: false };
  }

  static getDerivedStateFromError(error: Error) {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error('EventHistoryTab Error:', error, errorInfo);
  }

  render() {
    if (this.state.hasError) {
      return (
        <Card>
          <CardBody>
            <Alert variant="danger" title="Error loading Event History">
              {this.state.error?.message || 'An unexpected error occurred'}
            </Alert>
          </CardBody>
        </Card>
      );
    }

    return this.props.children;
  }
}

interface EventHistoryTabProps {
  obj?: {
    metadata?: {
      name?: string;
      namespace?: string;
    };
  };
}

const EventHistoryTabContent: React.FC<EventHistoryTabProps> = ({ obj }) => {
  // Validate obj prop
  if (!obj || !obj.metadata || !obj.metadata.name || !obj.metadata.namespace) {
    return (
      <Card>
        <CardBody>
          <Alert variant="warning" title="Unable to load event history">
            Resource metadata is missing or invalid.
          </Alert>
        </CardBody>
      </Card>
    );
  }

  const { name: vmName, namespace } = obj.metadata;

  const [filters, setFilters] = useState<FilterValues>({
    since: '24h',
    severity: undefined,
    reason: undefined,
    eventType: undefined,
  });

  const [pagination, setPagination] = useState({
    page: 1,
    perPage: 50,
  });

  const { data, isLoading, isError, error } = useVMEvents({
    namespace,
    vmName,
    since: filters.since,
    severity: filters.severity,
    reason: filters.eventType, // Map eventType to reason parameter
    limit: pagination.perPage,
    offset: (pagination.page - 1) * pagination.perPage,
  });

  const handleFilterChange = (newFilters: FilterValues) => {
    setFilters(newFilters);
    setPagination({ ...pagination, page: 1 }); // Reset to first page
  };

  const handlePageChange = (_event: any, newPage: number) => {
    setPagination({ ...pagination, page: newPage });
  };

  const handlePerPageChange = (_event: any, newPerPage: number) => {
    setPagination({ page: 1, perPage: newPerPage });
  };

  const handleExport = async (format: 'json' | 'csv') => {
    try {
      await exportEvents(namespace, vmName, format);
    } catch (err) {
      console.error('Export failed:', err);
    }
  };

  if (isLoading) {
    return (
      <Card>
        <CardBody>
          <EmptyState>
            <Spinner size="xl" />
            <EmptyStateBody>Loading event history...</EmptyStateBody>
          </EmptyState>
        </CardBody>
      </Card>
    );
  }

  if (isError) {
    return (
      <Card>
        <CardBody>
          <Alert variant="danger" title="Error loading events">
            {error?.message || 'An unknown error occurred'}
          </Alert>
        </CardBody>
      </Card>
    );
  }

  const events = data?.events || [];
  const total = data?.total || 0;

  return (
    <Card>
      <CardBody>
        <Toolbar>
          <ToolbarContent>
            <ToolbarItem style={{ flex: 1 }}>
              <EventFilters
                filters={filters}
                onFilterChange={handleFilterChange}
                onExport={handleExport}
              />
            </ToolbarItem>
            <ToolbarItem variant="pagination">
              <Pagination
                itemCount={total}
                perPage={pagination.perPage}
                page={pagination.page}
                onSetPage={handlePageChange}
                onPerPageSelect={handlePerPageChange}
                variant="top"
              />
            </ToolbarItem>
          </ToolbarContent>
        </Toolbar>

        {events.length === 0 ? (
          <EmptyState>
            <SearchIcon style={{ fontSize: '3rem', marginBottom: '1rem' }} />
            <EmptyStateBody>
              <h2>No events found</h2>
              <p>No events match the current filters. Try adjusting the time range or filters.</p>
            </EmptyStateBody>
          </EmptyState>
        ) : (
          <>
            <EventList events={events} />
            <Pagination
              itemCount={total}
              perPage={pagination.perPage}
              page={pagination.page}
              onSetPage={handlePageChange}
              onPerPageSelect={handlePerPageChange}
              variant="bottom"
            />
          </>
        )}
      </CardBody>
    </Card>
  );
};

const EventHistoryTab: React.FC<EventHistoryTabProps> = (props) => {
  // Log props for debugging
  console.log('EventHistoryTab props:', props);

  return (
    <ErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <EventHistoryTabContent {...props} />
      </QueryClientProvider>
    </ErrorBoundary>
  );
};

console.log('VM Events Plugin: EventHistoryTab component defined');

export default EventHistoryTab;
