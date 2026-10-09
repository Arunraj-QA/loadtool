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
    /** "HTTP/1.1" or "HTTP/2.0"; empty if no response was received. */
    proto: string;
    /** Transport error message; empty string when the request completed. */
    error: string;
    /** Response headers by canonical name; repeated headers are joined with ", ". */
    headers: Record<string, string>;
    /**
     * The body, or null when it was discarded or no response arrived.
     * Bodies are discarded by default: set options.discardResponseBodies
     * to false, or params.responseType to "text".
     */
    body: string | null;
    /** Parse the body as JSON. Throws SyntaxError on invalid JSON, TypeError if the body was discarded. */
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
    /**
     * "text" keeps this response's body, "none" discards it; unset follows
     * options.discardResponseBodies (which discards by default).
     */
    responseType?: "text" | "none";
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

declare module "loadtool/ws" {
  /** The result of a WebSocket session, returned by connect once the socket closed. */
  export interface Result {
    /** The handshake's HTTP status: 101 when the connection was upgraded; 0 without a response. */
    status: number;
    /** "" or why the session failed. */
    error: string;
    /** "" or a normalized category: "dns", "dial", "tls", "timeout", "protocol", "server", "closed" or "invalid". */
    error_code: string;
    url: string;
    timings: {
      /** The handshake, in milliseconds. */
      connecting: number;
      /** From the end of the handshake to the end of the session, in milliseconds. */
      duration: number;
    };
  }

  export interface SendOptions {
    /** Time this send until the next message received (ws_msg_latency); replies are matched in order. */
    reply?: boolean;
  }

  export interface ErrorEvent {
    error: string;
    error_code: string;
  }

  export interface Socket {
    on(event: "open", handler: () => void): void;
    on(event: "message", handler: (data: string | ArrayBuffer) => void): void;
    on(event: "close", handler: (code: number) => void): void;
    on(event: "error", handler: (e: ErrorEvent) => void): void;
    /** Sends a text message; returns false if it could not be sent. */
    send(data: string, options?: SendOptions): boolean;
    /** Sends a binary message; returns false if it could not be sent. */
    sendBinary(data: ArrayBuffer, options?: SendOptions): boolean;
    /** Starts the close handshake (default code 1000). */
    close(code?: number): void;
    /** Runs fn once, after ms milliseconds, inside the session. */
    setTimeout(fn: () => void, ms: number): number;
    /** Runs fn every ms milliseconds, inside the session. */
    setInterval(fn: () => void, ms: number): number;
  }

  export interface Params {
    /** Headers for the handshake request. The VU's cookies are sent too. */
    headers?: Record<string, string>;
  }

  /**
   * A socket in the blocking style: returned by connect without a setup
   * function. Its fields describe the session and are updated as it goes.
   */
  export interface BlockingSocket extends Result {
    /** True once the socket is closed (or never opened). */
    closed: boolean;
    /**
     * Sends a text message; returns false if it could not be sent. Every
     * send is timed until a later receive returns a message (ws_msg_latency),
     * unless { reply: false } is given.
     */
    send(data: string, options?: SendOptions): boolean;
    sendBinary(data: ArrayBuffer, options?: SendOptions): boolean;
    /**
     * Waits for the next message (default 30 000 ms). Returns null on a
     * timeout (error_code "timeout"; the socket stays open), when the socket
     * closes, or when the test ends.
     */
    receive(timeoutMs?: number): string | ArrayBuffer | null;
    /** Closes the socket and waits until it is closed. */
    close(code?: number): void;
  }

  /** Opens a WebSocket session and blocks until it is closed (the callback style). */
  export function connect(url: string, params: Params, setup: (socket: Socket) => void): Result;
  export function connect(url: string, setup: (socket: Socket) => void): Result;
  /**
   * Opens a socket and returns it (the blocking style). A socket left open
   * is closed when the iteration ends.
   */
  export function connect(url: string, params?: Params): BlockingSocket;

  const ws: { connect: typeof connect };
  export default ws;
}

/** "loadtool/websocket" is another name for "loadtool/ws". */
declare module "loadtool/websocket" {
  export * from "loadtool/ws";
  import ws from "loadtool/ws";
  export default ws;
}
