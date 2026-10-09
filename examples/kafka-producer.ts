/// <reference path="../types/loadtool.d.ts" />

import kafka from "loadtool/kafka";
import { check } from "loadtool";

// Kafka producer: each VU has its own producer, kept across iterations,
// and waits for every message's acknowledgement.
//
// Start the demo API (it runs an in-process Kafka on 127.0.0.1:9092), or
// a real broker (testenv/kafka/up.sh), then run:
//   go run ./examples/server
//   loadtool run examples/kafka-producer.ts --vus 10 --duration 10s

const BROKERS = (__ENV.KAFKA_BROKERS || "127.0.0.1:9092").split(",");

// Created in top-level code (no network yet); connects on first use.
const producer = new kafka.Producer({ brokers: BROKERS, topic: "orders", timeout: "5s" });

export const options = {
  thresholds: {
    kafka_produce_failed: ["rate<0.01"],    // fewer than 1% not acknowledged
    kafka_produce_duration: ["p(95)<100"],  // acknowledged within 100 ms
    checks: ["rate>0.99"],
  },
};

export default function (): void {
  const orderId = `${__VU}-${__ITER}`;
  const res = producer.produce({
    key: orderId, // the same key always goes to the same partition
    value: JSON.stringify({ id: orderId, product: 1 + (__ITER % 5), quantity: 1 }),
    headers: { source: "loadtool", "content-type": "application/json" },
  });
  check(res, {
    "acknowledged": (r) => r.ok,
    "has an offset": (r) => r.offset >= 0 && r.partition >= 0,
  });
}
