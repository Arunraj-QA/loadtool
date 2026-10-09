package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dop251/goja"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

// producer is one VU's producer. Its franz-go client is created on first
// use and closed with the VU (VU.OnClose).
type producer struct {
	inst   *instance
	cfg    config
	client *kgo.Client
	closed bool
}

func (i *instance) newProducer(cfg config) *producer {
	p := &producer{inst: i, cfg: cfg}
	i.vu.OnClose(p)
	return p
}

func (p *producer) bind(o *goja.Object) {
	_ = o.Set("produce", p.produce)
	_ = o.Set("produceBatch", p.produceBatch)
	_ = o.Set("close", func() { _ = p.Close() })
}

// Close closes the client. It is idempotent.
func (p *producer) Close() error {
	if p.client != nil && !p.closed {
		p.client.Close()
	}
	p.closed = true
	return nil
}

// clientFor returns the franz-go client, creating it on first use. Each
// call sends at once (no linger), so produce latency is the broker's
// acknowledgement time; delivery is bounded by the timeout.
func (p *producer) clientFor() (*kgo.Client, error) {
	if p.closed {
		return nil, kgo.ErrClientClosed
	}
	if p.client != nil {
		return p.client, nil
	}
	opts := append(p.cfg.clientOpts(p.inst.run.env),
		kgo.ProducerLinger(0),
		kgo.RecordDeliveryTimeout(p.cfg.timeout),
		kgo.MetadataMinAge(metadataMinAge(p.cfg.timeout)),
		kgo.RecordPartitioner(newPartitioner()),
	)
	if p.cfg.topic != "" {
		opts = append(opts, kgo.DefaultProduceTopic(p.cfg.topic))
	}
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	p.client = cl
	return cl, nil
}

// metadataMinAge is how often a producer may refresh metadata: a third of
// the timeout, so a record waiting on an unknown topic or a moved leader
// sees a refresh (and reports the broker's error rather than a bare
// timeout), and at most franz-go's default of 5 s, so the default 30 s
// timeout keeps the broker load of the default.
func metadataMinAge(timeout time.Duration) time.Duration {
	return min(max(timeout/3, 100*time.Millisecond), 5*time.Second)
}

var errNoValue = errors.New("a message needs a value")

// record builds a franz-go record from { topic, key, value, headers,
// partition }.
func (p *producer) record(ctx context.Context, v goja.Value) (*kgo.Record, error) {
	rt := p.inst.vu.Runtime()
	if !isSet(v) {
		return nil, errNoValue
	}
	o := v.ToObject(rt)
	r := &kgo.Record{Topic: p.cfg.topic, Context: ctx}
	if t := o.Get("topic"); isSet(t) {
		r.Topic = t.String()
	}
	if r.Topic == "" {
		return nil, errors.New("a message needs a topic, in the message or the producer")
	}
	val := o.Get("value")
	if !isSet(val) {
		return nil, errNoValue
	}
	r.Value, _ = bytesOf(val)
	if k := o.Get("key"); isSet(k) {
		r.Key, _ = bytesOf(k)
	}
	if h := o.Get("headers"); isSet(h) {
		ho := h.ToObject(rt)
		for _, name := range ho.Keys() {
			hv, _ := bytesOf(ho.Get(name))
			r.Headers = append(r.Headers, kgo.RecordHeader{Key: name, Value: hv})
		}
	}
	if pv := o.Get("partition"); isSet(pv) {
		n := pv.ToInteger()
		if n < 0 {
			return nil, fmt.Errorf("partition %d is negative", n)
		}
		r.Partition = int32(n)
		r.Context = withPartition(ctx)
	}
	return r, nil
}

// produce sends one message and waits for its acknowledgement.
func (p *producer) produce(msg goja.Value) goja.Value {
	results := p.send([]goja.Value{msg})
	return results[0]
}

// produceBatch sends several messages in one call; each gets a result.
func (p *producer) produceBatch(list goja.Value) goja.Value {
	rt := p.inst.vu.Runtime()
	var msgs []goja.Value
	if isSet(list) {
		arr := list.ToObject(rt)
		n := arr.Get("length").ToInteger()
		for k := int64(0); k < n; k++ {
			msgs = append(msgs, arr.Get(fmt.Sprint(k)))
		}
	}
	if len(msgs) == 0 {
		panic(rt.NewTypeError("producer.produceBatch: a non-empty list of messages is required"))
	}
	return rt.ToValue(p.send(msgs))
}

// send produces msgs and returns their results. Invalid messages are
// never sent; the rest go in one ProduceSync call.
func (p *producer) send(msgs []goja.Value) []goja.Value {
	i := p.inst
	i.requireIteration("producing")
	// franz-go's record timeout ends a late produce, with the broker's last
	// error as its cause; the context is only a backstop, a little later.
	ctx, cancel := context.WithTimeout(i.vu.Context(), p.cfg.timeout+time.Second)
	defer cancel()

	results := make([]goja.Value, len(msgs))
	var records []*kgo.Record
	var index []int // records[j] is msgs[index[j]]
	for k, m := range msgs {
		r, err := p.record(ctx, m)
		if err != nil {
			results[k] = p.result(nil, protocol.Outcome{Err: err}, 0)
			continue
		}
		records = append(records, r)
		index = append(index, k)
	}
	if len(records) == 0 {
		return results
	}
	cl, err := p.clientFor()
	if err != nil {
		for _, k := range index {
			results[k] = p.result(nil, protocol.Outcome{Err: err, Code: errorCode(err)}, 0)
		}
		return results
	}
	// ProduceSync returns results in the order they complete, not the
	// order the records were given: match them by record.
	pos := make(map[*kgo.Record]int, len(records))
	for j, r := range records {
		pos[r] = index[j]
	}
	start := time.Now()
	produced := cl.ProduceSync(ctx, records...)
	d := time.Since(start)
	for _, pr := range produced {
		results[pos[pr.Record]] = p.result(pr.Record, protocol.Outcome{Duration: d, Err: pr.Err, Code: errorCode(pr.Err), Sent: true}, d)
	}
	return results
}

// result records one message's outcome and builds its result object.
func (p *producer) result(r *kgo.Record, out protocol.Outcome, d time.Duration) goja.Value {
	i := p.inst
	rt := i.vu.Runtime()
	code := out.Code
	if code == protocol.CodeNone && out.Err != nil {
		code = protocol.CodeInvalid
		if out.Sent {
			code = protocol.Classify(out.Err)
		}
	}
	if rec := i.recorderLive(); rec != nil {
		failed := code != protocol.CodeNone
		if out.Sent {
			rec.Trend(i.run.produceDuration, d, !failed)
		}
		if !failed {
			rec.Add(i.run.produced, 1)
		}
		rec.Rate(i.run.produceFailed, failed)
	}
	o := rt.NewObject()
	msg := ""
	if out.Err != nil {
		msg = out.Err.Error()
	}
	_ = o.Set("ok", code == protocol.CodeNone)
	_ = o.Set("error", msg)
	_ = o.Set("error_code", string(code))
	topic, partition, offset := "", int64(-1), int64(-1)
	if r != nil {
		topic = r.Topic
		if code == protocol.CodeNone {
			partition, offset = int64(r.Partition), r.Offset
		}
	}
	_ = o.Set("topic", topic)
	_ = o.Set("partition", partition)
	_ = o.Set("offset", offset)
	timings := rt.NewObject()
	_ = timings.Set("duration", float64(d)/float64(time.Millisecond))
	_ = o.Set("timings", timings)
	return o
}
