package graphql_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/protocol/protocoltest"
	lgraphql "github.com/Arunraj-QA/loadtool/internal/protocols/graphql"
	"github.com/Arunraj-QA/loadtool/internal/protocols/graphql/graphqltest"
)

// server serves the graphqltest shop at /graphql, plus routes for the
// cases a real GraphQL server rarely produces on purpose.
func server(t testing.TB) *httptest.Server {
	t.Helper()
	shop, err := graphqltest.Handler(0)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/graphql", shop)
	mux.HandleFunc("/gateway-error", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	})
	mux.HandleFunc("/bad-request", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/graphql-response+json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errors":[{"message":"Syntax Error: Unexpected Name"}]}`))
	})
	mux.HandleFunc("/not-json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>maintenance</html>"))
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		// Reading the body lets the server notice the client going away.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
	})
	// /echo answers with the request's headers and body as GraphQL data.
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		cookie := ""
		if c, err := r.Cookie("session"); err == nil {
			cookie = c.Value
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"authorization": r.Header.Get("Authorization"), "tenant": r.Header.Get("X-Tenant"),
			"contentType": r.Header.Get("Content-Type"), "accept": r.Header.Get("Accept"),
			"cookie": cookie, "request": req,
		}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func run(t *testing.T, h *protocoltest.Harness, srv *httptest.Server, src string) *goja.Object {
	t.Helper()
	if _, err := h.Run(strings.ReplaceAll(src, "URL", srv.URL)); err != nil {
		t.Fatalf("script: %v", err)
	}
	rt := h.VU().Runtime()
	return rt.Get("res").ToObject(rt)
}

func get(t *testing.T, o *goja.Object, path string) any {
	t.Helper()
	var v goja.Value = o
	for _, k := range strings.Split(path, ".") {
		if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
			return nil
		}
		v = v.(*goja.Object).Get(k)
	}
	if v == nil {
		return nil
	}
	return v.Export()
}

func TestQueryWithVariables(t *testing.T) {
	srv := server(t)
	h := protocoltest.New(t, lgraphql.Module{})
	res := run(t, h, srv, `
var res = graphql.query("URL/graphql", "query Product($id: Int!) { product(id: $id) { id name } }", {
	variables: { id: 3 }, operationName: "Product",
});`)
	for path, want := range map[string]any{
		"ok": true, "http_ok": true, "status": int64(200), "kind": "query", "error": "", "error_code": "",
		"data.product.name": "Ceramic cup", "data.product.id": int64(3),
	} {
		if got := get(t, res, path); got != want {
			t.Errorf("%s = %v (%T), want %v", path, got, got, want)
		}
	}
	if n := len(get(t, res, "errors").([]any)); n != 0 {
		t.Errorf("errors has %d entries", n)
	}
	if h.Family(lgraphql.MetricReqs).Count != 1 || h.Family(lgraphql.MetricReqFailed).Trues != 0 ||
		h.Family(lgraphql.MetricReqDuration).Count != 1 || h.Family(lgraphql.MetricErrors).Count != 0 {
		t.Error("metrics miscounted")
	}
}

func TestMutation(t *testing.T) {
	srv := server(t)
	h := protocoltest.New(t, lgraphql.Module{})
	res := run(t, h, srv, `
var res = graphql.mutation("URL/graphql", "mutation ($p: Int!, $q: Int!) { placeOrder(productId: $p, quantity: $q) { productId quantity totalCents } }", {
	variables: { p: 2, q: 3 },
});`)
	if !res.Get("ok").ToBoolean() || get(t, res, "kind") != "mutation" || get(t, res, "data.placeOrder.totalCents") != int64(3*3900) {
		t.Errorf("result = %v, data %v", res.Export(), get(t, res, "data"))
	}
}

// HTTP 200 with GraphQL errors: the transport succeeded, the operation
// did not. Partial data stays available.
func TestHTTP200WithGraphQLErrors(t *testing.T) {
	srv := server(t)
	h := protocoltest.New(t, lgraphql.Module{})
	res := run(t, h, srv, `
var one = graphql.query("URL/graphql", "{ product(id: 99) { name } products { id } }");
var two = graphql.query("URL/graphql", "{ a: fail(message: \"first\") b: fail(message: \"second\") }");
var res = {
	one: { status: one.status, http_ok: one.http_ok, ok: one.ok, code: one.error_code, error: one.error,
		product: one.data.product, products: one.data.products.length, nErrors: one.errors.length, first: one.errors[0].message },
	two: { error: two.error, nErrors: two.errors.length },
};`)
	for path, want := range map[string]any{
		"one.status": int64(200), "one.http_ok": true, "one.ok": false, "one.code": "server",
		"one.error": "no product with id 99", "one.product": nil, "one.products": int64(5),
		"one.nErrors": int64(1), "one.first": "no product with id 99",
		"two.nErrors": int64(2),
	} {
		if got := get(t, res, path); got != want {
			t.Errorf("%s = %v, want %v", path, got, want)
		}
	}
	// Sibling fields resolve concurrently, so either error may come first.
	if e, _ := get(t, res, "two.error").(string); e != "first (and 1 more GraphQL errors)" && e != "second (and 1 more GraphQL errors)" {
		t.Errorf("two.error = %q", e)
	}
	if f := h.Family(lgraphql.MetricReqFailed); f.Count != 2 || f.Trues != 2 {
		t.Errorf("failed = %d of %d", f.Trues, f.Count)
	}
	if h.Family(lgraphql.MetricErrors).Count != 2 {
		t.Errorf("graphql_errors = %d, want 2", h.Family(lgraphql.MetricErrors).Count)
	}
}

