// Type declarations for the LoadTool Phase 0 script API.
// Editor support only: LoadTool strips types and does not type-check.

interface LoadToolResponse {
  /** HTTP status code, or 0 if no response was received. */
  status: number;
  /** Transport error message; empty string when the request completed. */
  error: string;
  timings: {
    /** Time from sending the request to reading the whole body, in milliseconds. */
    duration: number;
  };
}

interface LoadToolParams {
  headers?: Record<string, string>;
}

declare const http: {
  get(url: string, params?: LoadToolParams): LoadToolResponse;
  request(method: string, url: string, body?: string | null, params?: LoadToolParams): LoadToolResponse;
};
