// Package kafka is the Kafka protocol module, imported by scripts as
// "loadtool/kafka" (ADR-022). It keeps Kafka's own semantics, producers
// and consumers, acknowledgements and polls, consumer groups, on top of
// franz-go:
//
//	const producer = new kafka.Producer({ brokers: ["127.0.0.1:9092"], topic: "orders" });
//	producer.produce({ key: "123", value: JSON.stringify(order) });
//	const consumer = new kafka.Consumer({ brokers: ["127.0.0.1:9092"], topic: "orders", group: "loadtool-test" });
//	const messages = consumer.consume({ max: 10, timeout: "2s" });
//
// Each VU owns its clients: created on first use, reused across
// iterations, closed at the end of the run.
package kafka

import (
	"context"

	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

// Metric family names (ADR-015, ADR-022).
const (
	MetricProduceDuration  = "kafka_produce_duration"
	MetricMessagesProduced = "kafka_messages_produced"
	MetricProduceFailed    = "kafka_produce_failed"
	MetricConsumeLatency   = "kafka_consume_latency"
	MetricMessagesConsumed = "kafka_messages_consumed"
	MetricConsumeFailed    = "kafka_consume_failed"
)

// Module is the Kafka protocol module.
type Module struct{}

var _ protocol.Module = Module{}

func (Module) Name() string      { return "kafka" }
func (Module) Exports() []string { return []string{"Producer", "Consumer", "produce", "consume"} }

func (Module) Metrics() []metrics.Def {
	return []metrics.Def{
		{Name: MetricProduceDuration, Kind: metrics.Trend},
		{Name: MetricMessagesProduced, Kind: metrics.Counter},
		{Name: MetricProduceFailed, Kind: metrics.Rate},
		{Name: MetricConsumeLatency, Kind: metrics.Trend},
		{Name: MetricMessagesConsumed, Kind: metrics.Counter},
		{Name: MetricConsumeFailed, Kind: metrics.Rate},
	}
}

// run holds the family IDs and the environment; clients are per VU.
type run struct {
	produced, produceFailed, consumeFailed metrics.FamilyID
	produceDuration, consumeLatency        metrics.FamilyID
	consumed                               metrics.FamilyID
	env                                    protocol.RunEnv
}

func (Module) NewRun(env protocol.RunEnv) (protocol.Run, error) {
	ids, err := protocol.FamilyIDs(env, MetricProduceDuration, MetricMessagesProduced, MetricProduceFailed,
		MetricConsumeLatency, MetricMessagesConsumed, MetricConsumeFailed)
	if err != nil {
		return nil, err
	}
	return &run{
		produceDuration: ids[0], produced: ids[1], produceFailed: ids[2],
		consumeLatency: ids[3], consumed: ids[4], consumeFailed: ids[5],
		env: env,
	}, nil
}

func (r *run) NewInstance(vu protocol.VU) (protocol.Instance, error) {
	return &instance{vu: vu, run: r, producers: map[string]*producer{}, consumers: map[string]*consumer{}}, nil
}

func (r *run) Close(context.Context) error { return nil }

// instance is the module in one VU. The one-off kafka.produce and
// kafka.consume reuse a client per configuration; every client is
// registered with VU.OnClose.
type instance struct {
	vu        protocol.VU
	run       *run
	obj       *goja.Object
	producers map[string]*producer
	consumers map[string]*consumer
}

func (i *instance) Value() goja.Value {
	if i.obj != nil {
		return i.obj
	}
	rt := i.vu.Runtime()
	i.obj = rt.NewObject()
	_ = i.obj.Set("Producer", func(call goja.ConstructorCall) *goja.Object {
		p := i.newProducer(readConfig(rt, call.Argument(0), "new kafka.Producer", defaultProduceTimeout))
		p.bind(call.This)
		return nil
	})
	_ = i.obj.Set("Consumer", func(call goja.ConstructorCall) *goja.Object {
		c := i.newConsumer(readConfig(rt, call.Argument(0), "new kafka.Consumer", defaultConsumeTimeout))
		c.bind(call.This)
		return nil
	})
	// kafka.produce({ brokers, topic, key, value, headers, partition, timeout })
	_ = i.obj.Set("produce", func(msg goja.Value) goja.Value {
		cfg := readConfig(rt, msg, "kafka.produce", defaultProduceTimeout)
		p, ok := i.producers[cfg.key()]
		if !ok {
			p = i.newProducer(cfg)
			i.producers[cfg.key()] = p
		}
		return p.produce(msg)
	})
	// kafka.consume({ brokers, topic, group, startAt, max, timeout })
	_ = i.obj.Set("consume", func(params goja.Value) goja.Value {
		cfg := readConfig(rt, params, "kafka.consume", defaultConsumeTimeout)
		c, ok := i.consumers[cfg.key()]
		if !ok {
			c = i.newConsumer(cfg)
			i.consumers[cfg.key()] = c
		}
		return c.consume(params)
	})
	return i.obj
}

func (i *instance) BeginIteration()             {}
func (i *instance) EndIteration()               {}
func (i *instance) Close(context.Context) error { return nil } // clients close through VU.OnClose

// requireIteration throws in top-level code, where network calls are not
// allowed.
func (i *instance) requireIteration(what string) {
	if i.vu.Context() == nil {
		panic(i.vu.Runtime().NewGoError(errInit(what)))
	}
}

type errInit string

func (e errInit) Error() string {
	return string(e) + " is not allowed in the script's top-level code; call it inside the default function"
}

// recorderLive returns the VU's recorder, or nil when nothing may be
// recorded (top-level code, or the test is ending).
func (i *instance) recorderLive() *metrics.Recorder {
	ctx := i.vu.Context()
	if ctx == nil || ctx.Err() != nil {
		return nil
	}
	return i.vu.Recorder()
}
