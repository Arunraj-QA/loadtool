package script

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/httpclient"
)

// newHTTPModule builds the `http` global for this VU:
//
//	http.get(url, params?)
//	http.request(method, url, body?, params?)
//
// params is { headers: { name: value } }. Both return
// { status, error, timings: { duration } } with duration in milliseconds.
// Transport failures do not throw: status is 0 and error is set.
func (vu *VU) newHTTPModule() *goja.Object {
	o := vu.rt.NewObject()
	_ = o.Set("get", func(call goja.FunctionCall) goja.Value {
		return vu.request(http.MethodGet, call.Argument(0), goja.Undefined(), call.Argument(1))
	})
	_ = o.Set("request", func(call goja.FunctionCall) goja.Value {
		method := call.Argument(0)
		if !isSet(method) {
			panic(vu.rt.NewTypeError("http.request: method is required"))
		}
		return vu.request(strings.ToUpper(method.String()), call.Argument(1), call.Argument(2), call.Argument(3))
	})
	return o
}

func (vu *VU) request(method string, url, body, params goja.Value) goja.Value {
	rt := vu.rt
	if vu.rec == nil {
		// Top-level script code runs once per VU before the test starts;
		// requests there would not be measured.
		panic(rt.NewGoError(errInitRequest))
	}
	if !isSet(url) {
		panic(rt.NewTypeError("http: url is required"))
	}

	req := httpclient.Request{Method: method, URL: url.String()}
	if isSet(body) {
		req.Body = body.String()
	}
	if isSet(params) {
		req.Header = headersFrom(rt, params.ToObject(rt).Get("headers"))
	}

	res := httpclient.Do(vu.ctx, vu.client, req, vu.rec)

	timings := rt.NewObject()
	_ = timings.Set("duration", float64(res.Duration)/1e6)
	out := rt.NewObject()
	_ = out.Set("status", res.Status)
	_ = out.Set("timings", timings)
	errMsg := ""
	if res.Err != nil {
		errMsg = res.Err.Error()
	}
	_ = out.Set("error", errMsg)
	return out
}

var errInitRequest = errors.New("http requests are not allowed in the script's top-level code; make them inside the default function")

func headersFrom(rt *goja.Runtime, v goja.Value) http.Header {
	if !isSet(v) {
		return nil
	}
	obj := v.ToObject(rt)
	keys := obj.Keys()
	h := make(http.Header, len(keys))
	for _, k := range keys {
		h.Set(k, obj.Get(k).String())
	}
	return h
}

// isSet reports whether v is neither missing, undefined nor null.
func isSet(v goja.Value) bool {
	return v != nil && !goja.IsUndefined(v) && !goja.IsNull(v)
}
