package report_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/report"
	"github.com/Arunraj-QA/loadtool/internal/runner"
)

// schemaPath is the published schema of the JSON summary.
const schemaPath = "../../docs/schemas/summary-v1.schema.json"

func loadSchema(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	return s
}

// validate checks doc against schema, supporting the keywords the
// summary schema uses: type, const, enum, pattern, minimum, properties,
// patternProperties, required, additionalProperties (false), anyOf, items
// and local $ref. It returns every
// violation, with its JSON path.
func validate(root, schema map[string]any, doc any, path string) []string {
	if ref, ok := schema["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		return validate(root, root["$defs"].(map[string]any)[name].(map[string]any), doc, path)
	}
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, path+": "+fmt.Sprintf(format, args...)) }

	if branches, ok := schema["anyOf"].([]any); ok {
		var all []string
		for _, b := range branches {
			e := validate(root, b.(map[string]any), doc, path)
			if len(e) == 0 {
				all = nil
				break
			}
			all = append(all, e...)
		}
		if all != nil {
			fail("matches none of anyOf: %s", strings.Join(all, "; "))
		}
	}

	if types, ok := schema["type"]; ok {
		var allowed []string
		switch tv := types.(type) {
		case string:
			allowed = []string{tv}
		case []any:
			for _, x := range tv {
				allowed = append(allowed, x.(string))
			}
		}
		if !slices.Contains(allowed, jsonType(doc)) && !(jsonType(doc) == "integer" && slices.Contains(allowed, "number")) {
			fail("type %s, want %v", jsonType(doc), allowed)
			return errs
		}
	}
	if c, ok := schema["const"]; ok && !equal(c, doc) {
		fail("value %v, want %v", doc, c)
	}
	if e, ok := schema["enum"].([]any); ok && !slices.ContainsFunc(e, func(v any) bool { return equal(v, doc) }) {
		fail("value %v not in %v", doc, e)
	}
	if pat, ok := schema["pattern"].(string); ok {
		if str, isStr := doc.(string); isStr && !regexp.MustCompile(pat).MatchString(str) {
			fail("value %q does not match %s", str, pat)
		}
	}
	if m, ok := schema["minimum"].(float64); ok {
		if n, isNum := doc.(float64); isNum && n < m {
			fail("value %v below the minimum %v", n, m)
		}
	}
	switch v := doc.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		for _, r := range asStrings(schema["required"]) {
			if _, ok := v[r]; !ok {
				fail("missing required field %q", r)
			}
		}
		patterns, _ := schema["patternProperties"].(map[string]any)
		for _, k := range sortedKeys(v) {
			sub, known := props[k]
			if known {
				errs = append(errs, validate(root, sub.(map[string]any), v[k], path+"."+k)...)
			}
			// As in JSON Schema, every matching pattern applies too, and
			// additionalProperties covers keys matched by neither.
			for pattern, psub := range patterns {
				if regexp.MustCompile(pattern).MatchString(k) {
					known = true
					errs = append(errs, validate(root, psub.(map[string]any), v[k], path+"."+k)...)
				}
			}
			if !known && schema["additionalProperties"] == false {
				fail("field %q is not in the schema", k)
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, x := range v {
				errs = append(errs, validate(root, items, x, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	}
	return errs
}

func jsonType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if x == math.Trunc(x) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	default:
		return "object"
	}
}

func equal(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) && jsonType(a) == jsonType(b) }

