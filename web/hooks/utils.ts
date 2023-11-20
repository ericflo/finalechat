import { useState, useCallback, useMemo } from "react";
import { PaginatedResponse } from "../types";

// Utility function to perform deep comparison of two objects
export function deepEqual<T>(obj1: T, obj2: T): boolean {
  if (obj1 === obj2) {
    return true;
  }

  // Ensuring both objects are of the same type
  if (typeof obj1 !== typeof obj2) {
    return false;
  }

  // Check for the primitive types
  if ((obj1 !== Object(obj1) || obj2 !== Object(obj2)) && obj1 !== obj2) {
    return false;
  }

  const keys1 = Object.keys(obj1 as any);
  const keys2 = Object.keys(obj2 as any);

  if (keys1.length !== keys2.length) {
    return false;
  }

  for (const key of keys1) {
    if (!keys2.includes(key)) {
      return false;
    }

    // Recursively check for nested objects
    if (!deepEqual((obj1 as any)[key], (obj2 as any)[key])) {
      return false;
    }
  }

  return true;
}

// Updated shouldUpdateState function to use deep comparison
export function shouldUpdateState<T>(currentValue: T, newValue: T): boolean {
  return !deepEqual(currentValue, newValue);
}

export function usePaginatedFetch<T>(
  fetchFunctionFactory: (
    config?: any
  ) => (page: number, perPage: number) => Promise<PaginatedResponse<T> | null>,
  sort?: (a: T, b: T) => number
) {
  const [data, setData] = useState<T[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

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
