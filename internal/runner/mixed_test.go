package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/protocols/graphql/graphqltest"
	"github.com/Arunraj-QA/loadtool/internal/protocols/kafka/kafkatest"
	"github.com/Arunraj-QA/loadtool/internal/report"
)

// The Phase 2 exit test (criterion 1): examples/mixed-protocols.ts, the
// example CI's smoke test also runs, uses HTTP, GraphQL, WebSocket, gRPC
// and Kafka in each iteration. Here it runs against in-process servers.

// mixedTargets are local servers for every protocol the example uses.
type mixedTargets struct {
	env     map[string]string
	brokers []string
	// queries counts GraphQL requests; onQuery, if set, runs on each, and
	// when it returns true the request is never answered.
	queries atomic.Int64
	onQuery func(n int64) bool
}

// newMixedTargets starts an HTTP server with the demo API's routes the
// example uses (login, me, the WebSocket echo, GraphQL), a gRPC greeter
// and a Kafka cluster with the topic "events".
func newMixedTargets(t *testing.T) *mixedTargets {
	t.Helper()
	m := &mixedTargets{}
	shop, err := graphqltest.Handler(0)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok"}`)) })
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Username, Password string }
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Password != "demo" {
			http.Error(w, "wrong username or password", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"username": in.Username, "token": "token-" + in.Username})
	})
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		user, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer token-")
		if !ok {
			http.Error(w, "not logged in", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"username": user})
	})
	mux.HandleFunc("GET /ws/echo", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if c.Write(r.Context(), typ, data) != nil {
				return
			}
		}
	})
	mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		if m.onQuery != nil && m.onQuery(m.queries.Add(1)) {
			// Until the client goes away, which the server notices only
			// once the body has been read.
			io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
			return
		}
		shop.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cluster, err := kafkatest.NewCluster(0, "events")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cluster.Close)
	m.brokers = cluster.ListenAddrs()
	m.env = map[string]string{
		"BASE_URL":      srv.URL,
		"GRPC_ADDR":     grpcServer(t),
		"KAFKA_BROKERS": strings.Join(m.brokers, ","),
	}
	return m
}

