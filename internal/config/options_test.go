package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDurationUnmarshal(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{`"30s"`, 30 * time.Second, false},
		{`"1m30s"`, 90 * time.Second, false},
		{`1500`, 1500 * time.Millisecond, false}, // numbers are milliseconds, as in k6
		{`2.5`, 2500 * time.Microsecond, false},
		{`"abc"`, 0, true},
		{`"30"`, 0, true}, // a unitless string is ambiguous
		{`true`, 0, true},
	}
	for _, tt := range tests {
		var d Duration
		err := d.UnmarshalJSON([]byte(tt.in))
		if (err != nil) != tt.wantErr {
			t.Errorf("UnmarshalJSON(%s) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && time.Duration(d) != tt.want {
			t.Errorf("UnmarshalJSON(%s) = %v, want %v", tt.in, time.Duration(d), tt.want)
		}
	}
}

func TestParseOptions(t *testing.T) {
	for _, raw := range []string{"", "null", "  "} {
		opts, unknown, err := ParseOptions([]byte(raw))
		if err != nil || opts.VUs != nil || opts.Duration != nil || unknown != nil {
			t.Errorf("ParseOptions(%q) = %+v, %v, %v; want empty", raw, opts, unknown, err)
		}
	}

	opts, unknown, err := ParseOptions([]byte(`{"vus": 25, "duration": "45s", "tags": {}, "insecureSkipTLSVerify": true}`))
	if err != nil {
		t.Fatal(err)
	}
	if opts.VUs == nil || *opts.VUs != 25 || opts.Duration == nil || time.Duration(*opts.Duration) != 45*time.Second {
		t.Fatalf("got VUs=%v Duration=%v, want 25 and 45s", opts.VUs, opts.Duration)
	}
	if want := []string{"insecureSkipTLSVerify", "tags"}; !slices.Equal(unknown, want) {
		t.Errorf("unknown = %v, want %v (sorted)", unknown, want)
	}
}

func TestParseOptionsErrors(t *testing.T) {
	tests := []struct{ raw, want string }{
		{`[1, 2]`, "must be an object"},
		{`{"vus": "ten"}`, "options.vus must be a whole number"},
		{`{"vus": 2.5}`, "options.vus must be a whole number"},
		{`{"duration": "soon"}`, "options.duration"},
		{`{"discardResponseBodies": "yes"}`, "options.discardResponseBodies must be true or false"},
	}
	for _, tt := range tests {
		if _, _, err := ParseOptions([]byte(tt.raw)); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("ParseOptions(%s) error = %v, want it to contain %q", tt.raw, err, tt.want)
		}
	}
}

func intp(n int) *int                     { return &n }
func durp(d time.Duration) *time.Duration { return &d }
func optDur(d time.Duration) *Duration    { v := Duration(d); return &v }
func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestResolvePrecedence(t *testing.T) {
	script := Options{VUs: intp(10), Duration: optDur(20 * time.Second)}
	envBoth := env(map[string]string{EnvVUs: "20", EnvDuration: "30s"})
	tests := []struct {
		name    string
		cli     Overrides
		getenv  func(string) (string, bool)
		script  Options
		wantVUs int
		wantDur time.Duration
	}{
		{"defaults", Overrides{}, nil, Options{}, DefaultVUs, DefaultDuration},
		{"script", Overrides{}, nil, script, 10, 20 * time.Second},
		{"env beats script", Overrides{}, envBoth, script, 20, 30 * time.Second},
		{"cli beats env and script", Overrides{VUs: intp(30), Duration: durp(40 * time.Second)}, envBoth, script, 30, 40 * time.Second},
		{"sources mix per setting", Overrides{VUs: intp(30)}, env(map[string]string{EnvDuration: "30s"}), script, 30, 30 * time.Second},
		{"blank env is ignored", Overrides{}, env(map[string]string{EnvVUs: "  "}), script, 10, 20 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c Config
			if err := c.Resolve(tt.cli, tt.getenv, tt.script); err != nil {
				t.Fatal(err)
			}
			if c.VUs != tt.wantVUs || c.Duration != tt.wantDur {
				t.Errorf("got VUs=%d Duration=%v, want %d and %v", c.VUs, c.Duration, tt.wantVUs, tt.wantDur)
			}
		})
	}
}

