// Type declarations for the LoadTool script API (Phase 1, ADR-005, ADR-008).
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
    /** Response headers by canonical name; repeated headers are joined with ", ". */
    headers: Record<string, string>;
    /** The body, or null when bodies are discarded or no response arrived. */
    body: string | null;
    /** Parse the body as JSON. Throws SyntaxError on invalid JSON. */
    json(): any;
    timings: {
      /** Time from sending the request to reading the whole body, in milliseconds. */
      duration: number;
    };
    /** The request URL. */
    url: string;
    /** Cookies this response set, by name. */
    cookies: Record<string, ResponseCookie[]>;
  }

  export interface ResponseCookie {
    name: string;
    value: string;
    domain: string;
    path: string;
    /** Milliseconds since the epoch; 0 when the cookie has no Expires. */
    expires: number;
    max_age: number;
    http_only: boolean;
    secure: boolean;
  }

  export interface Params {
    headers?: Record<string, string>;
    /** Cookies to send with this request, in addition to the VU's jar. */
    cookies?: Record<string, string>;
  }

  /** Request body: a string. Use JSON.stringify(...) to send JSON. */
  export type Body = string | null;

  export function get(url: string, params?: Params): Response;
  export function post(url: string, body?: Body, params?: Params): Response;
  export function put(url: string, body?: Body, params?: Params): Response;
  export function patch(url: string, body?: Body, params?: Params): Response;
  export function del(url: string, body?: Body, params?: Params): Response;
  export function request(method: string, url: string, body?: Body, params?: Params): Response;

  const http: {
    get: typeof get;
    post: typeof post;
    put: typeof put;
    patch: typeof patch;
    del: typeof del;
    request: typeof request;
  };
  export default http;
}

declare module "loadtool" {
  /**
   * Pause this VU for the given number of seconds (fractions allowed).
   * Returns early when the run ends. Not allowed in top-level code.
   */
  export function sleep(seconds: number): void;
  /** Run fn and return its result. */
  export function group<T>(name: string, fn: () => T): T;
  /**
   * Run each condition on value and count a pass or fail per name.
   * Returns true if every condition passed. A condition that throws counts
   * as failed; it does not end the iteration.
   */
  export function check<T>(value: T, conditions: Record<string, (value: T) => unknown>): boolean;

  const loadtool: {
    sleep: typeof sleep;
    group: typeof group;
    check: typeof check;
  };
  export default loadtool;
}

/**
 * The process environment plus --env KEY=VALUE flags. Writes and deletes
 * affect only the current VU.
 */
declare var __ENV: Record<string, string | undefined>;
/** The VU number: 1..N, or 0 while LoadTool reads the script's options. */
declare const __VU: number;
/** This VU's iteration number, starting at 0. */
declare const __ITER: number;
