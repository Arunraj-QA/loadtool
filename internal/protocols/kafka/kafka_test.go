package kafka_test

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/protocol/protocoltest"
	lkafka "github.com/Arunraj-QA/loadtool/internal/protocols/kafka"
	"github.com/Arunraj-QA/loadtool/internal/protocols/kafka/kafkatest"
)

// cluster starts an in-process cluster with the given topics and returns
// its broker list as a JavaScript array literal.
func cluster(t testing.TB, topics ...string) string {
	t.Helper()
	c, err := kafkatest.NewCluster(0, topics...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	b, _ := json.Marshal(c.ListenAddrs())
	return string(b)
}

func run(t *testing.T, h *protocoltest.Harness, brokers, src string) *goja.Object {
	t.Helper()
	if _, err := h.Run(strings.ReplaceAll(src, "BROKERS", brokers)); err != nil {
		t.Fatalf("script: %v", err)
	}
	rt := h.VU().Runtime()
	return rt.Get("res").ToObject(rt)
}

func waitGoroutines(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > n {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutines: %d, want at most %d\n%s", runtime.NumGoroutine(), n, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// consumeAll is a script helper: consume until n messages arrived (or
// 10 tries).
const consumeAll = `
function consumeAll(consumer, n) {
	var got = [];
	for (var tries = 0; got.length < n && tries < 10; tries++) {
		got = got.concat(consumer.consume({ max: n - got.length, timeout: "1s" }));
	}
	return got;
}
`

// Produce messages with keys, values and headers, and consume them in a
// group: every field arrives, and both sides are measured.
func TestProduceAndConsume(t *testing.T) {
	brokers := cluster(t, "orders")
	h := protocoltest.New(t, lkafka.Module{})
	res := run(t, h, brokers, consumeAll+`
var producer = new kafka.Producer({ brokers: BROKERS, topic: "orders", timeout: "5s" });
var consumer = new kafka.Consumer({ brokers: BROKERS, topic: "orders", group: "g1", startAt: "earliest" });
var results = [];
for (var i = 0; i < 5; i++) {
	results.push(producer.produce({ key: "k" + i, value: JSON.stringify({ n: i }), headers: { source: "loadtool", seq: String(i) } }));
}
var got = consumeAll(consumer, 5);
got.sort(function (a, b) { return JSON.parse(a.value).n - JSON.parse(b.value).n; });
var res = {
	ok: results.every(function (r) { return r.ok && r.error === "" && r.topic === "orders" && r.offset >= 0; }),
	count: got.length,
	first: got[0].key + "|" + got[0].value + "|" + got[0].headers.source + "|" + got[0].headers.seq + "|" + got[0].topic,
	latencyOK: got.every(function (m) { return m.latency >= 0 && m.latency < 10000 && m.timestamp > 0; }),
	consumerError: consumer.error,
};
producer.close(); consumer.close();`)
	if !res.Get("ok").ToBoolean() || res.Get("count").ToInteger() != 5 ||
		res.Get("first").String() != `k0|{"n":0}|loadtool|0|orders` || !res.Get("latencyOK").ToBoolean() ||
		res.Get("consumerError").String() != "" {
		t.Errorf("result = %v", res.Export())
	}
	if h.Family(lkafka.MetricMessagesProduced).Count != 5 || h.Family(lkafka.MetricProduceDuration).Count != 5 ||
		h.Family(lkafka.MetricProduceFailed).Trues != 0 {
		t.Error("produce metrics miscounted")
	}
	if h.Family(lkafka.MetricMessagesConsumed).Count != 5 || h.Family(lkafka.MetricConsumeLatency).Count != 5 {
		t.Errorf("consume metrics: %d consumed, %d latency samples", h.Family(lkafka.MetricMessagesConsumed).Count, h.Family(lkafka.MetricConsumeLatency).Count)
	}
}

// An explicit partition is honoured; the same key always lands on the
// same partition; a batch goes in one call with a result per message.
func TestPartitionsAndBatches(t *testing.T) {
	brokers := cluster(t, "events")
	h := protocoltest.New(t, lkafka.Module{})
	res := run(t, h, brokers, `
var producer = new kafka.Producer({ brokers: BROKERS, topic: "events" });
var explicit = producer.produce({ value: "x", partition: 2 });
var a = producer.produce({ key: "same", value: "1" }), b = producer.produce({ key: "same", value: "2" });
var batch = producer.produceBatch([{ value: "b0" }, { value: "b1" }, { value: "b2", partition: 1 }]);
// Results come back in the messages' order whatever order the broker
// acknowledges them in.
var spread = producer.produceBatch([{ value: "p0", partition: 0 }, { value: "p2", partition: 2 }, { value: "p1", partition: 1 }]);
var res = {
	explicit: explicit.partition,
	sameKey: a.partition === b.partition && b.offset === a.offset + 1,
	batchOK: batch.length === 3 && batch.every(function (r) { return r.ok; }),
	batchExplicit: batch[2].partition,
	spread: spread.map(function (r) { return r.partition; }).join(","),
};`)
	if res.Get("explicit").ToInteger() != 2 || !res.Get("sameKey").ToBoolean() || !res.Get("batchOK").ToBoolean() ||
		res.Get("batchExplicit").ToInteger() != 1 || res.Get("spread").String() != "0,2,1" {
		t.Errorf("result = %v", res.Export())
	}
	if h.Family(lkafka.MetricMessagesProduced).Count != 9 {
		t.Errorf("produced = %d, want 9", h.Family(lkafka.MetricMessagesProduced).Count)
	}
}

// The one-off kafka.produce and kafka.consume, as in the roadmap.
func TestOneOffCalls(t *testing.T) {
	brokers := cluster(t, "orders")
	h := protocoltest.New(t, lkafka.Module{})
	res := run(t, h, brokers, `
var r = kafka.produce({ brokers: BROKERS, topic: "orders", key: "123", value: JSON.stringify({ id: 123 }) });
var got = [];
for (var i = 0; i < 10 && got.length === 0; i++) {
	got = kafka.consume({ brokers: BROKERS, topic: "orders", group: "loadtool-test", startAt: "earliest", timeout: "1s" });
}
var res = { ok: r.ok, key: got.length ? got[0].key : null, value: got.length ? got[0].value : null };`)
	if !res.Get("ok").ToBoolean() || res.Get("key").String() != "123" || res.Get("value").String() != `{"id":123}` {
		t.Errorf("result = %v", res.Export())
	}
}

// Errors are reported in results, never thrown: an unknown topic (a
// broker error), an invalid partition, a message without a value or
// topic, and an unreachable broker.
func TestProduceErrors(t *testing.T) {
	brokers := cluster(t, "orders")
	h := protocoltest.New(t, lkafka.Module{})
	res := run(t, h, brokers, `
var producer = new kafka.Producer({ brokers: BROKERS, timeout: "3s" });
function r(x) { return [x.ok, x.error_code].join(","); }
var unknown = producer.produce({ topic: "no-such-topic", value: "x" });
// Again, as an iteration later, once the client knows the topic is
// missing: still the broker's error, not a bare timeout.
kafka.produce({ brokers: ["127.0.0.1:1"], topic: "orders", value: "x", timeout: "1s" });
var again = producer.produce({ topic: "no-such-topic", value: "x" });
var res = {
	unknown: r(unknown), unknownError: unknown.error, againError: again.error,
	badPartition: r(producer.produce({ topic: "orders", value: "x", partition: 7 })),
	noValue: r(producer.produce({ topic: "orders" })),
	noTopic: r(producer.produce({ value: "x" })),
	unreachable: r(kafka.produce({ brokers: ["127.0.0.1:1"], topic: "orders", value: "x", timeout: "1s" })),
};`)
	for k, want := range map[string]string{
		"unknown": "false,server", "noValue": "false,invalid", "noTopic": "false,invalid",
	} {
		if got := res.Get(k).String(); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	for _, k := range []string{"unknownError", "againError"} {
		if !strings.Contains(strings.ToUpper(res.Get(k).String()), "UNKNOWN_TOPIC") {
			t.Errorf("%s = %q, want the broker's UNKNOWN_TOPIC", k, res.Get(k))
		}
	}
	if got := res.Get("badPartition").String(); !strings.HasPrefix(got, "false,") {
		t.Errorf("badPartition = %q, want a failure", got)
	}
	if got := res.Get("unreachable").String(); got != "false,timeout" && got != "false,dial" {
		t.Errorf("unreachable = %q, want a dial or timeout failure", got)
	}
	// Seven failures; the two invalid messages have no duration.
	if f := h.Family(lkafka.MetricProduceFailed); f.Trues != 7 || h.Family(lkafka.MetricMessagesProduced).Count != 0 {
		t.Errorf("failed %d of %d, produced %d", f.Trues, f.Count, h.Family(lkafka.MetricMessagesProduced).Count)
	}
}

// Nothing to consume within the timeout is an empty result, not an error.
func TestConsumeTimeoutIsNotAnError(t *testing.T) {
	brokers := cluster(t, "quiet")
	h := protocoltest.New(t, lkafka.Module{})
	start := time.Now()
	res := run(t, h, brokers, `
var consumer = new kafka.Consumer({ brokers: BROKERS, topic: "quiet", timeout: 300 });
var got = consumer.consume();
var res = { n: got.length, error: consumer.error, code: consumer.error_code };`)
	if took := time.Since(start); took < 250*time.Millisecond || took > 5*time.Second {
		t.Errorf("consume took %v for a 300 ms timeout", took)
	}
	if res.Get("n").ToInteger() != 0 || res.Get("error").String() != "" || res.Get("code").String() != "" {
		t.Errorf("result = %v", res.Export())
	}
	if f := h.Family(lkafka.MetricConsumeFailed); f.Count != 1 || f.Trues != 0 {
		t.Errorf("consume failed = %d of %d", f.Trues, f.Count)
	}
}

func TestMisuse(t *testing.T) {
	h := protocoltest.New(t, lkafka.Module{})
	for src, want := range map[string]string{
		`new kafka.Producer()`:                                      "brokers is required",
		`new kafka.Producer({ brokers: [] })`:                       "brokers is required",
		`kafka.produce({ topic: "t", value: "v" })`:                 "brokers is required",
		`new kafka.Consumer({ brokers: ["x:1"], startAt: "now" })`:  "startAt must be",
		`new kafka.Producer({ brokers: ["x:1"] }).produceBatch([])`: "non-empty list",
	} {
		if _, err := h.Run(src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", src, err, want)
		}
	}
	h.VU().SetContext(nil) // top-level code: creating is allowed, producing is not
	if _, err := h.Run(`var p = new kafka.Producer({ brokers: ["127.0.0.1:1"], topic: "t" });`); err != nil {
		t.Fatalf("top-level Producer: %v", err)
	}
	if _, err := h.Run(`p.produce({ value: "x" })`); err == nil || !strings.Contains(err.Error(), "top-level code") {
		t.Errorf("top-level produce: %v", err)
	}
}

// When the test ends during consume, it returns promptly and records
// nothing; closing the VU stops every client goroutine.
func TestCancellationAndShutdown(t *testing.T) {
	brokers := cluster(t, "quiet")
	before := runtime.NumGoroutine()
	h := protocoltest.New(t, lkafka.Module{})
	if _, err := h.Run(strings.ReplaceAll(`
var producer = new kafka.Producer({ brokers: BROKERS, topic: "quiet" });
producer.produce({ value: "warm" });
var consumer = new kafka.Consumer({ brokers: BROKERS, topic: "quiet", group: "g", timeout: "60s" });`, "BROKERS", brokers)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.VU().SetContext(ctx)
	time.AfterFunc(150*time.Millisecond, cancel)
	start := time.Now()
	_, _ = h.Run(`consumer.consume()`)
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("consume took %v after cancellation", took)
	}
	if f := h.Family(lkafka.MetricConsumeFailed); f.Count != 0 {
		t.Errorf("a cancelled consume was recorded: %+v", f)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	waitGoroutines(t, before+20) // the in-process cluster's own goroutines stay until the test ends
}

// Many VUs, each with its own producer, produce at once while one VU
// consumes (run with -race); every message is consumed.
func TestConcurrentVUs(t *testing.T) {
	brokers := cluster(t, "load")
	const vus, each = 8, 25
	var wg sync.WaitGroup
	for v := range vus {
		wg.Go(func() {
			h := protocoltest.New(t, lkafka.Module{})
			_, err := h.Run(strings.ReplaceAll(`
var producer = new kafka.Producer({ brokers: BROKERS, topic: "load" });
for (var i = 0; i < 25; i++) {
	var r = producer.produce({ key: "vu`+string(rune('a'+v))+`", value: String(i) });
	if (!r.ok) throw new Error(r.error);
}`, "BROKERS", brokers))
			if err != nil {
				t.Error(err)
			}
			if err := h.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	h := protocoltest.New(t, lkafka.Module{})
	res := run(t, h, brokers, consumeAll+`
var consumer = new kafka.Consumer({ brokers: BROKERS, topic: "load", startAt: "earliest" });
var res = { n: consumeAll(consumer, 200).length };`)
	if n := res.Get("n").ToInteger(); n != vus*each {
		t.Errorf("consumed %d, want %d", n, vus*each)
	}
}

// BenchmarkProduce measures one produce call (one message, acknowledged)
// against the in-process cluster, per VU; parallel VUs each have their
// own producer.
func BenchmarkProduce(b *testing.B) {
	brokers := cluster(b, "bench")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		h := protocoltest.New(b, lkafka.Module{})
		rt := h.VU().Runtime()
		if _, err := h.Run(strings.ReplaceAll(`var producer = new kafka.Producer({ brokers: BROKERS, topic: "bench" });
function once() { var r = producer.produce({ key: "k", value: "0123456789abcdef" }); if (!r.ok) throw new Error(r.error); }`, "BROKERS", brokers)); err != nil {
			b.Error(err)
			return
		}
		once, _ := goja.AssertFunction(rt.Get("once"))
		for pb.Next() {
			if _, err := once(goja.Undefined()); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
