package kafka_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Arunraj-QA/loadtool/internal/protocol/protocoltest"
	lkafka "github.com/Arunraj-QA/loadtool/internal/protocols/kafka"
)

// realBrokers returns the brokers of a real Kafka (testenv/kafka/up.sh),
// or skips the test when LOADTOOL_KAFKA_BROKERS is not set. CI sets it in
// its kafka job.
func realBrokers(t *testing.T) string {
	t.Helper()
	env := os.Getenv("LOADTOOL_KAFKA_BROKERS")
	if env == "" {
		t.Skip("set LOADTOOL_KAFKA_BROKERS (and run testenv/kafka/up.sh) to test against a real broker")
	}
	b, _ := json.Marshal(strings.Split(env, ","))
	return string(b)
}

// Against a real broker: produce with keys, headers and an explicit
// partition, consume in a group, and get a broker error for an unknown
// topic (auto-creation is off in testenv).
func TestRealBroker(t *testing.T) {
	brokers := realBrokers(t)
	h := protocoltest.New(t, lkafka.Module{})
	group := "loadtool-real-" + strings.ReplaceAll(t.Name(), "/", "-")
	res := run(t, h, brokers, consumeAll+`
var producer = new kafka.Producer({ brokers: BROKERS, topic: "loadtool-real", timeout: "10s" });
var marker = "run-" + Date.now();
var sent = [];
for (var i = 0; i < 20; i++) {
	sent.push(producer.produce({ key: marker, value: marker + ":" + i, headers: { n: String(i) } }));
}
var explicit = producer.produce({ value: marker + ":explicit", partition: 2 });
var unknown = producer.produce({ topic: "loadtool-no-such-topic", value: "x" });

var consumer = new kafka.Consumer({ brokers: BROKERS, topic: "loadtool-real", group: "`+group+`", startAt: "earliest", timeout: "5s" });
var mine = [];
for (var tries = 0; mine.length < 21 && tries < 20; tries++) {
	consumer.consume({ max: 100, timeout: "2s" }).forEach(function (m) {
		if (m.value.indexOf(marker + ":") === 0) mine.push(m);
	});
}
var res = {
	sentOK: sent.every(function (r) { return r.ok; }),
	samePartition: sent.every(function (r) { return r.partition === sent[0].partition; }),
	explicit: explicit.partition,
	unknown: [unknown.ok, unknown.error_code].join(","),
	consumed: mine.length,
	header: mine.filter(function (m) { return m.value === marker + ":3"; }).map(function (m) { return m.headers.n; }).join(""),
};
producer.close(); consumer.close();`)
	if !res.Get("sentOK").ToBoolean() || !res.Get("samePartition").ToBoolean() || res.Get("explicit").ToInteger() != 2 {
		t.Errorf("produce: %v", res.Export())
	}
	if res.Get("unknown").String() != "false,server" {
		t.Errorf("unknown topic = %q, want a server error", res.Get("unknown"))
	}
	if res.Get("consumed").ToInteger() != 21 || res.Get("header").String() != "3" {
		t.Errorf("consume: %v", res.Export())
	}
	if h.Family(lkafka.MetricConsumeLatency).Count == 0 {
		t.Error("no consume latency recorded")
	}
}
