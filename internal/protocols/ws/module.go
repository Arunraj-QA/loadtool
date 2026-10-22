// Package ws is the WebSocket protocol module, imported by scripts as
// "loadtool/ws" (ADR-019):
//
//	const res = ws.connect(url, params, (socket) => {
//	  socket.on("open", () => socket.send("hi", { reply: true }));
//	  socket.on("message", (data) => socket.close());
//	});
//
// connect blocks until the socket closes. Each call is one session: its
// connection, reader goroutine, handlers and timers belong to that call
// and are released before it returns, so VUs share no WebSocket state.
package ws

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

// Metric family names (ADR-015, ADR-019).
const (
	// MetricConnecting is the handshake time of every connection attempt
	// that reached the server; failed handshakes are failed samples.
	MetricConnecting = "ws_connecting"
	// MetricSessions counts connection attempts.
	MetricSessions = "ws_sessions"
	// MetricSessionFailed is true for a session whose handshake failed or
	// that ended abnormally (an error, or a close code other than 1000
	// or 1001).
	MetricSessionFailed = "ws_session_failed"
	// MetricSessionDuration is the time from the end of the handshake to
	// the end of the session.
	MetricSessionDuration = "ws_session_duration"
	// MetricMsgsSent and MetricMsgsReceived count data messages.
	MetricMsgsSent     = "ws_msgs_sent"
	MetricMsgsReceived = "ws_msgs_received"
	// MetricMsgLatency is the time from a send marked { reply: true } to
	// the next message received.
	MetricMsgLatency = "ws_msg_latency"
	// MetricErrors counts send and receive errors during sessions.
	MetricErrors = "ws_errors"
)

const (
	// connectTimeout bounds the handshake, as httpclient.DefaultTimeout
	// bounds an HTTP request.
	connectTimeout = 30 * time.Second
	// writeTimeout bounds one send; a peer that stops reading must not
	// block the VU forever.
	writeTimeout = 30 * time.Second
	// readLimit is the largest message accepted; a larger one is a
	// receive error. Each VU holds at most one message at a time.
	readLimit = 1 << 20
	// maxPendingReplies bounds the sends awaiting a reply in a session.
	maxPendingReplies = 1024
)

// Module is the WebSocket protocol module.
type Module struct{}

var _ protocol.Module = Module{}

func (Module) Name() string      { return "ws" }
func (Module) Aliases() []string { return []string{"websocket"} }
func (Module) Exports() []string { return []string{"connect"} }

func (Module) Metrics() []metrics.Def {
	return []metrics.Def{
		{Name: MetricConnecting, Kind: metrics.Trend},
		{Name: MetricSessions, Kind: metrics.Counter},
		{Name: MetricSessionFailed, Kind: metrics.Rate},
		{Name: MetricSessionDuration, Kind: metrics.Trend},
		{Name: MetricMsgsSent, Kind: metrics.Counter},
		{Name: MetricMsgsReceived, Kind: metrics.Counter},
		{Name: MetricMsgLatency, Kind: metrics.Trend},
		{Name: MetricErrors, Kind: metrics.Counter},
	}
}

// familyIDs are the module's family IDs in a run.
type familyIDs struct {
	connecting, sessions, sessionFailed, sessionDuration metrics.FamilyID
	msgsSent, msgsReceived, msgLatency, errors           metrics.FamilyID
}

// run is the module's state for one test run: the IDs and an HTTP/1.1
// transport for handshakes, shared by every VU.
type run struct {
	ids       familyIDs
	transport *http.Transport
	warn      func(string)
}

func (Module) NewRun(env protocol.RunEnv) (protocol.Run, error) {
	ids, err := protocol.FamilyIDs(env, MetricConnecting, MetricSessions, MetricSessionFailed, MetricSessionDuration,
		MetricMsgsSent, MetricMsgsReceived, MetricMsgLatency, MetricErrors)
	if err != nil {
		return nil, err
	}
	// WebSocket handshakes are HTTP/1.1 upgrades, whatever
	// options.httpVersion says; the transport is safe for concurrent use.
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Protocols = new(http.Protocols)
	t.Protocols.SetHTTP1(true)
	t.DisableCompression = true
	if env.TLS != nil {
		t.TLSClientConfig = env.TLS.Clone()
	}
	warn := env.Warn
	if warn == nil {
		warn = func(string) {}
	}
	return &run{
		ids:       familyIDs{ids[0], ids[1], ids[2], ids[3], ids[4], ids[5], ids[6], ids[7]},
		transport: t, warn: warn,
	}, nil
}

func (r *run) NewInstance(vu protocol.VU) (protocol.Instance, error) {
	return &instance{vu: vu, run: r}, nil
}

func (r *run) Close(context.Context) error {
	r.transport.CloseIdleConnections()
	return nil
}

// instance is the module in one VU. Callback-style sessions are scoped
// to their connect call; open holds the blocking-style sockets the
// current iteration opened and has not closed yet.
type instance struct {
	vu   protocol.VU
	run  *run
	obj  *goja.Object
	open []*blockingSocket
}

func (i *instance) Value() goja.Value {
	if i.obj == nil {
		rt := i.vu.Runtime()
		i.obj = rt.NewObject()
		_ = i.obj.Set("connect", i.connect)
	}
	return i.obj
}

func (i *instance) BeginIteration() {}

// EndIteration closes the blocking-style sockets the iteration left open,
// with a normal close (or at once, if the test is ending).
func (i *instance) EndIteration() {
	for _, b := range i.open {
		if !b.finished {
			i.vu.Warn("ws: a socket was still open when the iteration ended; it was closed for you (call socket.close())")
			break
		}
	}
	i.closeOpen()
}

// Close closes any socket still open; normally EndIteration already has.
func (i *instance) Close(context.Context) error {
	i.closeOpen()
	return nil
}

func (i *instance) closeOpen() {
	for _, b := range i.open {
		b.close(websocket.StatusNormalClosure)
	}
	i.open = nil
}
