/// <reference path="../types/loadtool.d.ts" />

import kafka from "loadtool/kafka";
import { check } from "loadtool";

// Kafka throughput: each iteration sends a batch of 100 messages in one
// call (they go out together), and a threshold checks the message rate.
//
// Start the demo API (in-process Kafka on 127.0.0.1:9092), then run:
//   go run ./examples/server
//   loadtool run examples/kafka-throughput.ts --vus 5 --duration 10s
//
// kafka_messages_produced's rate (per second) is the produce throughput.

const BROKERS = (__ENV.KAFKA_BROKERS || "127.0.0.1:9092").split(",");
const BATCH = 100;
const producer = new kafka.Producer({ brokers: BROKERS, topic: "events" });

// 1 KiB of payload per message.
const PAYLOAD = "x".repeat(1024);

export const options = {
  thresholds: {
    kafka_messages_produced: ["rate>1000"], // more than 1,000 messages per second
    kafka_produce_failed: ["rate==0"],
  },
};

export default function (): void {
  const batch = [];
  for (let i = 0; i < BATCH; i++) {
    batch.push({ key: `${__VU}-${i}`, value: PAYLOAD });
  }
  const results = producer.produceBatch(batch);
  check(results, { "whole batch acknowledged": (rs) => rs.every((r) => r.ok) });
}
