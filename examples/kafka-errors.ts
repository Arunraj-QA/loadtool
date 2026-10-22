/// <reference path="../types/loadtool.d.ts" />

import kafka from "loadtool/kafka";
import { check } from "loadtool";

// Kafka errors are reported in results, never thrown: a broker error, an
// invalid message, and a broker that does not answer. This also shows the
// one-off kafka.produce, which keeps a client per configuration in the VU.
//
// Start the demo API (in-process Kafka on 127.0.0.1:9092), then run:
//   go run ./examples/server
//   loadtool run examples/kafka-errors.ts --vus 1 --duration 5s
//
// All but the first produce fail on purpose, so kafka_produce_failed is
// high; the checks assert that each failure is reported correctly.

const BROKERS = (__ENV.KAFKA_BROKERS || "127.0.0.1:9092").split(",");

export const options = {
  thresholds: { checks: ["rate==1"] },
};

export default function (): void {
  // For comparison, one that works.
  const good = kafka.produce({ brokers: BROKERS, topic: "orders", value: "x", timeout: "3s" });
  check(good, { "valid message: acknowledged": (r) => r.ok && r.error === "" && r.error_code === "" });

  // A topic that does not exist (auto-creation is off): a broker error.
  // Later iterations wait up to the timeout for the topic to appear.
  const unknown = kafka.produce({ brokers: BROKERS, topic: "no-such-topic", value: "x", timeout: "3s" });
  check(unknown, {
    "unknown topic: not acknowledged": (r) => !r.ok,
    "unknown topic: a broker error": (r) => r.error_code === "server" && r.error.includes("UNKNOWN_TOPIC"),
  });

  // A message without a value is never sent.
  const empty = kafka.produce({ brokers: BROKERS, topic: "orders" });
  check(empty, { "no value: invalid": (r) => !r.ok && r.error_code === "invalid" });

  // Nothing listens on port 1: the produce times out (or fails to dial).
  const down = kafka.produce({ brokers: ["127.0.0.1:1"], topic: "orders", value: "x", timeout: "1s" });
  check(down, { "unreachable: reported": (r) => !r.ok && (r.error_code === "timeout" || r.error_code === "dial") });
}
