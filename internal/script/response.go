package script

import (
	"maps"
	"slices"
	"strings"

	"github.com/dop251/goja"

	"github.com/Arunraj-QA/loadtool/internal/httpclient"
)

// response is the object an http call returns (ADR-008):
//
//	status    number; 0 when no response was received
//	error     "" or the transport error
//	headers   { "Content-Type": "..." }, repeated headers joined with ", "
//	body      string, or null when bodies are discarded or nothing arrived
//	json()    body parsed as JSON
//	timings   { duration } in milliseconds
//	url       the request URL
//
// It is a dynamic object so headers, body and json are converted only if
// the script reads them: a script that only checks status pays for none
// of them. Scripts can add or replace properties.
type response struct {
	vu  *VU
	res httpclient.Result
	url string

	headers, body, timings, json goja.Value // built on first access
	extra                        map[string]goja.Value
}

var _ goja.DynamicObject = (*response)(nil)

// responseKeys are the enumerable properties; json is a method and, like
// a prototype method, is not listed.
var responseKeys = []string{"status", "error", "headers", "body", "timings", "url"}

func (r *response) Get(key string) goja.Value {
	if v, ok := r.extra[key]; ok {
		return v
	}
	rt := r.vu.rt
	switch key {
	case "status":
		return rt.ToValue(r.res.Status)
	case "error":
		if r.res.Err == nil {
			return rt.ToValue("")
		}
		return rt.ToValue(r.res.Err.Error())
	case "url":
		return rt.ToValue(r.url)
	case "headers":
		if r.headers == nil {
			r.headers = r.headersObject()
		}
		return r.headers
	case "body":
		if r.body == nil {
			r.body = goja.Null()
			if r.res.Body != nil {
				r.body = rt.ToValue(string(r.res.Body))
			}
		}
		return r.body
	case "timings":
		if r.timings == nil {
			t := rt.NewObject()
			_ = t.Set("duration", float64(r.res.Duration)/1e6)
			r.timings = t
		}
		return r.timings
	case "json":
		if r.json == nil {
			r.json = rt.ToValue(r.parseJSON)
		}
		return r.json
	}
	return nil
}

func (r *response) headersObject() goja.Value {
	o := r.vu.rt.NewObject()
	// Sorted, so Object.keys(res.headers) is deterministic.
	for _, name := range slices.Sorted(maps.Keys(r.res.Header)) {
		_ = o.Set(name, strings.Join(r.res.Header[name], ", "))
	}
	return o
}

// parseJSON implements res.json(). Parse errors are thrown as the
// SyntaxError JSON.parse raises.
func (r *response) parseJSON(goja.FunctionCall) goja.Value {
	rt := r.vu.rt
	if r.res.Body == nil {
		if r.vu.discardBodies {
			panic(rt.NewTypeError("res.json(): the response body was discarded because options.discardResponseBodies is true"))
		}
		panic(rt.NewTypeError("res.json(): no response was received (%s)", r.Get("error").String()))
	}
	parse, ok := goja.AssertFunction(rt.Get("JSON").ToObject(rt).Get("parse"))
	if !ok {
		panic(rt.NewTypeError("res.json(): JSON.parse is not available"))
	}
	v, err := parse(goja.Undefined(), r.Get("body"))
	if err != nil {
		if ex, isEx := err.(*goja.Exception); isEx {
			panic(ex.Value())
		}
		panic(rt.NewGoError(err))
	}
	return v
}

func (r *response) Set(key string, val goja.Value) bool {
	if r.extra == nil {
		r.extra = make(map[string]goja.Value)
	}
	r.extra[key] = val
	return true
}

func (r *response) Has(key string) bool {
	if _, ok := r.extra[key]; ok {
		return true
	}
	return key == "json" || indexOf(responseKeys, key) >= 0
}

func (r *response) Delete(key string) bool {
	delete(r.extra, key)
	return true
}

func (r *response) Keys() []string {
	keys := append([]string(nil), responseKeys...)
	for k := range r.extra {
		if indexOf(responseKeys, k) < 0 {
			keys = append(keys, k)
		}
	}
	return keys
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
