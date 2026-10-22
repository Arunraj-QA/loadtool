# Kafka (`loadtool/kafka`)

[Script API](../script-api.md) · Protocols: [HTTP](http.md) · [WebSocket](websocket.md) · [gRPC](grpc.md) · [GraphQL](graphql.md) · [Kafka](kafka.md)

Kafka producers and consumers, on the franz-go client (ADR-022)
([`examples/kafka-producer.ts`](../../examples/kafka-producer.ts),
[`kafka-consumer.ts`](../../examples/kafka-consumer.ts),
[`kafka-throughput.ts`](../../examples/kafka-throughput.ts),
[`kafka-errors.ts`](../../examples/kafka-errors.ts)):

```typescript
import kafka from "loadtool/kafka";
import { check } from "loadtool";

const producer = new kafka.Producer({ brokers: ["127.0.0.1:9092"], topic: "orders" });

export default function () {
  const res = producer.produce({ key: "order-1", value: JSON.stringify({ id: 1 }), headers: { source: "loadtool" } });
  check(res, { "acknowledged": (r) => r.ok });
}
```

Kafka is not request/response, so there are no `http`-like responses:
a producer sends messages and waits for the broker's acknowledgement, and
a consumer polls for messages.

| Call | Does |
|---|---|
| `new kafka.Producer({ brokers, topic?, tls?, timeout? })` | A producer; `timeout` bounds each delivery (default 30 s) |
| `producer.produce(message)` | Sends one message and waits for its acknowledgement; returns a result |
| `producer.produceBatch([message, ...])` | Sends the messages together; one result each, in order |
| `new kafka.Consumer({ brokers, topic, group?, startAt?, tls?, timeout? })` | A consumer of one topic; with `group`, it joins that consumer group |
| `consumer.consume({ max?, timeout? })` | Up to `max` messages (default 1); `[]` if none arrives within `timeout` (default 2 s) |
| `producer.close()`, `consumer.close()` | Close now (a consumer leaves its group); otherwise at the end of the test |
| `kafka.produce({ brokers, topic, ...message })` | A one-off produce, with a producer kept per configuration in the VU |
| `kafka.consume({ brokers, topic, group?, ..., max?, timeout? })` | A one-off consume, likewise |

A message is `{ value, key?, headers?, topic?, partition? }`. `value` is
required; `key`, `value` and header values are strings or `ArrayBuffer`s.
Messages with the same key go to the same partition; `partition` chooses
one explicitly. `topic` overrides the producer's.

**Clients are per VU.** A Producer or Consumer created in top-level code is
created in every VU, each with its own client, which connects on first use
and is reused across iterations. Calls that use the network are made in
iterations (or `setup`/`teardown`), not in top-level code. Consumers in the
same `group` share the topic's partitions, as an application's instances
would; without a group, each consumer reads every partition. `startAt`
(`"latest"`, the default, or `"earliest"`) is where a consumer starts
without a committed offset.

**Results never throw.** A produce result is
`{ ok, error, error_code, topic, partition, offset, timings: { duration } }`;
`partition` and `offset` are -1 when the message was not acknowledged.
A failed consume returns what arrived and sets `consumer.error` and
`consumer.error_code` (both `""` after a consume that worked); a consume
that times out with nothing to read is not an error.

| Failure | `error_code` |
|---|---|
| A broker error (unknown topic, not leader, message too large, ...) | `server` |
| Not acknowledged within the timeout | `timeout` (or `server` with the broker's last error) |
| Unreachable broker | `dial` or `timeout` |
| TLS handshake failure | `tls` |
| The producer or consumer was closed | `closed` |
| No value, no topic, a negative partition (never sent) | `invalid` |

A consumed message is
`{ topic, partition, offset, key, value, headers, timestamp, latency }`:
`key` is `null` without one, `timestamp` is when it was produced (Unix
milliseconds), and `latency` is the milliseconds from production to
consumption.

**Metrics:**

| Metric | Kind | Meaning |
|---|---|---|
| `kafka_produce_duration` | trend | From sending a message to its acknowledgement |
| `kafka_messages_produced` | counter | Acknowledged messages (its rate is the produce throughput) |
| `kafka_produce_failed` | rate | Messages that were not acknowledged (invalid ones included) |
| `kafka_consume_latency` | trend | End to end: from a message's production (its timestamp) to its consumption |
| `kafka_messages_consumed` | counter | Consumed messages |
| `kafka_consume_failed` | rate | Consumes that failed |

`kafka_consume_latency` compares the producer's clock with the consumer's,
so it is exact only when they are the same machine (as with LoadTool
producing and consuming) or their clocks are synchronized; messages
produced before the test began show how long they waited.

**Not supported:** Kafka Streams, transactions and exactly-once
production, administration (creating topics), manual offset commits
(groups commit automatically), and SASL authentication.

## Examples

Runnable against the demo API ([how](../../examples/README.md)):

| Example | Shows |
|---|---|
| [`kafka-producer.ts`](../../examples/kafka-producer.ts) | A producer per VU |
| [`kafka-consumer.ts`](../../examples/kafka-consumer.ts) | Producing and consuming, with a consumer group |
| [`kafka-throughput.ts`](../../examples/kafka-throughput.ts) | Batches; a threshold on messages per second |
| [`kafka-errors.ts`](../../examples/kafka-errors.ts) | How errors are reported |
