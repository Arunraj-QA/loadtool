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

declare module "loadtool/grpc" {
  export interface CallParams {
    /** Request metadata (headers). */
    metadata?: Record<string, string>;
    /** Deadline: milliseconds, or a duration such as "2s" (default 30 s). For a stream it bounds the whole stream. */
    timeout?: number | string;
  }

  export interface ConnectParams {
    /** Use h2c (gRPC without TLS). Default: TLS, certificates verified. */
    plaintext?: boolean;
    /** Describe the server's methods by server reflection instead of a .proto file. */
    reflect?: boolean;
    /** Milliseconds or a duration such as "5s" (default 30 s). */
    timeout?: number | string;
  }

  export interface Status {
    /** The gRPC status code: 0 is OK. */
    status: number;
    /** Its name, such as "OK" or "NotFound". */
    status_text: string;
    /** "" or the status message. */
    error: string;
    /** "" or a normalized category: "timeout", "dial", "closed", "server", "invalid", ... */
    error_code: string;
  }

  export interface Response extends Status {
    /** The reply as an object (null when the call failed). */
    message: any;
    headers: Record<string, string>;
    trailers: Record<string, string>;
    timings: { duration: number };
  }

  /** A stream in the blocking style; its status fields are final once it ended. */
  export interface Stream extends Status {
    closed: boolean;
    trailers?: Record<string, string>;
    timings?: { duration: number };
    /** Sends one message; false once the stream is over. */
    send(message: object): boolean;
    /** Ends the client side. */
    closeSend(): void;
    /** The next message, or null once the stream ended. */
    recv(): any | null;
    /** Cancels the stream if it is still open. */
    close(): void;
  }

  export class Client {
    constructor();
    /** Parses .proto files (relative to the script); allowed in top-level code. */
    load(importPaths: string[], ...files: string[]): void;
    /** Connects and waits until ready; the connection is kept across iterations. */
    connect(address: string, params?: ConnectParams): { error: string; error_code: string };
    /** One unary call: "package.Service/Method". */
    invoke(method: string, request: object, params?: CallParams): Response;
    /** Opens a stream for a streaming method (server, client or bidirectional). */
    stream(method: string, params?: CallParams): Stream;
    close(): void;
  }

  const grpc: { Client: typeof Client };
  export default grpc;
}

declare module "loadtool/graphql" {
  export interface Params {
    /** The operation's variables. */
    variables?: Record<string, any>;
    /** Request headers (a Client's default headers are merged under them). */
    headers?: Record<string, string>;
    /** Which operation of the document to run. */
    operationName?: string;
    /** Milliseconds, or a duration such as "2s" (default: the HTTP timeout, 30 s). */
    timeout?: number | string;
  }

  export interface Result {
    /** "query" or "mutation". */
    kind: string;
    /** HTTP status; 0 without a response. */
    status: number;
    proto: string;
    headers: Record<string, string>;
    /** Transport success: an HTTP 2xx response. */
    http_ok: boolean;
    /** GraphQL success: http_ok, a JSON body, and no GraphQL errors. */
    ok: boolean;
    /** The response's data (null when absent). */
    data: any;
    /** The response's GraphQL errors; [] when none. */
    errors: Array<{ message: string; path?: (string | number)[]; locations?: { line: number; column: number }[]; extensions?: any }>;
    /** "" or why it is not ok: the transport error, the HTTP status, or the first GraphQL error. */
    error: string;
    /** "" or "server" (HTTP error or GraphQL errors), "protocol" (not GraphQL JSON), "dial", "timeout", ..., "invalid". */
    error_code: string;
    /** The raw response body. */
    body: string | null;
    timings: { duration: number };
  }

  export function query(url: string, document: string, params?: Params): Result;
  export function mutation(url: string, document: string, params?: Params): Result;

  /** An endpoint with default headers. */
  export class Client {
    constructor(url: string, params?: { headers?: Record<string, string> });
    readonly url: string;
    query(document: string, params?: Params): Result;
    mutation(document: string, params?: Params): Result;
  }

  const graphql: { query: typeof query; mutation: typeof mutation; Client: typeof Client };
  export default graphql;
}


declare module "loadtool/kafka" {
  /** Where and how to connect. Clients are per VU and connect on first use. */
  export interface Config {
    /** Seed brokers, "host:port". Required. */
    brokers: string[];
    /** The default topic (a Consumer's only topic). */
    topic?: string;
    /** TLS with the run's TLS settings (default false). */
    tls?: boolean;
    /** Milliseconds, or a duration such as "2s": the delivery timeout for a
     * producer (default 30 s), the poll timeout for a consumer (default 2 s). */
    timeout?: number | string;
  }

  export interface ConsumerConfig extends Config {
    topic: string;
    /** A consumer group to join; without one, the VU reads every partition. */
    group?: string;
    /** Where to start without a committed offset (default "latest"). */
    startAt?: "earliest" | "latest";
  }

  export interface Message {
    /** Overrides the producer's topic. */
    topic?: string;
    /** Messages with the same key go to the same partition. */
    key?: string | ArrayBuffer;
    /** Required. */
    value: string | ArrayBuffer;
    headers?: Record<string, string | ArrayBuffer>;
    /** A partition to send to, instead of the one the key chooses. */
    partition?: number;
  }

  /** The outcome of one message; errors are reported here, never thrown. */
  export interface ProduceResult {
    /** The broker acknowledged the message. */
    ok: boolean;
    /** "" or why it was not acknowledged. */
    error: string;
    /** "" or "server" (a broker error), "timeout", "dial", "closed", ..., "invalid" (never sent). */
    error_code: string;
    topic: string;
    /** -1 when not acknowledged. */
    partition: number;
    /** -1 when not acknowledged. */
    offset: number;
    /** Milliseconds from sending to the acknowledgement. */
    timings: { duration: number };
  }

  export interface ConsumedMessage {
    topic: string;
    partition: number;
    offset: number;
    /** null when the message has no key. */
    key: string | null;
    value: string;
    headers: Record<string, string>;
    /** When the message was produced, in Unix milliseconds. */
    timestamp: number;
    /** Milliseconds from production to consumption (kafka_consume_latency). */
    latency: number;
  }

  export interface ConsumeParams {
    /** Most messages to return (default 1). */
    max?: number;
    /** How long to wait for one; [] if none arrives (default: the consumer's timeout). */
    timeout?: number | string;
  }

  export class Producer {
    constructor(config: Config);
    produce(message: Message): ProduceResult;
    /** Sends the messages together; one result each, in the same order. */
    produceBatch(messages: Message[]): ProduceResult[];
    /** Closes the client now; it is closed at the end of the test otherwise. */
    close(): void;
  }

  export class Consumer {
    constructor(config: ConsumerConfig);
    consume(params?: ConsumeParams): ConsumedMessage[];
    /** "" or why the last consume failed. */
    readonly error: string;
    readonly error_code: string;
    /** Leaves the group and closes the client; otherwise at the end of the test. */
    close(): void;
  }

  /** One message, with a client kept per configuration in the VU. */
  export function produce(message: Config & Message): ProduceResult;
  /** Messages, with a client kept per configuration in the VU. */
  export function consume(params: ConsumerConfig & ConsumeParams): ConsumedMessage[];

  const kafka: { Producer: typeof Producer; Consumer: typeof Consumer; produce: typeof produce; consume: typeof consume };
  export default kafka;
}
