/// <reference path="../types/loadtool.d.ts" />

import kafka from "loadtool/kafka";
import { check } from "loadtool";

// Kafka producer and consumer at once, with end-to-end latency: one
// scenario produces at a fixed rate, another consumes in a consumer group.
// kafka_consume_latency is the time from each message's production to its
// consumption.
//
// Start the demo API (in-process Kafka on 127.0.0.1:9092), then run:
//   go run ./examples/server
//   loadtool run examples/kafka-consumer.ts

const BROKERS = (__ENV.KAFKA_BROKERS || "127.0.0.1:9092").split(",");
const TOPIC = "events";

const producer = new kafka.Producer({ brokers: BROKERS, topic: TOPIC });
// Every consumer VU joins the same group, so the topic's partitions are
// shared between them, as between instances of an application.
const consumer = new kafka.Consumer({ brokers: BROKERS, topic: TOPIC, group: "loadtool-example", startAt: "latest" });

export const options = {
  scenarios: {
    produce: {
      executor: "constant-arrival-rate",
      rate: 100, timeUnit: "1s", duration: "10s",
      preAllocatedVUs: 5,
      exec: "produce",
    },
    consume: {
      executor: "constant-vus",
      vus: 2, duration: "12s", // a little longer, to drain the topic
      exec: "consume",
    },
  },
  thresholds: {
    kafka_produce_failed: ["rate==0"],
    kafka_consume_failed: ["rate==0"],
    kafka_messages_consumed: ["count>0"],
    kafka_consume_latency: ["p(95)<1000"],
  },
};

export function produce(): void {
  const res = producer.produce({ key: String(__ITER % 10), value: JSON.stringify({ at: Date.now() }) });
  check(res, { "produced": (r) => r.ok });
}

export function consume(): void {
  // Up to 50 messages at once when they are there; [] after 1 s if not.
  const messages = consumer.consume({ max: 50, timeout: "1s" });
  check(consumer, { "no consume error": (c) => c.error === "" });
  for (const m of messages) {
    check(m, { "message has a value": (x) => x.value.length > 0 });
  }
}
