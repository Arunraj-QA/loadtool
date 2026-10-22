package kafka

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

// Default timeouts (ADR-022): produce waits for acknowledgements; consume
// waits for messages on a topic that may be quiet.
const (
	defaultProduceTimeout = 30 * time.Second
	defaultConsumeTimeout = 2 * time.Second
	closeTimeout          = 5 * time.Second
)

func isSet(v goja.Value) bool {
	return v != nil && !goja.IsUndefined(v) && !goja.IsNull(v)
}

// config is the client part of a Producer, Consumer or one-off call.
type config struct {
	brokers []string
	topic   string
	group   string
	startAt string // "earliest" or "latest"
	tls     bool
	timeout time.Duration
}

// key identifies a one-off client: calls with the same configuration
// reuse one client in the VU.
func (c config) key() string {
	b := append([]string(nil), c.brokers...)
	sort.Strings(b)
	return fmt.Sprintf("%s|%s|%s|%s|%t", strings.Join(b, ","), c.topic, c.group, c.startAt, c.tls)
}

// readConfig reads { brokers, topic, group, startAt, tls, timeout }.
func readConfig(rt *goja.Runtime, v goja.Value, what string, defaultTimeout time.Duration) config {
	if !isSet(v) {
		panic(rt.NewTypeError("%s: a configuration with brokers is required", what))
	}
	o := v.ToObject(rt)
	c := config{startAt: "latest", timeout: defaultTimeout}
	if b := o.Get("brokers"); isSet(b) {
		if err := rt.ExportTo(b, &c.brokers); err != nil {
			panic(rt.NewTypeError("%s: brokers must be a list such as [\"127.0.0.1:9092\"]", what))
		}
	}
	if len(c.brokers) == 0 {
		panic(rt.NewTypeError("%s: brokers is required, such as [\"127.0.0.1:9092\"]", what))
	}
	if t := o.Get("topic"); isSet(t) {
		c.topic = t.String()
	}
	if g := o.Get("group"); isSet(g) {
		c.group = g.String()
	}
	if s := o.Get("startAt"); isSet(s) {
		c.startAt = s.String()
		if c.startAt != "earliest" && c.startAt != "latest" {
			panic(rt.NewTypeError("%s: startAt must be \"earliest\" or \"latest\"", what))
		}
	}
	if t := o.Get("tls"); isSet(t) {
		c.tls = t.ToBoolean()
	}
	if t := o.Get("timeout"); isSet(t) {
		c.timeout = parseTimeout(rt, t, what)
	}
	return c
}

// clientOpts are the options every client gets.
func (c config) clientOpts(env protocol.RunEnv) []kgo.Opt {
	opts := []kgo.Opt{kgo.SeedBrokers(c.brokers...), kgo.DialTimeout(c.timeout)}
	if c.tls {
		cfg := &tls.Config{}
		if env.TLS != nil {
			cfg = env.TLS.Clone()
		}
		opts = append(opts, kgo.DialTLSConfig(cfg))
	}
	return opts
}

// parseTimeout reads milliseconds or a duration such as "2s".
func parseTimeout(rt *goja.Runtime, v goja.Value, what string) time.Duration {
	if s, ok := v.Export().(string); ok {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			panic(rt.NewTypeError("%s: timeout %q must be a positive duration, such as \"2s\"", what, s))
		}
		return d
	}
	ms := v.ToFloat()
	if ms <= 0 || ms != ms {
		panic(rt.NewTypeError("%s: timeout must be a positive number of milliseconds or a duration such as \"2s\"", what))
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// errorCode maps a franz-go error to an error_code (ADR-022).
func errorCode(err error) protocol.ErrorCode {
	if err == nil {
		return protocol.CodeNone
	}
	var ke *kerr.Error
	switch {
	case errors.As(err, &ke):
		return protocol.CodeServer
	case errors.Is(err, kgo.ErrRecordTimeout), errors.Is(err, context.DeadlineExceeded):
		return protocol.CodeTimeout
	case errors.Is(err, kgo.ErrClientClosed):
		return protocol.CodeClosed
	}
	return protocol.Classify(err)
}

// bytesOf reads a key or value: a string or an ArrayBuffer.
func bytesOf(v goja.Value) ([]byte, bool) {
	if ab, ok := v.Export().(goja.ArrayBuffer); ok {
		return ab.Bytes(), true
	}
	return []byte(v.String()), true
}
