package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/protocols/graphql/graphqltest"
	"github.com/Arunraj-QA/loadtool/internal/report"
)

// h2cServer serves "/" (plain HTTP) and "/graphql" over HTTP/1.1 and
// HTTP/2 without TLS.
func h2cServer(t *testing.T) string {
	t.Helper()
	shop, err := graphqltest.Handler(0)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/graphql", shop)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux, Protocols: new(http.Protocols)}
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return "http://" + ln.Addr().String()
}

// GraphQL rides the HTTP transport (ADR-021): with httpVersion "2" its
// operations use HTTP/2 through the shared pool, yet they are counted
// only under graphql_*: http_reqs and the HTTP protocol counts hold the
// script's own HTTP requests alone. HTTP 200 with GraphQL errors fails
// graphql_req_failed and counts in graphql_errors; thresholds and the
// reports see the families.
func TestRunMixesHTTPAndGraphQL(t *testing.T) {
	base := h2cServer(t)
	path := scriptFile(t, `import http from "loadtool/http";
import graphql from "loadtool/graphql";
import { check } from "loadtool";

export const options = {
	httpVersion: "2",
	thresholds: {
		graphql_req_duration: ["p(95)<1000"],
		graphql_errors: ["count>0"],
		http_req_failed: ["rate==0"],
	},
};

export default function () {
	http.get("`+base+`/");
	const ok = graphql.query("`+base+`/graphql", "query ($id: Int!) { product(id: $id) { name } }", { variables: { id: 1 } });
	check(ok, {
		"graphql ok": (r) => r.ok && r.http_ok && r.data.product.name === "Espresso beans, 1 kg",
		"over HTTP/2": (r) => r.proto === "HTTP/2.0",
	});
	const bad = graphql.query("`+base+`/graphql", "{ product(id: 404) { name } }");
	check(bad, { "200 with errors": (r) => r.status === 200 && r.http_ok && !r.ok && r.errors.length === 1 });
}`)
	res, err := Run(context.Background(), Params{
		Config:    config.Config{Script: path, GracefulStop: time.Second},
		Overrides: config.Overrides{VUs: intp(4), Duration: durp(300 * time.Millisecond)},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summary
	if s.ScriptErrors != 0 {
		t.Fatalf("script error: %s", s.FirstScriptError)
	}
	ops, _ := s.Family("graphql_reqs")
	failed, _ := s.Family("graphql_req_failed")
	gqlErrors, _ := s.Family("graphql_errors")
	if s.Requests == 0 || ops.Count != 2*s.Requests {
		t.Errorf("%d HTTP requests, %d GraphQL operations; want two operations per request and none in http_reqs", s.Requests, ops.Count)
	}
	if s.Protocols.HTTP2 != s.Requests {
		t.Errorf("HTTP/2 responses counted = %d, want %d (the HTTP requests only)", s.Protocols.HTTP2, s.Requests)
	}
	if failed.Trues != s.Requests || gqlErrors.Count != s.Requests {
		t.Errorf("failed %d, graphql_errors %d; want one per iteration (%d)", failed.Trues, gqlErrors.Count, s.Requests)
	}
	for _, c := range s.Checks {
		if c.Fails != 0 {
			t.Errorf("check %q failed %d times", c.Name, c.Fails)
		}
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
	for _, name := range []string{"graphql_req_duration", "graphql_reqs", "graphql_req_failed", "graphql_errors"} {
		if _, ok := doc.Metrics[name]; !ok {
			t.Errorf("JSON summary misses %s", name)
		}
	}
	b.Reset()
	if err := report.HTML(&b, res, "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "<td><code>graphql_errors</code></td>") {
		t.Error("HTML report misses graphql_errors")
	}
}
