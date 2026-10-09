package kafka

import (
	"context"
	"errors"
	"time"

	"github.com/dop251/goja"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

// consumer is one VU's consumer: a group member when a group is set,
// otherwise a reader of every partition of its topic. Its client is
// created on first use and closed with the VU, leaving the group first.
type consumer struct {
	inst   *instance
	cfg    config
	obj    *goja.Object
	client *kgo.Client
	closed bool
}

func (i *instance) newConsumer(cfg config) *consumer {
	c := &consumer{inst: i, cfg: cfg}
	i.vu.OnClose(c)
	return c
}

func (c *consumer) bind(o *goja.Object) {
	c.obj = o
	_ = o.Set("consume", c.consume)
	_ = o.Set("close", func() { _ = c.Close() })
	_ = o.Set("error", "")
	_ = o.Set("error_code", "")
}

// Close leaves the consumer group (bounded) and closes the client. It is
// idempotent.
func (c *consumer) Close() error {
	if c.client != nil && !c.closed {
		if c.cfg.group != "" {
			ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
			_ = c.client.LeaveGroupContext(ctx)
			cancel()
		}
		c.client.Close()
	}
	c.closed = true
	return nil
}

func (c *consumer) clientFor() (*kgo.Client, error) {
	if c.closed {
		return nil, kgo.ErrClientClosed
	}
	if c.client != nil {
		return c.client, nil
	}
	if c.cfg.topic == "" {
		return nil, errors.New("a consumer needs a topic")
	}
	start := kgo.NewOffset().AtEnd()
	if c.cfg.startAt == "earliest" {
		start = kgo.NewOffset().AtStart()
	}
	opts := append(c.cfg.clientOpts(c.inst.run.env),
		kgo.ConsumeTopics(c.cfg.topic),
		kgo.ConsumeResetOffset(start),
	)
	if c.cfg.group != "" {
		opts = append(opts, kgo.ConsumerGroup(c.cfg.group))
	}
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	c.client = cl
	return cl, nil
}

// consume returns up to max messages (default 1): at once when any are
// available, otherwise after waiting up to the timeout ([] then). A fetch
// error sets consumer.error and error_code and counts in
// kafka_consume_failed; nothing arriving is not an error.
func (c *consumer) consume(params goja.Value) goja.Value {
	i := c.inst
	i.requireIteration("consuming")
	rt := i.vu.Runtime()
	max, timeout := 1, c.cfg.timeout
	if isSet(params) {
		o := params.ToObject(rt)
		if m := o.Get("max"); isSet(m) {
			max = int(m.ToInteger())
			if max < 1 {
				panic(rt.NewTypeError("consume: max must be 1 or more"))
			}
		}
		if t := o.Get("timeout"); isSet(t) {
			timeout = parseTimeout(rt, t, "consume")
		}
	}
	vuCtx := i.vu.Context()
	ctx, cancel := context.WithTimeout(vuCtx, timeout)
	defer cancel()

	var failure error
	out := []any{}
	cl, err := c.clientFor()
	if err != nil {
		failure = err
	} else {
		fetches := cl.PollRecords(ctx, max)
		for _, fe := range fetches.Errors() {
			// Our own timeout, or the test ending, is not a failure.
			if errors.Is(fe.Err, context.DeadlineExceeded) || errors.Is(fe.Err, context.Canceled) {
				continue
			}
			failure = fe.Err
			break
		}
		now := time.Now()
		rec := i.recorderLive()
		fetches.EachRecord(func(r *kgo.Record) {
			if rec != nil {
				rec.Add(i.run.consumed, 1)
				if !r.Timestamp.IsZero() {
					rec.Trend(i.run.consumeLatency, max0(now.Sub(r.Timestamp)), true)
				}
			}
			out = append(out, c.message(r, now))
		})
	}
	code := errorCode(failure)
	if failure != nil && code == protocol.CodeNone {
		code = protocol.CodeProtocol
	}
	if rec := i.recorderLive(); rec != nil {
		rec.Rate(i.run.consumeFailed, failure != nil)
	}
	if c.obj != nil {
		msg := ""
		if failure != nil {
			msg = failure.Error()
		}
		_ = c.obj.Set("error", msg)
		_ = c.obj.Set("error_code", string(code))
	}
	return rt.ToValue(out)
}

// message converts a record for the script.
func (c *consumer) message(r *kgo.Record, now time.Time) map[string]any {
	headers := map[string]any{}
	for _, h := range r.Headers {
		headers[h.Key] = string(h.Value)
	}
	var key any
	if r.Key != nil {
		key = string(r.Key)
	}
	return map[string]any{
		"topic":     r.Topic,
		"partition": int64(r.Partition),
		"offset":    r.Offset,
		"key":       key,
		"value":     string(r.Value),
		"headers":   headers,
		"timestamp": r.Timestamp.UnixMilli(),
		"latency":   float64(max0(now.Sub(r.Timestamp))) / float64(time.Millisecond),
	}
}

func max0(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}