// Transport and HTTP failures, apart from GraphQL errors.
func TestFailureKinds(t *testing.T) {
	srv := server(t)
	refused := httptest.NewServer(http.NotFoundHandler())
	refusedURL := refused.URL
	refused.Close()
	h := protocoltest.New(t, lgraphql.Module{})
	res := run(t, h, srv, `
function r(x) { return [x.status, x.http_ok, x.ok, x.error_code, x.errors.length].join(","); }
var res = {
	gateway: r(graphql.query("URL/gateway-error", "{ products { id } }")),
	badRequest: r(graphql.query("URL/bad-request", "{ products ")),
	notJSON: r(graphql.query("URL/not-json", "{ products { id } }")),
	refused: r(graphql.query("`+refusedURL+`/graphql", "{ products { id } }")),
	invalid: r(graphql.query("::not a url", "{ products { id } }")),
};`)
	for k, want := range map[string]string{
		"gateway":    "502,false,false,server,0",
		"badRequest": "400,false,false,server,1", // GraphQL-over-HTTP errors come with a 4xx
		"notJSON":    "200,true,false,protocol,0",
		"refused":    "0,false,false,dial,0",
		"invalid":    "0,false,false,invalid,0",
	} {
		if got := get(t, res, k); got != want {
			t.Errorf("%s = %v, want %s", k, got, want)
		}
	}
	// Five operations failed; four were sent (the invalid one has no
	// duration); only the 400's body carried GraphQL errors.
	if f := h.Family(lgraphql.MetricReqFailed); f.Trues != 5 || h.Family(lgraphql.MetricReqDuration).Count != 4 ||
		h.Family(lgraphql.MetricErrors).Count != 1 {
		t.Errorf("failed %d, durations %d, graphql_errors %d", f.Trues, h.Family(lgraphql.MetricReqDuration).Count, h.Family(lgraphql.MetricErrors).Count)
	}
}

// Request headers, a Client's default headers, the content type, and the
// VU's cookies (its HTTP session) all reach the server.
func TestHeadersClientAndCookies(t *testing.T) {
	srv := server(t)
	h := protocoltest.New(t, lgraphql.Module{})
	u, _ := url.Parse(srv.URL)
	h.VU().HTTPClient().Jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "abc"}})
	res := run(t, h, srv, `
var api = new graphql.Client("URL/echo", { headers: { Authorization: "Bearer t1", "X-Tenant": "a" } });
var plain = api.query("{ x }");
var override = api.mutation("mutation { y }", { headers: { "X-Tenant": "b" }, variables: { v: 1 } });
var res = { plain: plain.data, override: override.data, kind: override.kind, url: api.url };`)
	for path, want := range map[string]any{
		"plain.authorization": "Bearer t1", "plain.tenant": "a", "plain.cookie": "abc",
		"plain.contentType": "application/json", "plain.accept": "application/graphql-response+json, application/json",
		"plain.request.query": "{ x }",
		"override.tenant":     "b", "override.authorization": "Bearer t1", "override.request.variables.v": int64(1),
		"kind": "mutation",
	} {
		if got := get(t, res, path); got != want {
			t.Errorf("%s = %v, want %v", path, got, want)
		}
	}
}

func TestTimeoutAndCancellation(t *testing.T) {
	srv := server(t)
	h := protocoltest.New(t, lgraphql.Module{})
	res := run(t, h, srv, `var res = graphql.query("URL/slow", "{ x }", { timeout: 100 });`)
	if get(t, res, "error_code") != "timeout" || get(t, res, "ok") != false {
		t.Errorf("timeout result = %v", res.Export())
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.VU().SetContext(ctx)
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, _ = h.Run(strings.ReplaceAll(`graphql.query("URL/slow", "{ x }")`, "URL", srv.URL))
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the query took %v after cancellation", took)
	}
	if n := h.Family(lgraphql.MetricReqs).Count; n != 1 { // only the timed-out one
		t.Errorf("operations recorded = %d, want 1 (the cancelled one is not)", n)
	}
}

func TestMisuse(t *testing.T) {
	h := protocoltest.New(t, lgraphql.Module{})
	for src, want := range map[string]string{
		`graphql.query()`:           "an endpoint URL is required",
		`graphql.query("http://x")`: "a GraphQL document is required",
		`new graphql.Client()`:      "an endpoint URL is required",
		`graphql.query("http://x", "{ a }", { timeout: "soon" })`: "positive duration",
	} {
		if _, err := h.Run(src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", src, err, want)
		}
	}
	h.VU().SetContext(nil)
	if _, err := h.Run(`graphql.query("http://x", "{ a }")`); err == nil || !strings.Contains(err.Error(), "top-level code") {
		t.Errorf("top-level query: %v", err)
	}
}

// Many VUs query at once (run with -race), each with its own runtime.
func TestConcurrentVUs(t *testing.T) {
	srv := server(t)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			h := protocoltest.New(t, lgraphql.Module{})
			_, err := h.Run(strings.ReplaceAll(`
for (var i = 1; i <= 25; i++) {
	var r = graphql.query("URL/graphql", "query ($id: Int!) { product(id: $id) { id } }", { variables: { id: (i % 5) + 1 } });
	if (!r.ok || r.data.product.id !== (i % 5) + 1) throw new Error("bad: " + r.error);
	var m = graphql.mutation("URL/graphql", "mutation { placeOrder(productId: 1, quantity: 1) { id } }");
	if (!m.ok) throw new Error("bad mutation: " + m.error);
}`, "URL", srv.URL))
			if err != nil {
				t.Error(err)
				return
			}
			if got := h.Family(lgraphql.MetricReqs).Count; got != 50 {
				t.Errorf("operations = %d, want 50", got)
			}
		})
	}
	wg.Wait()
}
