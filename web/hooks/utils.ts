import { useState, useCallback, useMemo, useEffect } from "react";
import { PaginatedResponse, Chat, Message, User } from "../types";

export function shouldUpdateState<T extends Chat | Message | User>(
  currentValue: T[],
  newValue: T[]
): boolean {
  // Check if the length of arrays is different
  if (currentValue?.length !== newValue?.length) {
    return true;
  }

  // Compare each item in the arrays
  for (let i = 0; i < currentValue?.length ?? 0; i++) {
    const currentItem = currentValue[i];
    const newItem = newValue[i];

    // Compare the id property
    if (currentItem?.id !== newItem?.id) {
      return true;
    }

    // Additional comparisons can be added here if needed
    // For example, comparing timestamps or specific fields that
    // are likely to change and are important for your application's logic
  }

  // If all checks pass, the state does not need to be updated
  return false;
}

export function usePaginatedFetch<T extends Chat | Message | User>(
  token: string | null,
  fetchFunctionFactory: (
    config?: any
  ) => (page: number, perPage: number) => Promise<PaginatedResponse<T> | null>,
  sort?: (a: T, b: T) => number
) {
  const [data, setData] = useState<T[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!token) {
      setData([]);
      setLoading(false);
      setError(null);
    }
  }, [token]);

  const fetchPaginatedData = useCallback(
    async (config?: {
      maxPages?: number;
      perPage?: number;
      [key: string]: any;
    }) => {
      const { maxPages = 10, perPage = 20, ...otherConfig } = config || {};
      setLoading(true);
      setError(null);
      let allData: T[] = [];
      let currentPage = 1;
      const fetchFunction = fetchFunctionFactory(otherConfig);

      try {
        while (currentPage <= maxPages) {
          const responseData = await fetchFunction(currentPage, perPage);
          if (!responseData) {
            break;
          }

          allData = allData.concat(responseData.items);
          if (responseData.page === responseData.pages) {
            break; // Stop if it's the last page
          }

          currentPage++;
        }
        if (shouldUpdateState(data, allData)) {
          await setData(allData);
        }
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [fetchFunctionFactory, data]
  );

  const sortedData = useMemo(() => {
    const dat = [...(data || [])];
    if (sort) {
      dat.sort(sort);
    }
    return dat;
  }, [data, sort]);

  return { data: sortedData, loading, error, fetchPaginatedData };
}
