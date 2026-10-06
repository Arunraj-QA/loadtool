package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Executor names (ADR-008).
const (
	ConstantVUs         = "constant-vus"
	RampingVUs          = "ramping-vus"
	ConstantArrivalRate = "constant-arrival-rate"
)

// DefaultScenario names the scenario built from vus/duration or stages.
const DefaultScenario = "default"

// Defaults for scenario fields, as in k6.
const (
	DefaultGracefulStop     = 30 * time.Second
	DefaultGracefulRampDown = 30 * time.Second
	DefaultTimeUnit         = time.Second
)

// Scenario is one resolved, validated workload. Only the fields of its
// Executor are set.
type Scenario struct {
	Name     string
	Executor string
	// Exec is the exported function each iteration calls.
	Exec         string
	StartTime    time.Duration
	GracefulStop time.Duration

	// constant-vus
	VUs      int
	Duration time.Duration
	// ramping-vus
	StartVUs         int
	Stages           []Stage
	GracefulRampDown time.Duration
	// constant-arrival-rate (Duration is shared with constant-vus)
	Rate            int
	TimeUnit        time.Duration
	PreAllocatedVUs int
}

// Stage is one ramping-vus step: move to Target VUs over Duration.
type Stage struct {
	Duration time.Duration
	Target   int
}

// MaxVUs is how many VUs the scenario needs.
func (s Scenario) MaxVUs() int {
	switch s.Executor {
	case RampingVUs:
		n := s.StartVUs
		for _, st := range s.Stages {
			n = max(n, st.Target)
		}
		return n
	case ConstantArrivalRate:
		return s.PreAllocatedVUs
	default:
		return s.VUs
	}
}

// Length is how long iterations may start, from the scenario's start.
func (s Scenario) Length() time.Duration {
	if s.Executor != RampingVUs {
		return s.Duration
	}
	var d time.Duration
	for _, st := range s.Stages {
		d += st.Duration
	}
	return d
}

// ScenarioOptions is one entry of options.scenarios as the script wrote
// it. A nil field was not set.
type ScenarioOptions struct {
	Executor     string    `json:"executor"`
	Exec         *string   `json:"exec"`
	StartTime    *Duration `json:"startTime"`
	GracefulStop *Duration `json:"gracefulStop"`

	VUs      *int      `json:"vus"`
	Duration *Duration `json:"duration"`

	StartVUs         *int           `json:"startVUs"`
	Stages           []StageOptions `json:"stages"`
	GracefulRampDown *Duration      `json:"gracefulRampDown"`

	Rate            *int      `json:"rate"`
	TimeUnit        *Duration `json:"timeUnit"`
	PreAllocatedVUs *int      `json:"preAllocatedVUs"`
	MaxVUs          *int      `json:"maxVUs"`
}

// StageOptions is one stage as the script wrote it.
type StageOptions struct {
	Duration Duration `json:"duration"`
	Target   int      `json:"target"`
}

// knownScenarioKeys lists the keys ScenarioOptions understands.
var knownScenarioKeys = []string{"duration", "exec", "executor", "gracefulRampDown", "gracefulStop",
	"maxVUs", "preAllocatedVUs", "rate", "stages", "startTime", "startVUs", "timeUnit", "vus"}

// parseScenarios decodes options.scenarios. Keys it does not understand
// are returned as "scenarios.<name>.<key>" so the caller can warn.
func parseScenarios(raw json.RawMessage) (map[string]ScenarioOptions, []string, error) {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil || entries == nil {
		return nil, nil, fmt.Errorf("options.scenarios must be an object of named scenarios, got %s", raw)
	}
	out := make(map[string]ScenarioOptions, len(entries))
	var unknown []string
	for name, v := range entries {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(v, &fields); err != nil || fields == nil {
			return nil, nil, fmt.Errorf("options.scenarios.%s must be an object, got %s", name, v)
		}
		for k := range fields {
			if !slices.Contains(knownScenarioKeys, k) {
				unknown = append(unknown, "scenarios."+name+"."+k)
			}
		}
		var so ScenarioOptions
		dec := json.NewDecoder(bytes.NewReader(v))
		if err := dec.Decode(&so); err != nil {
			return nil, nil, fmt.Errorf("options.scenarios.%s: %w", name, err)
		}
		out[name] = so
	}
	slices.Sort(unknown)
	return out, unknown, nil
}