// asStrings returns a list of strings; a missing list (nil) is empty.
func asStrings(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, x := range list {
		out = append(out, x.(string))
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func decode(t *testing.T, b []byte) any {
	t.Helper()
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	return doc
}

// Every golden JSON summary (the output for each report test case)
// conforms to the published schema.
func TestGoldenSummariesMatchSchema(t *testing.T) {
	schema := loadSchema(t)
	files, _ := filepath.Glob("testdata/*.json")
	if len(files) == 0 {
		t.Fatal("no golden JSON files")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if errs := validate(schema, schema, decode(t, b), "$"); len(errs) > 0 {
			t.Errorf("%s:\n  %s", f, strings.Join(errs, "\n  "))
		}
	}
}

// The summary of a real run, with every executor, checks and passing and
// failing thresholds, conforms to the schema, and its outcome represents
// the failed threshold.
func TestRealRunMatchesSchema(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"ok":true}`)) }))
	t.Cleanup(srv.Close)
	path := filepath.Join(t.TempDir(), "test.ts")
	src := `import http from "loadtool/http";
import { check } from "loadtool";
export const options = {
	scenarios: {
		a: { executor: "constant-vus", vus: 2, duration: "300ms" },
		b: { executor: "ramping-vus", stages: [{ duration: "150ms", target: 2 }, { duration: "150ms", target: 2 }] },
		c: { executor: "constant-arrival-rate", rate: 20, duration: "300ms", preAllocatedVUs: 2 },
	},
	thresholds: { http_req_failed: ["rate<0.5"], http_reqs: ["count<0"], checks: ["rate==1"] },
};
export default function () { check(http.get("` + srv.URL + `", { responseType: "text" }), { "ok": (r) => r.json().ok === true }); }`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := runner.Run(context.Background(), runner.Params{Config: config.Config{Script: path, GracefulStop: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := report.JSON(&b, res, "v0.0.0-test"); err != nil {
		t.Fatal(err)
	}
	schema := loadSchema(t)
	doc := decode(t, b.Bytes())
	if errs := validate(schema, schema, doc, "$"); len(errs) > 0 {
		t.Fatalf("real summary does not match the schema:\n  %s\n%s", strings.Join(errs, "\n  "), b.String())
	}

	outcome := doc.(map[string]any)["outcome"].(map[string]any)
	if outcome["passed"] != false || outcome["exitCode"] != 99.0 {
		t.Errorf("outcome %v, want failed with exit code 99", outcome)
	}
	reasons := fmt.Sprint(outcome["reasons"])
	if !strings.Contains(reasons, "threshold failed: http_reqs count<0") || strings.Contains(reasons, "checks") {
		t.Errorf("reasons %v, want only the failed threshold", reasons)
	}
	failed := 0
	for _, th := range doc.(map[string]any)["thresholds"].([]any) {
		if th.(map[string]any)["passed"] == false {
			failed++
		}
	}
	if failed != 1 {
		t.Errorf("%d thresholds marked failed, want 1", failed)
	}
}

// The validator, and the schema's strictness, reject what they should.
func TestSchemaRejectsInvalidSummaries(t *testing.T) {
	schema := loadSchema(t)
	golden, err := os.ReadFile("testdata/thresholds.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"unknown field", func(d map[string]any) { d["debug"] = true }, `field "debug" is not in the schema`},
		{"missing field", func(d map[string]any) { delete(d, "outcome") }, `missing required field "outcome"`},
		{"wrong type", func(d map[string]any) { d["elapsedMs"] = "1s" }, "$.elapsedMs: type string"},
		{"bad exit code", func(d map[string]any) { d["outcome"].(map[string]any)["exitCode"] = 2.0 }, "$.outcome.exitCode: value 2"},
		{"unknown metric field", func(d map[string]any) { d["metrics"].(map[string]any)["http_reqs"].(map[string]any)["p50"] = 1.0 }, `$.metrics.http_reqs: field "p50"`},
		{"newer schema version", func(d map[string]any) { d["schemaVersion"] = 2.0 }, "$.schemaVersion: value 2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			doc := decode(t, golden).(map[string]any)
			tt.mutate(doc)
			errs := strings.Join(validate(schema, schema, doc, "$"), "\n")
			if !strings.Contains(errs, tt.want) {
				t.Errorf("errors %q, want one containing %q", errs, tt.want)
			}
		})
	}
}

// The schema lists every field it allows as required (except the
// executor-specific fields of a scenario), so a field the summary stops
// writing, or a field the schema forgets, cannot go unnoticed.
func TestSchemaRequiresEveryField(t *testing.T) {
	schema := loadSchema(t)
	var check func(path string, s map[string]any)
	check = func(path string, s map[string]any) {
		if props, ok := s["properties"].(map[string]any); ok && path != "$defs.scenario" {
			if s["additionalProperties"] != false {
				t.Errorf("%s: objects must be closed (additionalProperties: false)", path)
			}
			req := asStrings(s["required"])
			for k := range props {
				if !slices.Contains(req, k) {
					t.Errorf("%s: field %q is allowed but not required", path, k)
				}
			}
		}
		for _, key := range []string{"properties", "$defs"} {
			if m, ok := s[key].(map[string]any); ok {
				for k, v := range m {
					if sub, ok := v.(map[string]any); ok {
						p := path + "." + k
						if key == "$defs" {
							p = "$defs." + k
						}
						check(p, sub)
					}
				}
			}
		}
		if items, ok := s["items"].(map[string]any); ok {
			check(path+"[]", items)
		}
	}
	check("$", schema)
}

// The schema accepts protocol metric families only with a known protocol
// prefix and the shape their kind declares.
func TestSchemaChecksFamilies(t *testing.T) {
	schema := loadSchema(t)
	golden, err := os.ReadFile("testdata/families.json")
	if err != nil {
		t.Fatal(err)
	}
	metricsOf := func(d map[string]any) map[string]any { return d["metrics"].(map[string]any) }
	for _, tt := range []struct {
		name   string
		mutate func(d map[string]any)
		want   string
	}{
		{"unknown protocol", func(d map[string]any) { metricsOf(d)["smtp_sent"] = metricsOf(d)["ws_msgs_sent"] }, `field "smtp_sent" is not in the schema`},
		{"wrong kind", func(d map[string]any) { metricsOf(d)["ws_msgs_sent"].(map[string]any)["kind"] = "trend" }, "$.metrics.ws_msgs_sent: matches none of anyOf"},
		{"missing field", func(d map[string]any) { delete(metricsOf(d)["ws_connecting"].(map[string]any), "p95") }, "$.metrics.ws_connecting: matches none of anyOf"},
		{"extra field", func(d map[string]any) { metricsOf(d)["ws_session_failed"].(map[string]any)["passes"] = 1.0 }, "$.metrics.ws_session_failed: matches none of anyOf"},
		{"unused family", func(d map[string]any) {
			metricsOf(d)["grpc_reqs"] = map[string]any{"kind": "counter", "count": 0.0, "rate": 0.0}
		}, "$.metrics.grpc_reqs: matches none of anyOf"},
		{"threshold on an unknown metric", func(d map[string]any) {
			d["thresholds"].([]any)[0].(map[string]any)["metric"] = "smtp_sent"
		}, "$.thresholds[0].metric: matches none of anyOf"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			doc := decode(t, golden).(map[string]any)
			tt.mutate(doc)
			errs := strings.Join(validate(schema, schema, doc, "$"), "\n")
			if !strings.Contains(errs, tt.want) {
				t.Errorf("errors %q, want one containing %q", errs, tt.want)
			}
		})
	}
}
