package config

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// resolveRaw parses options JSON and resolves them as the runner does.
func resolveRaw(t *testing.T, raw string, cli Overrides, env map[string]string) (Config, []string, error) {
	t.Helper()
	opts, unknown, err := ParseOptions([]byte(raw))
	if err != nil {
		return Config{}, unknown, err
	}
	c := Config{GracefulStop: 30 * time.Second}
	return c, unknown, c.Resolve(cli, envOf(env), opts)
}

func envOf(m map[string]string) func(string) (string, bool) {
	if m == nil {
		return nil
	}
	return env(m)
}

func resolveOK(t *testing.T, raw string) Config {
	t.Helper()
	opts, _, err := ParseOptions([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := Config{GracefulStop: 30 * time.Second}
	if err := c.Resolve(Overrides{}, nil, opts); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestShorthandIsOneConstantScenario(t *testing.T) {
	c := resolveOK(t, `{"vus": 5, "duration": "20s"}`)
	want := []Scenario{{Name: "default", Executor: ConstantVUs, Exec: "default", GracefulStop: 30 * time.Second, VUs: 5, Duration: 20 * time.Second}}
	if !reflect.DeepEqual(c.Scenarios, want) || c.VUs != 5 || c.Duration != 20*time.Second || c.ScenariosReplaced {
		t.Errorf("got %+v VUs=%d Duration=%v replaced=%v", c.Scenarios, c.VUs, c.Duration, c.ScenariosReplaced)
	}
}

func TestStagesShorthand(t *testing.T) {
	c := resolveOK(t, `{"vus": 2, "stages": [{"duration": "10s", "target": 8}, {"duration": "5s", "target": 0}]}`)
	s := c.Scenarios[0]
	if s.Executor != RampingVUs || s.StartVUs != 2 || len(s.Stages) != 2 || s.GracefulRampDown != DefaultGracefulRampDown {
		t.Errorf("scenario = %+v", s)
	}
	if c.VUs != 8 || c.Duration != 15*time.Second {
		t.Errorf("VUs=%d Duration=%v, want 8 and 15s", c.VUs, c.Duration)
	}
}

func TestScenariosResolve(t *testing.T) {
	c := resolveOK(t, `{"scenarios": {
		"browse": {"executor": "ramping-vus", "stages": [{"duration": "30s", "target": 50}], "gracefulRampDown": "5s"},
		"orders": {"executor": "constant-arrival-rate", "rate": 20, "duration": "1m", "preAllocatedVUs": 30, "maxVUs": 30, "exec": "placeOrder", "startTime": "10s"},
		"smoke":  {"executor": "constant-vus", "duration": "5s", "gracefulStop": "0s"}
	}}`)
	want := []Scenario{
		{Name: "browse", Executor: RampingVUs, Exec: "default", GracefulStop: 30 * time.Second,
			StartVUs: 1, Stages: []Stage{{30 * time.Second, 50}}, GracefulRampDown: 5 * time.Second},
		{Name: "orders", Executor: ConstantArrivalRate, Exec: "placeOrder", StartTime: 10 * time.Second, GracefulStop: 30 * time.Second,
			Rate: 20, TimeUnit: time.Second, Duration: time.Minute, PreAllocatedVUs: 30},
		{Name: "smoke", Executor: ConstantVUs, Exec: "default", VUs: 1, Duration: 5 * time.Second},
	}
	if !reflect.DeepEqual(c.Scenarios, want) {
		t.Errorf("Scenarios =\n%+v\nwant\n%+v", c.Scenarios, want)
	}
	// 50 + 30 + 1 VUs; orders ends last, at 10s + 1m.
	if c.VUs != 81 || c.Duration != 70*time.Second {
		t.Errorf("VUs=%d Duration=%v, want 81 and 1m10s", c.VUs, c.Duration)
	}
}

// A typed flag or environment variable replaces the script's scenarios
// (ADR-006 precedence), and Config says so.
func TestOverrideReplacesScenarios(t *testing.T) {
	raw := `{"scenarios": {"a": {"executor": "constant-vus", "vus": 9, "duration": "1m"}}}`
	for _, tt := range []struct {
		name string
		cli  Overrides
		env  map[string]string
	}{
		{"--vus", Overrides{VUs: intp(3)}, nil},
		{"--duration", Overrides{Duration: durp(5 * time.Second)}, nil},
		{"LOADTOOL_VUS", Overrides{}, map[string]string{EnvVUs: "3"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _, err := resolveRaw(t, raw, tt.cli, tt.env)
			if err != nil {
				t.Fatal(err)
			}
			if !c.ScenariosReplaced || len(c.Scenarios) != 1 || c.Scenarios[0].Name != "default" || c.Scenarios[0].Executor != ConstantVUs {
				t.Errorf("Scenarios = %+v replaced=%v", c.Scenarios, c.ScenariosReplaced)
			}
		})
	}
	c, _, err := resolveRaw(t, `{"stages": [{"duration": "10s", "target": 5}]}`, Overrides{VUs: intp(2)}, nil)
	if err != nil || !c.ScenariosReplaced || c.Scenarios[0].VUs != 2 {
		t.Errorf("stages + --vus: %+v, %v", c.Scenarios, err)
	}
}

func TestScenarioErrors(t *testing.T) {
	tests := []struct{ raw, want string }{
		{`{"vus": 2, "scenarios": {"a": {"executor": "constant-vus", "duration": "1s"}}}`, "either scenarios or the vus/duration/stages shorthand"},
		{`{"duration": "10s", "stages": [{"duration": "5s", "target": 1}]}`, "stages and duration cannot be combined"},
		{`{"scenarios": {}}`, "options.scenarios is empty"},
		{`{"scenarios": []}`, "options.scenarios must be an object"},
		{`{"scenarios": {"a": {}}}`, "options.scenarios.a: executor is required"},
		{`{"scenarios": {"a": {"executor": "per-vu-iterations"}}}`, `executor "per-vu-iterations" is not supported`},
		{`{"scenarios": {"a": {"executor": "constant-vus", "duration": "1s", "rate": 5}}}`, "rate does not apply to the constant-vus executor (it uses vus, duration)"},
		{`{"scenarios": {"a": {"executor": "constant-vus"}}}`, "duration is required"},
		{`{"scenarios": {"a": {"executor": "constant-vus", "duration": "1s", "vus": 0}}}`, "vus must be at least 1"},
		{`{"scenarios": {"a": {"executor": "ramping-vus"}}}`, "stages is required"},
		{`{"scenarios": {"a": {"executor": "ramping-vus", "startVUs": 0, "stages": [{"duration": "1s", "target": 0}]}}}`, "a stage target must be at least 1"},
		{`{"scenarios": {"a": {"executor": "ramping-vus", "stages": [{"duration": "0s", "target": 3}]}}}`, "must last longer than 0s"},
		{`{"scenarios": {"a": {"executor": "constant-arrival-rate", "duration": "1s", "preAllocatedVUs": 1}}}`, "rate is required"},
		{`{"scenarios": {"a": {"executor": "constant-arrival-rate", "rate": 5, "duration": "1s"}}}`, "preAllocatedVUs is required"},
		{`{"scenarios": {"a": {"executor": "constant-arrival-rate", "rate": 5, "duration": "1s", "preAllocatedVUs": 2, "maxVUs": 10}}}`, "maxVUs (10) different from preAllocatedVUs (2) is not supported yet"},
		{`{"scenarios": {"a": {"executor": "constant-vus", "duration": "1s", "exec": "do-it"}}}`, `exec "do-it" is not a function name`},
		{`{"scenarios": {"a": {"executor": "constant-vus", "duration": "1s", "startTime": "-1s"}}}`, "must not be negative"},
		{`{"scenarios": {"a": {"executor": "constant-vus", "duration": "soon"}}}`, "options.scenarios.a"},
		{`{"stages": {"duration": "1s"}}`, "options.stages must be an array"},
	}
	for _, tt := range tests {
		if _, _, err := resolveRaw(t, tt.raw, Overrides{}, nil); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s:\n  error = %v\n  want it to contain %q", tt.raw, err, tt.want)
		}
	}
}

func TestUnknownScenarioKeysWarn(t *testing.T) {
	_, unknown, err := resolveRaw(t, `{"scenarios": {"a": {"executor": "constant-vus", "duration": "1s", "tags": {"x": "y"}}}}`, Overrides{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(unknown, []string{"scenarios.a.tags"}) {
		t.Errorf("unknown = %q", unknown)
	}
}

func TestDescribe(t *testing.T) {
	c := resolveOK(t, `{"scenarios": {
		"a": {"executor": "constant-vus", "vus": 3, "duration": "10s"},
		"b": {"executor": "ramping-vus", "stages": [{"duration": "20s", "target": 7}], "startTime": "5s"},
		"c": {"executor": "constant-arrival-rate", "rate": 50, "timeUnit": "1m", "duration": "2m", "preAllocatedVUs": 4, "exec": "api"}
	}}`)
	var got []string
	for _, s := range c.Scenarios {
		got = append(got, s.Describe())
	}
	want := []string{
		"a: constant-vus, 3 VUs for 10s",
		"b: ramping-vus, up to 7 VUs over 20s, starting at 5s",
		"c: constant-arrival-rate, 50 iterations per 1m0s for 2m0s, 4 VUs, exec api",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Describe =\n%q\nwant\n%q", got, want)
	}
}
