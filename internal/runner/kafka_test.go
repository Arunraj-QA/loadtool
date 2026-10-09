package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/protocols/kafka/kafkatest"
	"github.com/Arunraj-QA/loadtool/internal/report"
)

// One script mixes HTTP and Kafka: each VU owns a producer and a consumer
// (created in top-level code, connected on first use), produces and
// consumes every iteration, and the runner closes every client after the
// run. Kafka thresholds and the reports see the kafka_* families.
func TestRunMixesHTTPAndKafka(t *testing.T) {
	httpSrv := okServer(t)
	cluster, err := kafkatest.NewCluster(0, "orders")
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()
	before := runtime.NumGoroutine()
	brokers, _ := json.Marshal(cluster.ListenAddrs())
	path := scriptFile(t, `import http from "loadtool/http";
import kafka from "loadtool/kafka";
import { check } from "loadtool";

const producer = new kafka.Producer({ brokers: `+string(brokers)+`, topic: "orders" });
const consumer = new kafka.Consumer({ brokers: `+string(brokers)+`, topic: "orders", group: "vu" + __VU, startAt: "earliest" });

export const options = {
	thresholds: {
		kafka_produce_failed: ["rate==0"],
		kafka_messages_produced: ["count>0"],
		kafka_produce_duration: ["p(95)<1000"],
		http_req_failed: ["rate==0"],
	},
};

export default function () {
	http.get("`+httpSrv.URL+`");
	const r = producer.produce({ key: "vu" + __VU, value: JSON.stringify({ vu: __VU, iter: __ITER }), headers: { source: "loadtool" } });
	check(r, { "produced": (x) => x.ok && x.offset >= 0 });
	consumer.consume({ max: 10, timeout: 200 });
}`)
	res, err := Run(context.Background(), Params{
		Config:    config.Config{Script: path, GracefulStop: time.Second},
		Overrides: config.Overrides{VUs: intp(3), Duration: durp(500 * time.Millisecond)},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summary
	if s.ScriptErrors != 0 {
		t.Fatalf("script error: %s", s.FirstScriptError)
	}
	produced, _ := s.Family("kafka_messages_produced")
	consumed, _ := s.Family("kafka_messages_consumed")
	if produced.Count == 0 || produced.Count != s.Requests || consumed.Count == 0 {
		t.Errorf("%d produced, %d consumed, %d HTTP requests; want one produce per request and some consumed", produced.Count, consumed.Count, s.Requests)
	}
	for _, th := range res.Thresholds {
		if !th.Passed {
			t.Errorf("threshold %s %s failed", th.Metric, th.Expr)
		}
	}
	var b bytes.Buffer
	if err := report.JSON(&b, res, "test"); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Metrics map[string]json.RawMessage `json:"metrics"`
	}
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kafka_produce_duration", "kafka_messages_produced", "kafka_produce_failed", "kafka_consume_latency", "kafka_messages_consumed", "kafka_consume_failed"} {
		if _, ok := doc.Metrics[name]; !ok {
			t.Errorf("JSON summary misses %s", name)
		}
	}
	b.Reset()
	if err := report.HTML(&b, res, "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "<td><code>kafka_consume_latency</code></td>") {
		t.Error("HTML report misses kafka_consume_latency")
	}
	// Every client (3 VUs x 2, plus none in setup) was closed by the runner.
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > before+4 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+4 {
		t.Errorf("goroutines: %d after the run, %d before", n, before)
	}
}
