package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Options are the run settings a script declares with
// `export const options = {...}`. A nil field was not set.
// Keys and value forms follow k6 (ADR-005, ADR-006).
type Options struct {
	VUs      *int      `json:"vus"`
	Duration *Duration `json:"duration"`
}

// knownOptions lists the keys Options understands.
var knownOptions = []string{"duration", "vus"}

// Duration accepts a duration string ("30s", "1m30s") or a number of
// milliseconds, as k6 options do.
type Duration time.Duration

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		v, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q: use a form like \"30s\" or \"1m30s\"", s)
		}
		*d = Duration(v)
		return nil
	}
	ms, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return fmt.Errorf("invalid duration %s: use a string like \"30s\" or a number of milliseconds", b)
	}
	*d = Duration(time.Duration(ms * float64(time.Millisecond)))
	return nil
}

// ParseOptions decodes the JSON of a script's options. It returns the keys
// it does not understand, sorted, so the caller can warn about them; they
// are not an error, so k6 scripts with not-yet-supported options still run.
// Empty or "null" input means no options.
func ParseOptions(raw []byte) (Options, []string, error) {
	var opts Options
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return opts, nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return opts, nil, fmt.Errorf("script options must be an object: %w", err)
	}
	var unknown []string
	for k := range fields {
		if !slices.Contains(knownOptions, k) {
			unknown = append(unknown, k)
		}
	}
	slices.Sort(unknown)

	// Decode field by field so the error names the option.
	if v, ok := fields["vus"]; ok {
		var n int
		if err := json.Unmarshal(v, &n); err != nil {
			return opts, nil, fmt.Errorf("options.vus must be a whole number, got %s", v)
		}
		opts.VUs = &n
	}
	if v, ok := fields["duration"]; ok {
		var d Duration
		if err := d.UnmarshalJSON(v); err != nil {
			return opts, nil, fmt.Errorf("options.duration: %w", err)
		}
		opts.Duration = &d
	}
	return opts, unknown, nil
}

// Overrides are settings given on the command line. A nil field was not
// typed by the user, so it does not override other sources.
type Overrides struct {
	VUs      *int
	Duration *time.Duration
}

// Environment variable names for run settings.
const (
	EnvVUs      = "LOADTOOL_VUS"
	EnvDuration = "LOADTOOL_DURATION"
)

// Defaults used when no source sets a value.
const (
	DefaultVUs      = 1
	DefaultDuration = 10 * time.Second
)

// Resolve sets c.VUs and c.Duration from, highest priority first: CLI
// flags, environment variables (looked up with getenv), script options
// and defaults (ADR-006). Each value is validated as it is chosen, so an
// error names the source the bad value came from.
func (c *Config) Resolve(cli Overrides, getenv func(string) (string, bool), script Options) error {
	vus, src := DefaultVUs, "default"
	switch {
	case cli.VUs != nil:
		vus, src = *cli.VUs, "--vus"
	case hasEnv(getenv, EnvVUs):
		s, _ := getenv(EnvVUs)
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("%s must be a whole number, got %q", EnvVUs, s)
		}
		vus, src = n, EnvVUs
	case script.VUs != nil:
		vus, src = *script.VUs, "script options"
	}
	if vus < 1 {
		return fmt.Errorf("vus must be at least 1, got %d (from %s)", vus, src)
	}

	dur, dsrc := DefaultDuration, "default"
	switch {
	case cli.Duration != nil:
		dur, dsrc = *cli.Duration, "--duration"
	case hasEnv(getenv, EnvDuration):
		s, _ := getenv(EnvDuration)
		d, err := time.ParseDuration(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("%s must be a duration like \"30s\", got %q", EnvDuration, s)
		}
		dur, dsrc = d, EnvDuration
	case script.Duration != nil:
		dur, dsrc = time.Duration(*script.Duration), "script options"
	}
	if dur <= 0 {
		return fmt.Errorf("duration must be positive, got %s (from %s)", dur, dsrc)
	}

	c.VUs, c.Duration = vus, dur
	return nil
}

func hasEnv(getenv func(string) (string, bool), name string) bool {
	if getenv == nil {
		return false
	}
	v, ok := getenv(name)
	return ok && strings.TrimSpace(v) != ""
}
