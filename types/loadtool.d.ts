// Type declarations for the LoadTool script API (Phase 1, ADR-005).
// Editor support only: LoadTool strips types and does not type-check.
//
// Reference this file from a script to get completion and type errors in
// your editor:
//   /// <reference path="path/to/types/loadtool.d.ts" />

declare module "loadtool/http" {
  export interface Response {
    /** HTTP status code, or 0 if no response was received. */
    status: number;
    /** Transport error message; empty string when the request completed. */
    error: string;
    timings: {
      /** Time from sending the request to reading the whole body, in milliseconds. */
      duration: number;
    };
  }

  export interface Params {
    headers?: Record<string, string>;
  }

  export function get(url: string, params?: Params): Response;
  export function request(method: string, url: string, body?: string | null, params?: Params): Response;

  const http: {
    get: typeof get;
    request: typeof request;
  };
  export default http;
}

declare module "loadtool" {
  const loadtool: Record<string, never>;
  export default loadtool;
}