func mixedExample(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "examples", "mixed-protocols.ts"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// The protocols' operation counters; each runs once per iteration.
var mixedCounters = []string{"graphql_reqs", "ws_sessions", "grpc_reqs", "kafka_messages_produced"}

// The protocols' failure rates.
var mixedFailures = []string{"http_req_failed", "graphql_req_failed", "ws_session_failed", "grpc_req_failed", "kafka_produce_failed"}

func TestMixedProtocolsEndToEnd(t *testing.T) {
	targets := newMixedTargets(t)
	before := runtime.NumGoroutine()
	var console bytes.Buffer
	const vus = 4
	res, err := Run(context.Background(), Params{
		Config:    config.Config{Script: mixedExample(t), GracefulStop: 5 * time.Second},
		Overrides: config.Overrides{VUs: intp(vus), Duration: durp(time.Second)},
		Env:       targets.env,
		Console:   &console,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summary
	if s.ScriptErrors != 0 {
		t.Fatalf("script error: %s", s.FirstScriptError)
	}
	if res.Interrupted || s.Iterations == 0 {
		t.Fatalf("Interrupted=%v, %d iterations; want a complete run", res.Interrupted, s.Iterations)
	}

	// Shared lifecycle: setup's data reached every iteration (the GraphQL
	// check reads it), and teardown ran after the load phase.
	if !strings.Contains(console.String(), "mixed-protocols: done (5 products)") {
		t.Errorf("teardown did not run; console: %q", console.String())
	}

	// Shared checks: every protocol's check ran in every iteration and
	// passed. Each compares with something only its VU sent, so state
	// leaking between VUs would fail it.
	wantChecks := []string{
		"http: logged in as this VU", "graphql: product found",
		"websocket: echoed this VU's message", "grpc: greeted this VU", "kafka: event acknowledged",
	}
	byName := map[string]metrics.CheckResult{}
	for _, c := range s.Checks {
		byName[c.Name] = c
	}
	for _, name := range wantChecks {
		c := byName[name]
		if c.Passes != s.Iterations || c.Fails != 0 {
			t.Errorf("check %q: %d passed, %d failed; want %d passed", name, c.Passes, c.Fails, s.Iterations)
		}
	}

	// Shared metrics model: one operation per protocol per iteration, each
	// counted under its own family; HTTP counts the two HTTP requests
	// only (GraphQL is not HTTP's, scope decision 1; setup's request is
	// not part of the load phase).
	if s.Requests != 2*s.Iterations {
		t.Errorf("%d HTTP requests for %d iterations; want 2 each", s.Requests, s.Iterations)
	}
	for _, name := range mixedCounters {
		if f, ok := s.Family(name); !ok || f.Count != s.Iterations {
			t.Errorf("%s = %d; want %d, one per iteration", name, f.Count, s.Iterations)
		}
	}
	for _, name := range mixedFailures[1:] {
		if f, _ := s.Family(name); f.Trues != 0 {
			t.Errorf("%s: %d failures", name, f.Trues)
		}
	}

	// Shared thresholds: all nine, across five protocols, passed.
	if len(res.Thresholds) != 9 {
		t.Errorf("%d thresholds; want 9", len(res.Thresholds))
	}
	for _, th := range res.Thresholds {
		if !th.Passed {
			t.Errorf("threshold %s %s failed", th.Metric, th.Expr)
		}
	}

	// Shared JSON and HTML reports carry every protocol.
	var b bytes.Buffer
	if err := report.JSON(&b, res, "test"); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Metrics map[string]json.RawMessage `json:"metrics"`
		Checks  []json.RawMessage          `json:"checks"`
	}
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, name := range append(append([]string{"http_reqs", "ws_msg_latency", "grpc_req_duration", "graphql_req_duration", "kafka_produce_duration"}, mixedCounters...), mixedFailures...) {
		if _, ok := doc.Metrics[name]; !ok {
			t.Errorf("JSON summary misses %s", name)
		}
	}
	if len(doc.Checks) != len(wantChecks) {
		t.Errorf("JSON summary has %d checks; want %d", len(doc.Checks), len(wantChecks))
	}
	b.Reset()
	if err := report.HTML(&b, res, "test"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ws_msg_latency", "grpc_req_duration", "graphql_req_duration", "kafka_produce_duration"} {
		if !strings.Contains(b.String(), "<td><code>"+name+"</code></td>") {
			t.Errorf("HTML report misses %s", name)
		}
	}
	if !strings.Contains(b.String(), "websocket: echoed this VU") {
		t.Error("HTML report misses the checks")
	}

	// No cross-VU leakage at the broker either: every event was produced
	// by the VU its key names, and all VUs produced.
	users := kafkaEventUsers(t, targets.brokers, s.Iterations)
	if len(users) != vus {
		t.Errorf("events from %d VUs; want %d", len(users), vus)
	}

	// Clean shutdown: every VU's sockets, connections and clients closed.
	waitForGoroutines(t, before)
}

// kafkaEventUsers reads want events from the cluster and returns how many
// each user produced, failing the test if an event's key is not the user
// in its value.
func kafkaEventUsers(t *testing.T, brokers []string, want int) map[string]int {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumeTopics("events"), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	users := map[string]int{}
	for n := 0; n < want && ctx.Err() == nil; {
		cl.PollFetches(ctx).EachRecord(func(r *kgo.Record) {
			n++
			var v struct{ User string }
			if err := json.Unmarshal(r.Value, &v); err != nil || v.User != string(r.Key) {
				t.Errorf("event with key %q and value %s: produced by another VU", r.Key, r.Value)
			}
			users[v.User]++
		})
	}
	return users
}

// waitForGoroutines fails the test unless the goroutine count returns to
// near before: servers' connection goroutines take a moment to end.
func waitForGoroutines(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > before+4 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+4 {
		buf := make([]byte, 1<<20)
		t.Errorf("goroutines: %d after the run, %d before\n%s", n, before, buf[:runtime.Stack(buf, true)])
	}
}

// Cancelling the run (Ctrl+C) in the middle of iterations returns
// promptly with a partial result. The cancel comes while one VU waits for
// a GraphQL response that never comes, and the other VUs are in their
// own calls of any protocol. Calls cut short are not counted as failures, teardown
// still runs, and nothing is left running.
func TestMixedProtocolsCancellation(t *testing.T) {
	targets := newMixedTargets(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	targets.onQuery = func(n int64) bool {
		if n == 40 {
			cancel()
			return true
		}
		return false
	}
	before := runtime.NumGoroutine()
	var console bytes.Buffer
	start := time.Now()
	res, err := Run(ctx, Params{
		Config:    config.Config{Script: mixedExample(t), GracefulStop: time.Minute},
		Overrides: config.Overrides{VUs: intp(8), Duration: durp(time.Hour)},
		Env:       targets.env,
		Console:   &console,
	})
	if err != nil {
		t.Fatalf("an interrupted run must return its partial result, got error %v", err)
	}
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("the run took %v to stop after cancellation", took)
	}
	s := res.Summary
	if !res.Interrupted || s.Iterations == 0 {
		t.Errorf("Interrupted=%v, %d iterations; want a partial result marked interrupted", res.Interrupted, s.Iterations)
	}
	if s.ScriptErrors != 0 {
		t.Errorf("script error: %s", s.FirstScriptError)
	}
	for _, name := range mixedFailures[1:] {
		if f, _ := s.Family(name); f.Trues != 0 {
			t.Errorf("%s: %d failures from cancelled calls", name, f.Trues)
		}
	}
	if !strings.Contains(console.String(), "mixed-protocols: done") {
		t.Errorf("teardown did not run after cancellation; console: %q", console.String())
	}
	waitForGoroutines(t, before)
}
