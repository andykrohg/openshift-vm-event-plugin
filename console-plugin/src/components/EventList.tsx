import React, { useState } from 'react';
import {
  DataList,
  DataListItem,
  DataListItemRow,
  DataListItemCells,
  DataListCell,
  Label,
  Content,
  ContentVariants,
  ExpandableSection,
  DescriptionList,
  DescriptionListGroup,
  DescriptionListTerm,
  DescriptionListDescription,
} from '@patternfly/react-core';
import {
  ExclamationTriangleIcon,
  CheckCircleIcon,
} from '@patternfly/react-icons';
import { VMEvent } from '../utils/api';

interface EventListProps {
  events: VMEvent[];
}

const EventItem: React.FC<{ event: VMEvent }> = ({ event }) => {
  const [isExpanded, setIsExpanded] = useState(false);

  const isWarning = event.eventType === 'Warning';
  const icon = isWarning ? (
    <ExclamationTriangleIcon color="var(--pf-v5-global--warning-color--100)" />
  ) : (
    <CheckCircleIcon color="var(--pf-v5-global--success-color--100)" />
  );

  const formatDate = (dateString: string) => {
    const date = new Date(dateString);
    return date.toLocaleString();
  };

  const formatRelativeTime = (dateString: string) => {
    const date = new Date(dateString);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    const diffHours = Math.floor(diffMins / 60);
    const diffDays = Math.floor(diffHours / 24);

    if (diffMins < 1) return 'just now';
    if (diffMins < 60) return `${diffMins}m ago`;
    if (diffHours < 24) return `${diffHours}h ago`;
    return `${diffDays}d ago`;
  };

  return (
    <DataListItem>
      <DataListItemRow>
        <DataListItemCells
          dataListCells={[
            <DataListCell key="icon" width={1}>
              {icon}
            </DataListCell>,
            <DataListCell key="main" width={4}>
              <div>
                <Label color={isWarning ? 'orange' : 'blue'}>{event.reason}</Label>
                <Content component={ContentVariants.small} style={{ marginLeft: '8px' }}>
                  {formatRelativeTime(event.lastTimestamp)}
                  {event.count > 1 && ` (occurred ${event.count} times)`}
                </Content>
              </div>
              <Content component={ContentVariants.p}>{event.message}</Content>

              <ExpandableSection
                toggleText={isExpanded ? 'Hide details' : 'Show details'}
                onToggle={() => setIsExpanded(!isExpanded)}
                isExpanded={isExpanded}
              >
                <DescriptionList isCompact>
                  <DescriptionListGroup>
                    <DescriptionListTerm>First occurrence</DescriptionListTerm>
                    <DescriptionListDescription>
                      {formatDate(event.firstTimestamp)}
                    </DescriptionListDescription>
                  </DescriptionListGroup>

                  <DescriptionListGroup>
                    <DescriptionListTerm>Last occurrence</DescriptionListTerm>
                    <DescriptionListDescription>
                      {formatDate(event.lastTimestamp)}
                    </DescriptionListDescription>
                  </DescriptionListGroup>

                  <DescriptionListGroup>
                    <DescriptionListTerm>Source</DescriptionListTerm>
                    <DescriptionListDescription>
                      {event.sourceComponent}
                    </DescriptionListDescription>
                  </DescriptionListGroup>

                  <DescriptionListGroup>
                    <DescriptionListTerm>Type</DescriptionListTerm>
                    <DescriptionListDescription>
                      {event.eventType}
                    </DescriptionListDescription>
                  </DescriptionListGroup>

                  {event.enrichment?.snapshotName && (
                    <DescriptionListGroup>
                      <DescriptionListTerm>Snapshot</DescriptionListTerm>
                      <DescriptionListDescription>
                        {event.enrichment.snapshotName}
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                  )}

                  {event.enrichment?.patch && (() => {
                    try {
                      const patch = JSON.parse(event.enrichment.patch);
                      return (
                        <DescriptionListGroup>
                          <DescriptionListTerm>Configuration changes</DescriptionListTerm>
                          <DescriptionListDescription>
                            <pre style={{ fontSize: '12px', margin: 0, whiteSpace: 'pre-wrap' }}>
                              {JSON.stringify(patch, null, 2)}
                            </pre>
                          </DescriptionListDescription>
                        </DescriptionListGroup>
                      );
                    } catch (e) {
                      return null;
                    }
                  })()}

                  {event.enrichment && (() => {
                    // Remove user, patch, and snapshotName fields since they have dedicated fields
                    const { user, userGroup, patch, snapshotName, ...otherEnrichment } = event.enrichment;
                    return Object.keys(otherEnrichment).length > 0 ? (
                      <DescriptionListGroup>
                        <DescriptionListTerm>Additional context</DescriptionListTerm>
                        <DescriptionListDescription>
                          <pre style={{ fontSize: '12px', margin: 0 }}>
                            {JSON.stringify(otherEnrichment, null, 2)}
                          </pre>
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                    ) : null;
                  })()}
                </DescriptionList>
              </ExpandableSection>
            </DataListCell>,
            <DataListCell key="user" width={2}>
              <Content component={ContentVariants.small} style={{ color: 'var(--pf-v5-global--Color--200)' }}>
                User
              </Content>
              <Content component={ContentVariants.p}>
                {event.enrichment?.user || '—'}
              </Content>
            </DataListCell>,
          ]}
        />
      </DataListItemRow>
    </DataListItem>
  );
};

export const EventList: React.FC<EventListProps> = ({ events }) => {
  if (!events || events.length === 0) {
    return null;
  }

  return (
    <DataList aria-label="VM Events">
      {events.map((event) => (
        <EventItem key={event.id} event={event} />
      ))}
    </DataList>
  );
};
