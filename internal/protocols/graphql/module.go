// Package graphql is the GraphQL protocol module, imported by scripts as
// "loadtool/graphql" (ADR-021). It is an HTTP-based module: operations
// are JSON POSTs sent with httpclient.Send over the VU's HTTP session, so
// they share HTTP's transport (HTTP/1.1 or HTTP/2), connection pool and
// cookie jar, and are measured by the same code. They are recorded only
// under graphql_* metrics, never http_* (Phase 2 scope decision 1).
//
//	const res = graphql.query(url, `query ($id: Int!) { product(id: $id) { name } }`, { variables: { id: 3 } });
//	res.http_ok   // transport: an HTTP 2xx response
//	res.ok        // GraphQL: http_ok, a JSON body, and no errors
package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/httpclient"
	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/protocol"
)

// Metric family names (ADR-015, ADR-021).
const (
	MetricReqDuration = "graphql_req_duration"
	MetricReqs        = "graphql_reqs"
	MetricReqFailed   = "graphql_req_failed"
	// MetricErrors counts operations whose response carried GraphQL
	// errors: application errors, apart from transport failures.
	MetricErrors = "graphql_errors"
)

// Module is the GraphQL protocol module.
type Module struct{}

var _ protocol.Module = Module{}

func (Module) Name() string      { return "graphql" }
func (Module) Exports() []string { return []string{"query", "mutation", "Client"} }

func (Module) Metrics() []metrics.Def {
	return []metrics.Def{
		{Name: MetricReqDuration, Kind: metrics.Trend},
		{Name: MetricReqs, Kind: metrics.Counter},
		{Name: MetricReqFailed, Kind: metrics.Rate},
		{Name: MetricErrors, Kind: metrics.Counter},
	}
}

// run holds the family IDs; the transport is the VU's HTTP session.
type run struct {
	ops    protocol.Families
	errors metrics.FamilyID
}

func (Module) NewRun(env protocol.RunEnv) (protocol.Run, error) {
	ids, err := protocol.FamilyIDs(env, MetricReqDuration, MetricReqs, MetricReqFailed, MetricErrors)
	if err != nil {
		return nil, err
	}
	return &run{ops: protocol.Families{Duration: ids[0], Count: ids[1], Failed: ids[2]}, errors: ids[3]}, nil
}

func (r *run) NewInstance(vu protocol.VU) (protocol.Instance, error) {
	return &instance{vu: vu, run: r}, nil
}

func (r *run) Close(context.Context) error { return nil }

// instance is the module in one VU. It holds nothing between operations:
// connections and cookies are the VU's HTTP session.
type instance struct {
	vu        protocol.VU
	run       *run
	obj       *goja.Object
	stringify goja.Callable
	parse     goja.Callable
}

func (i *instance) Value() goja.Value {
	if i.obj == nil {
		rt := i.vu.Runtime()
		i.obj = rt.NewObject()
		_ = i.obj.Set("query", func(call goja.FunctionCall) goja.Value {
			return i.operation("query", call.Argument(0), call.Argument(1), call.Argument(2), nil)
		})
		_ = i.obj.Set("mutation", func(call goja.FunctionCall) goja.Value {
			return i.operation("mutation", call.Argument(0), call.Argument(1), call.Argument(2), nil)
		})
		_ = i.obj.Set("Client", func(call goja.ConstructorCall) *goja.Object {
			return i.newClient(call.This, call.Argument(0), call.Argument(1))
		})
	}
	return i.obj
}

func (i *instance) BeginIteration()             {}
func (i *instance) EndIteration()               {}
func (i *instance) Close(context.Context) error { return nil }

// newClient builds a client with an endpoint and default headers:
// new graphql.Client(url, { headers }).
func (i *instance) newClient(this *goja.Object, url, params goja.Value) *goja.Object {
	rt := i.vu.Runtime()
	if !isSet(url) {
		panic(rt.NewTypeError("new graphql.Client: an endpoint URL is required"))
	}
	defaults := headersFrom(rt, params, nil)
	_ = this.Set("url", url.String())
	_ = this.Set("query", func(call goja.FunctionCall) goja.Value {
		return i.operation("query", url, call.Argument(0), call.Argument(1), defaults)
	})
	_ = this.Set("mutation", func(call goja.FunctionCall) goja.Value {
		return i.operation("mutation", url, call.Argument(0), call.Argument(1), defaults)
	})
	return nil // use this
}

var errInit = errors.New("graphql requests are not allowed in the script's top-level code; make them inside the default function")

