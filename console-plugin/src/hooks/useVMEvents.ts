import { useQuery, UseQueryResult } from 'react-query';
import { fetchVMEvents, EventsResponse, QueryParams } from '../utils/api';

export interface UseVMEventsOptions extends QueryParams {
  namespace: string;
  vmName: string;
  refreshInterval?: number;
}

export function useVMEvents(
  options: UseVMEventsOptions
): UseQueryResult<EventsResponse, Error> {
  const { namespace, vmName, refreshInterval = 30000, ...queryParams } = options;

  return useQuery(
    ['vmEvents', namespace, vmName, queryParams],
    () => fetchVMEvents(namespace, vmName, queryParams),
    {
      refetchInterval: refreshInterval,
      staleTime: 10000, // Consider data stale after 10 seconds
    }
  );
}