func TestResolveErrorsNameSource(t *testing.T) {
	tests := []struct {
		name   string
		cli    Overrides
		getenv func(string) (string, bool)
		script Options
		want   string
	}{
		{"cli vus", Overrides{VUs: intp(0)}, nil, Options{}, "vus must be at least 1, got 0 (from --vus)"},
		{"env vus", Overrides{}, env(map[string]string{EnvVUs: "0"}), Options{}, "(from LOADTOOL_VUS)"},
		{"env vus not a number", Overrides{}, env(map[string]string{EnvVUs: "many"}), Options{}, "LOADTOOL_VUS must be a whole number"},
		{"script vus", Overrides{}, nil, Options{VUs: intp(-1)}, "(from script options)"},
		{"cli duration", Overrides{Duration: durp(0)}, nil, Options{}, "duration must be positive, got 0s (from --duration)"},
		{"env duration", Overrides{}, env(map[string]string{EnvDuration: "later"}), Options{}, "LOADTOOL_DURATION must be a duration"},
		{"script duration", Overrides{}, nil, Options{Duration: optDur(0)}, "(from script options)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c Config
			err := c.Resolve(tt.cli, tt.getenv, tt.script)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestDiscardResponseBodies(t *testing.T) {
	for _, tt := range []struct {
		raw  string
		want bool
	}{
		{`{}`, false},
		{`{"discardResponseBodies": false}`, false},
		{`{"discardResponseBodies": true}`, true},
	} {
		opts, unknown, err := ParseOptions([]byte(tt.raw))
		if err != nil || len(unknown) != 0 {
			t.Fatalf("ParseOptions(%s) = %v, %v", tt.raw, unknown, err)
		}
		var c Config
		if err := c.Resolve(Overrides{}, nil, opts); err != nil {
			t.Fatal(err)
		}
		if c.DiscardResponseBodies != tt.want {
			t.Errorf("%s: DiscardResponseBodies = %v, want %v", tt.raw, c.DiscardResponseBodies, tt.want)
		}
	}
}

func TestLifecycleTimeouts(t *testing.T) {
	resolve := func(raw string) (Config, error) {
		opts, _, err := ParseOptions([]byte(raw))
		if err != nil {
			return Config{}, err
		}
		var c Config
		return c, c.Resolve(Overrides{}, nil, opts)
	}
	c, err := resolve(`{}`)
	if err != nil || c.SetupTimeout != DefaultLifecycleTimeout || c.TeardownTimeout != DefaultLifecycleTimeout {
		t.Fatalf("defaults: %+v, %v; want %s for both", c, err, DefaultLifecycleTimeout)
	}
	c, err = resolve(`{"setupTimeout": "2m", "teardownTimeout": 5000}`)
	if err != nil || c.SetupTimeout != 2*time.Minute || c.TeardownTimeout != 5*time.Second {
		t.Fatalf("set: %+v, %v; want 2m and 5s", c, err)
	}
	for _, raw := range []string{`{"setupTimeout": "0s"}`, `{"teardownTimeout": -1}`} {
		if _, err := resolve(raw); err == nil || !strings.Contains(err.Error(), "must be positive") {
			t.Errorf("%s: error = %v, want must be positive", raw, err)
		}
	}
	if _, err := resolve(`{"setupTimeout": "soon"}`); err == nil || !strings.Contains(err.Error(), "options.setupTimeout") {
		t.Errorf("invalid duration: error = %v", err)
	}
}

func TestThresholdOptions(t *testing.T) {
	opts, unknown, err := ParseOptions([]byte(`{"thresholds": {"http_req_duration": ["p(95)<500", "avg<200"], "checks": []}}`))
	if err != nil || len(unknown) != 0 {
		t.Fatalf("ParseOptions = %v, %v", unknown, err)
	}
	if got := opts.Thresholds["http_req_duration"]; !slices.Equal(got, []string{"p(95)<500", "avg<200"}) {
		t.Errorf("http_req_duration = %q", got)
	}
	if got, ok := opts.Thresholds["checks"]; !ok || len(got) != 0 {
		t.Errorf("checks = %q, %v; want present and empty", got, ok)
	}

	for _, tt := range []struct{ raw, want string }{
		{`{"thresholds": ["p(95)<500"]}`, "options.thresholds must be an object"},
		{`{"thresholds": {"checks": "rate>0.9"}}`, "options.thresholds.checks must be an array of expressions"},
		{`{"thresholds": {"checks": [{"threshold": "rate>0.9", "abortOnFail": true}]}}`, "object form ({ threshold, abortOnFail }) is not supported yet"},
		{`{"thresholds": {"checks": [0.9]}}`, "expressions must be strings"},
	} {
		if _, _, err := ParseOptions([]byte(tt.raw)); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("ParseOptions(%s) error = %v, want %q", tt.raw, err, tt.want)
		}
	}
}

func TestSessionOptions(t *testing.T) {
	c := resolveOK(t, `{"noCookiesReset": true, "noConnectionReuse": true}`)
	if !c.NoCookiesReset || !c.NoConnectionReuse {
		t.Errorf("NoCookiesReset=%v NoConnectionReuse=%v, want both true", c.NoCookiesReset, c.NoConnectionReuse)
	}
	if c := resolveOK(t, `{}`); c.NoCookiesReset || c.NoConnectionReuse {
		t.Error("defaults must be false")
	}
	if _, _, err := ParseOptions([]byte(`{"noCookiesReset": 1}`)); err == nil || !strings.Contains(err.Error(), "options.noCookiesReset must be true or false") {
		t.Errorf("error = %v", err)
	}
}

func TestHTTPVersionOption(t *testing.T) {
	if c := resolveOK(t, `{}`); c.HTTPVersion != "auto" {
		t.Errorf("default = %q, want auto", c.HTTPVersion)
	}
	for _, v := range []string{"auto", "1.1", "2"} {
		if c := resolveOK(t, `{"httpVersion": "`+v+`"}`); c.HTTPVersion != v {
			t.Errorf("httpVersion %s resolved to %q", v, c.HTTPVersion)
		}
	}
	for _, raw := range []string{`{"httpVersion": "3"}`, `{"httpVersion": 2}`, `{"httpVersion": "HTTP/2"}`} {
		if _, _, err := ParseOptions([]byte(raw)); err == nil || !strings.Contains(err.Error(), `options.httpVersion must be "auto", "1.1" or "2"`) {
			t.Errorf("%s: error = %v", raw, err)
		}
	}
}
