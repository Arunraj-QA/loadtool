// Kafka produce benchmark (benchmarks/kafka-produce.ps1, or protocol.ps1
// with VALUE_BYTES set): one message of VALUE_BYTES (default 100) per
// iteration from each VU's own producer, waiting for the acknowledgement
// (no linger), with no think time.
import kafka from "loadtool/kafka";

const BROKERS = (__ENV.KAFKA_BROKERS || "127.0.0.1:9092").split(",");
const producer = new kafka.Producer({ brokers: BROKERS, topic: "orders" });
const VALUE = "x".repeat(Number(__ENV.VALUE_BYTES || 100));

export default function () {
  producer.produce({ key: String(__ITER % 64), value: VALUE });
}
