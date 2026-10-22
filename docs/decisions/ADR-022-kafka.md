# ADR-022: Kafka module

- Status: Accepted (2026-10-09, Phase 2)
- Date: 2026-10-09
- Builds on: ADR-014 to ADR-018 (protocol modules), ADR-015 (metrics),
  ADR-016 (errors), ADR-017 (blocking calls)

## Context

The roadmap's Kafka goal, as the user set it on 2026-10-09:

- **Messaging:** a producer and a consumer, topics, message keys and
  values, headers, and partition selection where practical.
- **Measures:** produce latency, consume latency, message throughput and
  error reporting.
- **Behaviour:** VU-owned client state, connection reuse, cancellation,
  clean shutdown, and configurable brokers, topic, consumer group and
  timeout.
- **Integration:** checks, thresholds and the JSON and HTML reports.

**Kafka is not request/response.** A producer appends records and gets
acknowledgements; a consumer polls a log, usually as a member of a
consumer group. The module keeps those semantics; it does not wrap them
in the HTTP model.

**The previous scope said one client per run.** Phase 2 scope decision 4
(CLAUDE.md, 2026-10-08) said "one client per run, shared by the VUs". The
user's requirements now say **VU-owned client and session state**, so the
decision is updated (below).

**Out of scope:**

- distributed workers;
- Kafka Streams;
- administration tooling (creating or deleting topics, ACLs);
- transactions.

## Decision: the client library

| Option | Licence | Assessment |
|---|---|---|
| **`github.com/twmb/franz-go`** | BSD-3-Clause | **Chosen.** Pure Go, so release builds stay static and cgo-free. It covers the whole protocol (producer, consumer groups, idempotence, headers, compression) and is actively maintained. Every call takes a `context.Context`, which cancellation needs. Its in-process fake cluster, `pkg/kfake`, runs the tests and examples without Docker. |
| `github.com/IBM/sarama` | MIT | Mature and widely used, but its API is channel- and callback-based, harder to bound per call. It has no in-process test cluster, and franz-go is faster in its maintainers' and users' published comparisons (not re-measured here). |
| `github.com/segmentio/kafka-go` | MIT | Simple, but development has slowed, its consumer-group support is thinner, and it has no fake cluster. |
| `github.com/confluentinc/confluent-kafka-go` | Apache-2.0 | Wraps librdkafka through cgo, which breaks the static, cross-compiled release builds (ADR-003, `scripts/release-build.sh`). |
| A hand-written wire protocol | — | Excluded by the request. |

**Dependencies franz-go brings:**

- `klauspost/compress` (BSD-3) and `pierrec/lz4` (BSD-3), for
  compression codecs;
- `kmsg`, its protocol messages.

All are permissive. They are imported only by `internal/protocols/kafka`
and the test environment (ADR-018 §1).

## Decision: the module (`loadtool/kafka`)

### API

```ts
import kafka from "loadtool/kafka";

const BROKERS = (__ENV.KAFKA_BROKERS || "127.0.0.1:9092").split(",");

// Classes: one producer or consumer per VU, kept across iterations.
const producer = new kafka.Producer({ brokers: BROKERS, topic: "orders", timeout: "5s" });
const consumer = new kafka.Consumer({ brokers: BROKERS, topic: "orders", group: "loadtool-test", timeout: "5s" });

export default function () {
  const r = producer.produce({ key: "123", value: JSON.stringify({ id: 123 }), headers: { source: "loadtool" } });
  // r: { ok, error, error_code, topic, partition, offset, timings: { duration } }

  const batch = producer.produceBatch([{ value: "a" }, { value: "b" }]);   // one round trip, a result per message

  const msgs = consumer.consume({ max: 10, timeout: "2s" });
  // [{ topic, partition, offset, key, value, headers, timestamp, latency }]; [] when nothing arrives in time
}

// One-off calls (a per-VU client is kept, keyed by the configuration):
kafka.produce({ brokers: BROKERS, topic: "orders", key: "123", value: "..." });
kafka.consume({ brokers: BROKERS, topic: "orders", group: "loadtool-test" });
```

| Setting | Where | Default |
|---|---|---|
| `brokers` | Producer, Consumer, one-off calls | required |
| `topic` | Producer (default topic), message (overrides it), Consumer | required somewhere |
| `group` | Consumer | none: without a group, the consumer reads every partition itself |
| `startAt` | Consumer: `"earliest"` or `"latest"` | `"latest"` |
| `timeout` | Producer, Consumer, each call | 30 s for produce, 2 s for consume |
| `key`, `value` | message | key optional; value required (a string or an `ArrayBuffer`) |
| `headers` | message (`{ name: "value" }`) | none |
| `partition` | message | none: chosen from the key (murmur2, as the Java client does), or spread when there is no key |

