package script

import (
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/httpclient"
)

// httpProps are the methods of the loadtool/http module (ADR-008):
//
//	get(url, params?)
//	post(url, body?, params?), and put, patch, del alike
//	request(method, url, body?, params?)
//
// body is a string; params is { headers: { name: value } }. Each returns a
// response (see response). Transport failures do not throw: status is 0
// and error is set. Methods are built on first use (see lazyObject).
var httpProps = []lazyProp{
	{"get", httpMethod(http.MethodGet, false)},
	{"post", httpMethod(http.MethodPost, true)},
	{"put", httpMethod(http.MethodPut, true)},
	{"patch", httpMethod(http.MethodPatch, true)},
	{"del", httpMethod(http.MethodDelete, true)},
	{"request", func(vu *VU) goja.Value {
		return vu.rt.ToValue(func(call goja.FunctionCall) goja.Value {
			method := call.Argument(0)
			if !isSet(method) {
				panic(vu.rt.NewTypeError("http.request: method is required"))
			}
			return vu.request(strings.ToUpper(method.String()), call.Argument(1), call.Argument(2), call.Argument(3))
		})
	}},
}

// httpMethod builds a module method for a fixed HTTP method. Methods that
// take a body have the signature (url, body, params); get has (url, params).
func httpMethod(method string, hasBody bool) func(vu *VU) goja.Value {
	return func(vu *VU) goja.Value {
		return vu.rt.ToValue(func(call goja.FunctionCall) goja.Value {
			if hasBody {
				return vu.request(method, call.Argument(0), call.Argument(1), call.Argument(2))
			}
			return vu.request(method, call.Argument(0), goja.Undefined(), call.Argument(1))
		})
	}
}

// newHTTPModule builds the Go side of the loadtool/http module.
func (vu *VU) newHTTPModule() goja.Value {
	return vu.newLazyObject(httpProps)
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

	req := httpclient.Request{Method: method, URL: url.String(), KeepBody: !vu.discardBodies}
	if isSet(body) {
		if t := body.ExportType(); t == nil || t.Kind() != reflect.String {
			panic(rt.NewTypeError("http: the request body must be a string; use JSON.stringify(...) to send JSON"))
		}
		req.Body = body.String()
	}
	if isSet(params) {
		req.Header = vu.readParams(params.ToObject(rt))
	}

	res := httpclient.Do(vu.ctx, vu.client, req, vu.rec)
	return rt.NewDynamicObject(&response{vu: vu, res: res, url: req.URL})
}

var errInitRequest = errors.New("http requests are not allowed in the script's top-level code; make them inside the default function")

// readParams returns the request headers from params and warns, once per
// run, about keys LoadTool does not support yet.
func (vu *VU) readParams(params *goja.Object) http.Header {
	var h http.Header
	for _, k := range params.Keys() {
		if k == "headers" {
			h = headersFrom(vu.rt, params.Get(k))
			continue
		}
		vu.warn.once(`http params key "` + k + `" is not supported yet and was ignored`)
	}
	return h
}

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