func parseStages(raw json.RawMessage) ([]StageOptions, error) {
	var stages []StageOptions
	if err := json.Unmarshal(raw, &stages); err != nil {
		return nil, fmt.Errorf(`options.stages must be an array like [{ duration: "30s", target: 10 }]: %w`, err)
	}
	return stages, nil
}

// resolveScenarios applies ADR-008's rules after vus and duration were
// resolved (ADR-006). It returns the scenarios and whether a typed flag
// or environment variable replaced the script's scenarios or stages.
func resolveScenarios(script Options, override bool, vus int, dur, gracefulStop time.Duration) ([]Scenario, bool, error) {
	shorthand := func() []Scenario {
		return []Scenario{{Name: DefaultScenario, Executor: ConstantVUs, Exec: "default",
			GracefulStop: gracefulStop, VUs: vus, Duration: dur}}
	}
	switch {
	case script.Scenarios != nil:
		if script.VUs != nil || script.Duration != nil || script.Stages != nil {
			return nil, false, errors.New("options: use either scenarios or the vus/duration/stages shorthand, not both")
		}
		if override {
			return shorthand(), true, nil
		}
		if len(script.Scenarios) == 0 {
			return nil, false, errors.New("options.scenarios is empty; define at least one scenario")
		}
		var out []Scenario
		var errs []error
		for _, name := range slices.Sorted(maps.Keys(script.Scenarios)) {
			s, err := resolveScenario(name, script.Scenarios[name])
			if err != nil {
				errs = append(errs, fmt.Errorf("options.scenarios.%s: %w", name, err))
				continue
			}
			out = append(out, s)
		}
		return out, false, errors.Join(errs...)
	case script.Stages != nil:
		if script.Duration != nil {
			return nil, false, errors.New("options: stages and duration cannot be combined; the stages set the duration")
		}
		if override {
			return shorthand(), true, nil
		}
		s, err := resolveScenario(DefaultScenario, ScenarioOptions{
			Executor: RampingVUs, StartVUs: &vus, Stages: script.Stages,
			GracefulStop: (*Duration)(&gracefulStop),
		})
		if err != nil {
			return nil, false, fmt.Errorf("options.stages: %w", err)
		}
		return []Scenario{s}, false, nil
	default:
		return shorthand(), false, nil
	}
}

var identifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// resolveScenario validates one scenario and fills in defaults.
func resolveScenario(name string, o ScenarioOptions) (Scenario, error) {
	s := Scenario{Name: name, Executor: o.Executor, Exec: "default", GracefulStop: DefaultGracefulStop}
	if o.Exec != nil {
		if !identifier.MatchString(*o.Exec) {
			return Scenario{}, fmt.Errorf("exec %q is not a function name", *o.Exec)
		}
		s.Exec = *o.Exec
	}
	if o.StartTime != nil {
		s.StartTime = time.Duration(*o.StartTime)
	}
	if o.GracefulStop != nil {
		s.GracefulStop = time.Duration(*o.GracefulStop)
	}
	if s.StartTime < 0 || s.GracefulStop < 0 {
		return Scenario{}, errors.New("startTime and gracefulStop must not be negative")
	}

	// Fields that belong to another executor are errors, not silently
	// ignored: a rate on a constant-vus scenario is almost surely a
	// mistake.
	set := map[string]bool{
		"vus": o.VUs != nil, "duration": o.Duration != nil,
		"startVUs": o.StartVUs != nil, "stages": o.Stages != nil, "gracefulRampDown": o.GracefulRampDown != nil,
		"rate": o.Rate != nil, "timeUnit": o.TimeUnit != nil, "preAllocatedVUs": o.PreAllocatedVUs != nil, "maxVUs": o.MaxVUs != nil,
	}
	allowed := map[string][]string{
		ConstantVUs:         {"vus", "duration"},
		RampingVUs:          {"startVUs", "stages", "gracefulRampDown"},
		ConstantArrivalRate: {"rate", "timeUnit", "duration", "preAllocatedVUs", "maxVUs"},
	}
	fields, ok := allowed[o.Executor]
	if !ok {
		if o.Executor == "" {
			return Scenario{}, fmt.Errorf("executor is required: one of %s, %s, %s", ConstantVUs, RampingVUs, ConstantArrivalRate)
		}
		return Scenario{}, fmt.Errorf("executor %q is not supported; use %s, %s or %s", o.Executor, ConstantVUs, RampingVUs, ConstantArrivalRate)
	}
	for _, f := range slices.Sorted(maps.Keys(set)) {
		if set[f] && !slices.Contains(fields, f) {
			return Scenario{}, fmt.Errorf("%s does not apply to the %s executor (it uses %s)", f, o.Executor, strings.Join(fields, ", "))
		}
	}

	switch o.Executor {
	case ConstantVUs:
		s.VUs = 1
		if o.VUs != nil {
			s.VUs = *o.VUs
		}
		if s.VUs < 1 {
			return Scenario{}, fmt.Errorf("vus must be at least 1, got %d", s.VUs)
		}
		if o.Duration == nil || *o.Duration <= 0 {
			return Scenario{}, errors.New("duration is required and must be positive")
		}
		s.Duration = time.Duration(*o.Duration)
	case RampingVUs:
		s.StartVUs, s.GracefulRampDown = 1, DefaultGracefulRampDown
		if o.StartVUs != nil {
			s.StartVUs = *o.StartVUs
		}
		if o.GracefulRampDown != nil {
			s.GracefulRampDown = time.Duration(*o.GracefulRampDown)
		}
		if s.StartVUs < 0 || s.GracefulRampDown < 0 {
			return Scenario{}, errors.New("startVUs and gracefulRampDown must not be negative")
		}
		if len(o.Stages) == 0 {
			return Scenario{}, errors.New(`stages is required, like [{ duration: "30s", target: 10 }]`)
		}
		for i, st := range o.Stages {
			if st.Duration < 0 || st.Target < 0 {
				return Scenario{}, fmt.Errorf("stage %d: duration and target must not be negative", i+1)
			}
			s.Stages = append(s.Stages, Stage{Duration: time.Duration(st.Duration), Target: st.Target})
		}
		if s.Length() <= 0 {
			return Scenario{}, errors.New("the stages must last longer than 0s")
		}
		if s.MaxVUs() < 1 {
			return Scenario{}, errors.New("startVUs or a stage target must be at least 1")
		}
	case ConstantArrivalRate:
		if o.Rate == nil || *o.Rate < 1 {
			return Scenario{}, errors.New("rate is required and must be at least 1")
		}
		if o.Duration == nil || *o.Duration <= 0 {
			return Scenario{}, errors.New("duration is required and must be positive")
		}
		if o.PreAllocatedVUs == nil || *o.PreAllocatedVUs < 1 {
			return Scenario{}, errors.New("preAllocatedVUs is required and must be at least 1")
		}
		s.Rate, s.Duration, s.PreAllocatedVUs = *o.Rate, time.Duration(*o.Duration), *o.PreAllocatedVUs
		s.TimeUnit = DefaultTimeUnit
		if o.TimeUnit != nil {
			s.TimeUnit = time.Duration(*o.TimeUnit)
		}
		if s.TimeUnit <= 0 {
			return Scenario{}, errors.New("timeUnit must be positive")
		}
		if o.MaxVUs != nil && *o.MaxVUs != s.PreAllocatedVUs {
			return Scenario{}, fmt.Errorf("maxVUs (%d) different from preAllocatedVUs (%d) is not supported yet: "+
				"all VUs are created before the test starts, so set preAllocatedVUs to the number of VUs needed", *o.MaxVUs, s.PreAllocatedVUs)
		}
	}
	return s, nil
}

// Describe is a one-line summary for the console report.
func (s Scenario) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", s.Name, s.Executor)
	switch s.Executor {
	case ConstantVUs:
		fmt.Fprintf(&b, ", %d VUs for %s", s.VUs, s.Duration)
	case RampingVUs:
		fmt.Fprintf(&b, ", up to %d VUs over %s", s.MaxVUs(), s.Length())
	case ConstantArrivalRate:
		fmt.Fprintf(&b, ", %d iterations per %s for %s, %d VUs", s.Rate, s.TimeUnit, s.Duration, s.PreAllocatedVUs)
	}
	if s.StartTime > 0 {
		fmt.Fprintf(&b, ", starting at %s", s.StartTime)
	}
	if s.Exec != "default" {
		fmt.Fprintf(&b, ", exec %s", s.Exec)
	}
	return b.String()
}
