package graphql

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dop251/goja"
)

func isSet(v goja.Value) bool {
	return v != nil && !goja.IsUndefined(v) && !goja.IsNull(v)
}

func (i *instance) jsonFuncs() (goja.Callable, goja.Callable) {
	if i.stringify == nil {
		rt := i.vu.Runtime()
		j := rt.Get("JSON").ToObject(rt)
		i.stringify, _ = goja.AssertFunction(j.Get("stringify"))
		i.parse, _ = goja.AssertFunction(j.Get("parse"))
	}
	return i.stringify, i.parse
}

func (i *instance) parseJSON(s string) (goja.Value, error) {
	_, parse := i.jsonFuncs()
	return parse(goja.Undefined(), i.vu.Runtime().ToValue(s))
}

// requestBody builds {"query", "variables", "operationName"}. Variables
// that cannot be serialized make the operation invalid (never sent).
func (i *instance) requestBody(document, params goja.Value) (string, error) {
	rt := i.vu.Runtime()
	req := rt.NewObject()
	_ = req.Set("query", document.String())
	if isSet(params) {
		p := params.ToObject(rt)
		if v := p.Get("variables"); isSet(v) {
			_ = req.Set("variables", v)
		}
		if op := p.Get("operationName"); isSet(op) {
			_ = req.Set("operationName", op.String())
		}
	}
	stringify, _ := i.jsonFuncs()
	s, err := stringify(goja.Undefined(), req)
	if err != nil {
		return "", fmt.Errorf("the variables are not JSON-serializable: %w", err)
	}
	return s.String(), nil
}

// headersFrom returns defaults overlaid with params.headers.
func headersFrom(rt *goja.Runtime, params goja.Value, defaults http.Header) http.Header {
	h := defaults.Clone()
	if h == nil {
		h = make(http.Header)
	}
	if !isSet(params) {
		return h
	}
	hv := params.ToObject(rt).Get("headers")
	if !isSet(hv) {
		return h
	}
	obj := hv.ToObject(rt)
	for _, k := range obj.Keys() {
		h.Set(k, obj.Get(k).String())
	}
	return h
}

// timeout reads params.timeout: milliseconds, or a duration such as "2s";
// 0 when unset (the HTTP client's own 30 s timeout applies).
func timeout(rt *goja.Runtime, params goja.Value) time.Duration {
	if !isSet(params) {
		return 0
	}
	v := params.ToObject(rt).Get("timeout")
	if !isSet(v) {
		return 0
	}
	if s, ok := v.Export().(string); ok {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			panic(rt.NewTypeError("graphql: timeout %q must be a positive duration, such as \"2s\"", s))
		}
		return d
	}
	ms := v.ToFloat()
	if ms <= 0 || ms != ms {
		panic(rt.NewTypeError("graphql: timeout must be a positive number of milliseconds or a duration such as \"2s\""))
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// headersValue converts response headers to { Name: "v1, v2" }, as HTTP
// responses have them.
func headersValue(rt *goja.Runtime, h http.Header) goja.Value {
	o := rt.NewObject()
	for k, vs := range h {
		_ = o.Set(k, strings.Join(vs, ", "))
	}
	return o
}
