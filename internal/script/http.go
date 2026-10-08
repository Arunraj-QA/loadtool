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
		var keep *bool
		req.Header, keep = vu.readParams(params.ToObject(rt))
		if keep != nil {
			req.KeepBody = *keep
		}
	}

	res := httpclient.Do(vu.ctx, vu.client, req, vu.rec)
	return rt.NewDynamicObject(&response{vu: vu, res: res, url: req.URL, discarded: !req.KeepBody})
}

// discardedHint says how to keep a body that was discarded (ADR-013).
const discardedHint = `response bodies are discarded by default; set options.discardResponseBodies: false, or pass { responseType: "text" } in this request's params`

var errInitRequest = errors.New("http requests are not allowed in the script's top-level code; make them inside the default function")

// readParams returns the request headers from params, with params.cookies
// added to the Cookie header, and whether params.responseType asks to keep
// ("text") or discard ("none") this response's body; nil leaves it to
// options.discardResponseBodies. It warns, once per run, about keys
// LoadTool does not support yet.
func (vu *VU) readParams(params *goja.Object) (http.Header, *bool) {
	var h http.Header
	var cookies goja.Value
	var keep *bool
	for _, k := range params.Keys() {
		switch k {
		case "headers":
			h = headersFrom(vu.rt, params.Get(k))
		case "cookies":
			cookies = params.Get(k)
		case "responseType":
			keep = responseType(vu.rt, params.Get(k))
		default:
			vu.warn.once(`http params key "` + k + `" is not supported yet and was ignored`)
		}
	}
	if isSet(cookies) {
		if h == nil {
			h = make(http.Header)
		}
		// AddCookie validates names and values and joins them with "; ",
		// after any Cookie header the script set. The jar's cookies are
		// added when the request is sent.
		req := http.Request{Header: h}
		obj := cookies.ToObject(vu.rt)
		for _, name := range obj.Keys() {
			req.AddCookie(&http.Cookie{Name: name, Value: obj.Get(name).String()})
		}
	}
	return h, keep
}

var keepBody, discardBody = true, false

// responseType reads params.responseType: "text" keeps the body, "none"
// discards it. "binary" (k6's third value) is not supported.
func responseType(rt *goja.Runtime, v goja.Value) *bool {
	if !isSet(v) {
		return nil
	}
	switch v.String() {
	case "text":
		return &keepBody
	case "none":
		return &discardBody
	}
	panic(rt.NewTypeError(`http: params.responseType must be "text" or "none", not %q`, v.String()))
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