// response is the part of a GraphQL response the module itself reads to
// judge it; data and errors are converted for the script lazily.
type response struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// operation sends one query or mutation and returns its result.
func (i *instance) operation(kind string, url, document, params goja.Value, defaults http.Header) goja.Value {
	rt := i.vu.Runtime()
	ctx := i.vu.Context()
	if ctx == nil {
		panic(rt.NewGoError(errInit))
	}
	if !isSet(url) {
		panic(rt.NewTypeError("graphql.%s: an endpoint URL is required", kind))
	}
	if !isSet(document) {
		panic(rt.NewTypeError("graphql.%s: a GraphQL document is required", kind))
	}
	header := headersFrom(rt, params, defaults)
	if header.Get("Content-Type") == "" {
		header.Set("Content-Type", "application/json")
	}
	if header.Get("Accept") == "" {
		header.Set("Accept", "application/graphql-response+json, application/json")
	}

	res := rt.NewObject()
	_ = res.Set("kind", kind)
	body, err := i.requestBody(document, params)
	if err != nil {
		return i.finish(res, protocol.Outcome{Err: err}, nil, nil)
	}
	if t := timeout(rt, params); t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t)
		defer cancel()
	}
	hres, sent := httpclient.Send(ctx, i.vu.HTTPClient(), httpclient.Request{
		Method: http.MethodPost, URL: url.String(), Body: body, Header: header, KeepBody: true,
	})
	if !sent {
		return i.finish(res, protocol.Outcome{Err: hres.Err}, nil, nil)
	}

	out := protocol.Outcome{Duration: hres.Duration, Err: hres.Err, Sent: true}
	var gql response
	switch {
	case hres.Err != nil:
		// transport failure: Record classifies it
	case hres.Status < 200 || hres.Status >= 300:
		out.Code, out.Err = protocol.CodeServer, fmt.Errorf("HTTP %d %s", hres.Status, http.StatusText(hres.Status))
		_ = json.Unmarshal(hres.Body, &gql) // GraphQL-over-HTTP errors may come with 4xx
	default:
		if err := json.Unmarshal(hres.Body, &gql); err != nil || (gql.Data == nil && gql.Errors == nil) {
			out.Code, out.Err = protocol.CodeProtocol, errors.New("the response is not a GraphQL JSON response")
		}
	}
	if len(gql.Errors) > 0 && out.Err == nil {
		out.Code = protocol.CodeServer
		out.Err = errors.New(gql.Errors[0].Message)
		if n := len(gql.Errors); n > 1 {
			out.Err = fmt.Errorf("%s (and %d more GraphQL errors)", gql.Errors[0].Message, n-1)
		}
	}
	return i.finish(res, out, &hres, &gql)
}

// finish records the operation and fills in its result.
func (i *instance) finish(res *goja.Object, out protocol.Outcome, hres *httpclient.Result, gql *response) goja.Value {
	rt := i.vu.Runtime()
	code := protocol.Record(i.vu, i.run.ops, out)
	hasErrors := gql != nil && len(gql.Errors) > 0
	if hasErrors {
		if rec := i.vu.Recorder(); rec != nil && i.vu.Context().Err() == nil {
			rec.Add(i.run.errors, 1)
		}
	}
	msg := ""
	if out.Err != nil {
		msg = out.Err.Error()
	}
	_ = res.Set("error", msg)
	_ = res.Set("error_code", string(code))
	_ = res.Set("ok", code == protocol.CodeNone)

	status, httpOK := 0, false
	var body []byte
	if hres != nil {
		status, body = hres.Status, hres.Body
		httpOK = hres.Err == nil && status >= 200 && status < 300
		_ = res.Set("proto", hres.Proto)
		_ = res.Set("headers", headersValue(rt, hres.Header))
	} else {
		_ = res.Set("proto", "")
		_ = res.Set("headers", rt.NewObject())
	}
	_ = res.Set("status", status)
	_ = res.Set("http_ok", httpOK)
	timings := rt.NewObject()
	_ = timings.Set("duration", float64(out.Duration)/float64(time.Millisecond))
	_ = res.Set("timings", timings)

	// body, data and errors are converted only when read.
	var parsed *goja.Object
	parseBody := func() *goja.Object {
		if parsed == nil {
			parsed = rt.NewObject()
			if gql != nil && (gql.Data != nil || gql.Errors != nil) {
				if v, err := i.parseJSON(string(body)); err == nil {
					parsed = v.ToObject(rt)
				}
			}
		}
		return parsed
	}
	accessor := func(name string, get func() goja.Value) {
		_ = res.DefineAccessorProperty(name, rt.ToValue(func(goja.FunctionCall) goja.Value { return get() }), nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
	}
	accessor("body", func() goja.Value {
		if body == nil {
			return goja.Null()
		}
		return rt.ToValue(string(body))
	})
	accessor("data", func() goja.Value {
		if d := parseBody().Get("data"); isSet(d) {
			return d
		}
		return goja.Null()
	})
	accessor("errors", func() goja.Value {
		if e := parseBody().Get("errors"); isSet(e) {
			return e
		}
		return rt.NewArray()
	})
	return res
}
