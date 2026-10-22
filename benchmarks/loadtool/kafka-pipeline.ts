// Kafka pipeline benchmark (benchmarks/protocol.ps1 with -LevelEnv RATE):
// producers send RATE messages per second (constant arrival rate) of
// VALUE_BYTES each, and three consumers in one group (one per partition)
// read them, for the end-to-end consume latency under a steady load.
// Producing starts 5 s after the consumers, so they have joined the group.
import kafka from "loadtool/kafka";

const BROKERS = (__ENV.KAFKA_BROKERS || "127.0.0.1:9092").split(",");
const RATE = Number(__ENV.RATE || 1000);
const SECS = Number(__ENV.DURATION_SEC || 30);
const VALUE = "x".repeat(Number(__ENV.VALUE_BYTES || 1024));
const VUS = Math.max(10, Math.ceil(RATE / 100));

const producer = new kafka.Producer({ brokers: BROKERS, topic: "orders" });
const consumer = new kafka.Consumer({ brokers: BROKERS, topic: "orders", group: "bench", startAt: "earliest" });

export const options = {
  scenarios: {
    consume: { executor: "constant-vus", vus: 3, duration: `${SECS + 8}s`, exec: "consume", gracefulStop: "1s" },
    produce: {
      executor: "constant-arrival-rate", rate: RATE, timeUnit: "1s", duration: `${SECS}s`, startTime: "5s",
      preAllocatedVUs: VUS, maxVUs: VUS, exec: "produce", gracefulStop: "5s",
    },
  },
};

export function produce() {
  producer.produce({ key: String(__ITER % 64), value: VALUE });
}

export function consume() {
  consumer.consume({ max: 500, timeout: "500ms" });
}
