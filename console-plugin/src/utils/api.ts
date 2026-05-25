export interface VMEvent {
  id: number;
  eventUID: string;
  vmName: string;
  vmNamespace: string;
  eventType: 'Normal' | 'Warning';
  reason: string;
  message: string;
  sourceComponent: string;
  firstTimestamp: string;
  lastTimestamp: string;
  count: number;
  enrichment?: Record<string, any>;
  createdAt: string;
}

export interface EventsResponse {
  events: VMEvent[];
  total: number;
  limit: number;
  offset: number;
}

export interface QueryParams {
  since?: string;
  severity?: 'normal' | 'warning';
  reason?: string;
  limit?: number;
  offset?: number;
}

const API_BASE_PATH = '/api/proxy/plugin/vm-events-plugin/vm-events/api/v1';

export async function fetchVMEvents(
  namespace: string,
  vmName: string,
  params: QueryParams = {}
): Promise<EventsResponse> {
  const queryString = new URLSearchParams(
    Object.entries(params).reduce((acc, [key, value]) => {
      if (value !== undefined) {
        acc[key] = String(value);
      }
      return acc;
    }, {} as Record<string, string>)
  ).toString();

  const url = `${API_BASE_PATH}/namespaces/${namespace}/virtualmachines/${vmName}/events${
    queryString ? `?${queryString}` : ''
  }`;

  const response = await fetch(url, {
    method: 'GET',
    headers: {
      'Content-Type': 'application/json',
    },
  });

  if (!response.ok) {
    throw new Error(`Failed to fetch events: ${response.statusText}`);
  }

  return response.json();
}

export async function exportEvents(
  namespace: string,
  vmName: string,
  format: 'json' | 'csv' = 'json'
): Promise<void> {
  const url = `${API_BASE_PATH}/events/export?format=${format}&namespace=${namespace}&vm=${vmName}`;

  const response = await fetch(url, {
    method: 'GET',
  });

  if (!response.ok) {
    throw new Error(`Failed to export events: ${response.statusText}`);
  }

  // Trigger download
  const blob = await response.blob();
  const downloadUrl = window.URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = downloadUrl;
  a.download = `vm-events-${vmName}.${format}`;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  window.URL.revokeObjectURL(downloadUrl);
}

// Generic fetch helper for API calls
export async function fetchJSON<T = any>(path: string): Promise<T> {
  const url = path.startsWith('/api/proxy')
    ? path
    : `/api/proxy/plugin/vm-events-plugin/vm-events${path}`;

  const response = await fetch(url, {
    method: 'GET',
    headers: {
      'Content-Type': 'application/json',
    },
  });

  if (!response.ok) {
    throw new Error(`Failed to fetch: ${response.statusText}`);
  }

  return response.json();
}
