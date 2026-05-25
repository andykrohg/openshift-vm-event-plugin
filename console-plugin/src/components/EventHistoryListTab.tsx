import React, { useState } from 'react';
import {
  Page,
  PageSection,
  Title,
  Card,
  CardBody,
  Flex,
  FlexItem,
  EmptyState,
  EmptyStateBody,
  Spinner,
} from '@patternfly/react-core';
import { SearchIcon } from '@patternfly/react-icons';
import { EventFilters, FilterValues } from './EventFilters';
import { EventList } from './EventList';
import { useQuery } from 'react-query';
import { fetchJSON } from '../utils/api';

interface EventsResponse {
  events: any[];
  total: number;
  limit: number;
  offset: number;
}

const EventHistoryListTab: React.FC = () => {
  const [filters, setFilters] = useState<FilterValues>({ since: '24h' });

  // Get current namespace from URL or console context
  // OpenShift console sets the active namespace in the URL
  const getCurrentNamespace = () => {
    const match = window.location.pathname.match(/\/ns\/([^/]+)/);
    return match ? match[1] : null;
  };

  const currentNamespace = getCurrentNamespace();

  // Build API path based on namespace context
  const apiPath = currentNamespace
    ? `/api/v1/namespaces/${currentNamespace}/events`
    : '/api/v1/events';

  // Build query params
  const queryParams = new URLSearchParams();
  if (filters.since) queryParams.append('since', filters.since);
  if (filters.severity) queryParams.append('severity', filters.severity);
  if (filters.reason) queryParams.append('reason', filters.reason);
  if (filters.eventType) queryParams.append('reason', filters.eventType);

  const fullPath = `${apiPath}?${queryParams.toString()}`;

  const { data, isLoading, error } = useQuery<EventsResponse, Error>(
    ['vmEvents', apiPath, filters],
    () => fetchJSON(fullPath),
    {
      refetchInterval: 30000,
      staleTime: 10000,
    }
  );

  const handleExport = (format: 'json' | 'csv') => {
    const params = new URLSearchParams();
    if (filters.since) params.append('since', filters.since);
    if (filters.severity) params.append('severity', filters.severity);
    if (filters.reason) params.append('reason', filters.reason);
    if (currentNamespace) params.append('namespace', currentNamespace);
    params.append('format', format);

    const url = `/api/proxy/plugin/vm-events/api/v1/events/export?${params.toString()}`;
    window.open(url, '_blank');
  };

  const scopeLabel = currentNamespace
    ? `namespace: ${currentNamespace}`
    : 'cluster-wide (all namespaces)';

  return (
    <Page>
      <PageSection>
        <Title headingLevel="h1">Virtual Machine Event History</Title>
        <Title headingLevel="h2" size="md" style={{ marginTop: '8px', color: 'var(--pf-v5-global--Color--200)' }}>
          Viewing events for {scopeLabel}
        </Title>
      </PageSection>

      <PageSection>
        <Card>
          <CardBody>
            <Flex direction={{ default: 'column' }} spaceItems={{ default: 'spaceItemsMd' }}>
              {/* Event Filters */}
              <FlexItem>
                <EventFilters
                  filters={filters}
                  onFilterChange={setFilters}
                  onExport={handleExport}
                />
              </FlexItem>

              {/* Event List */}
              <FlexItem>
                {isLoading ? (
                  <EmptyState>
                    <Spinner size="xl" />
                    <EmptyStateBody>Loading event history...</EmptyStateBody>
                  </EmptyState>
                ) : error ? (
                  <EmptyState>
                    <EmptyStateBody>
                      <h2>Error loading events</h2>
                      <p>Failed to load event history. Please try again.</p>
                    </EmptyStateBody>
                  </EmptyState>
                ) : data && data.events.length > 0 ? (
                  <>
                    <Title headingLevel="h3" size="md" style={{ marginBottom: '16px' }}>
                      {data.total} event{data.total !== 1 ? 's' : ''} found
                    </Title>
                    <EventList events={data.events} />
                  </>
                ) : (
                  <EmptyState>
                    <SearchIcon style={{ fontSize: '3rem', marginBottom: '1rem' }} />
                    <EmptyStateBody>
                      <h2>No events found</h2>
                      <p>No events match the current filters. Try adjusting the time range or filters.</p>
                    </EmptyStateBody>
                  </EmptyState>
                )}
              </FlexItem>
            </Flex>
          </CardBody>
        </Card>
      </PageSection>
    </Page>
  );
};

export default EventHistoryListTab;