**`consume` returns at once** with up to `max` messages (default 1) when
any are available. Otherwise it waits up to its timeout and returns `[]`.

**An empty result is not an error.** No messages within the timeout is a
normal outcome on a quiet topic; it is not recorded as a failure.

### Ownership and lifecycle (ADR-018 §6; scope decision 4 updated)

**Each VU owns its clients.** Every `new kafka.Producer()`,
`new kafka.Consumer()` or one-off client is a franz-go client belonging
to that VU:

- it is created on first use, without network activity in top-level
  code;
- it is reused across iterations, so its broker connections are reused;
- it is registered with `VU.OnClose`.

**At the end of the run,** a consumer leaves its group, then every client
closes, each step bounded by a timeout. Producers have already been
flushed, because `produce` waits for acknowledgements. Nothing is shared
between VUs, and the run holds only the family IDs.

**Scope decision 4 changes** from "one client per run" to "one client
per VU per configuration", as the user asked:

- memory per VU is higher, which the benchmark measures;
- consumer groups behave as real applications do, each VU being a group
  member.

**Many VUs in one group trigger rebalances** as they join, as many
application instances do. Tests that want stable consumption use a few
consumer VUs.

### Blocking calls and cancellation (ADR-017)

**Calls block, bounded by the VU's context and the call's timeout:**

- `produce` and `produceBatch` use `ProduceSync`;
- `consume` uses `PollRecords`.

**When the test ends,** calls return at once and nothing is recorded.
**No goroutine is started by the module;** franz-go's own goroutines
belong to the client and stop when it closes.

**Producers send each call at once** (`ProducerLinger(0)`), so produce
latency is the acknowledgement time, not time spent waiting to batch.
Records still go out together in `produceBatch`.

**Delivery is bounded:** the record delivery timeout is the call's
timeout, so a failing broker cannot block a VU forever.

### Latency and throughput

| Metric | Kind | Meaning |
|---|---|---|
| `kafka_produce_duration` | Trend | From `produce` to the broker's acknowledgement, per message. In a batch, every message gets the batch's time. |
| `kafka_messages_produced` | Counter | Messages acknowledged. Its rate is the produce throughput. |
| `kafka_produce_failed` | Rate | Messages that were not acknowledged |
| `kafka_consume_latency` | Trend | End to end: the message's timestamp, set by its producer, to the moment `consume` received it |
| `kafka_messages_consumed` | Counter | Messages consumed. Its rate is the consume throughput. |
| `kafka_consume_failed` | Rate | `consume` calls that failed (a fetch error), out of all calls |

**End-to-end latency compares two clocks:** the producer's and the
consumer's. It is exact when both run in one LoadTool process (the usual
test). Across machines, it includes their clock skew. Record timestamps
set by the broker (`LogAppendTime`) measure from the append instead.

### Errors (ADR-016)

**Produce results** carry `error` and `error_code`; **consume** returns
the messages it got and sets `consumer.error` and `consumer.error_code`:

| Failure | `error_code` |
|---|---|
| Broker unreachable, DNS, TLS | `dial`, `dns`, `tls` (classified) |
| Not acknowledged within the timeout | `timeout` |
| A broker error (unknown topic, not leader after retries, too large a message, …) | `server`, with the broker's error name in `error` |
| The client was closed | `closed` |
| A message without a value, or with no topic anywhere | `invalid` (never sent) |

**Misuse throws a `TypeError`:**

- network calls in top-level code (creating a `Producer` or `Consumer`
  there is allowed);
- a missing `brokers` list.

### Test environments

**`kfake`, franz-go's in-process cluster,** is used for:

- **unit, integration, race and concurrency tests:** fast, deterministic
  and run on every CI build;
- **the demo API,** which serves one on `127.0.0.1:9092`
  (`-kafka-addr`), so the Kafka examples run locally like the others.

**A real broker, through Docker Compose** (`testenv/kafka/docker-compose.yml`):

- Apache Kafka in KRaft mode, a single node.
- A CI job starts it, and runs the module's real-broker tests, which skip
  unless `LOADTOOL_KAFKA_BROKERS` is set.
- It checks behaviour against a real broker that `kfake` might not copy
  exactly.

## Consequences

- **Kafka's semantics stay visible in the API:** producers and consumers,
  acknowledgements and polls, groups. The HTTP abstraction is not
  stretched to fit.
- **Per-VU clients cost memory.** The benchmark measures it at several VU
  counts.
- **Not supported yet:**
  - SASL authentication;
  - TLS client certificates (server TLS uses the system's roots);
  - transactions;
  - exactly-once;
  - topic administration;
  - Kafka Streams;
  - schema registries;
  - manual offset commits (offsets are committed automatically).
