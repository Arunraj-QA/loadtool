// Package config holds and validates the settings for a test run.
package config

import (
	"errors"
	"fmt"
	"time"
)

// Config describes a single load-test run.
type Config struct {
	// Script is the path to the test script.
	Script string
	// VUs is the total number of virtual users across all scenarios.
	VUs int
	// Duration is how long, from the start of the test, iterations keep
	// starting: the end of the last scenario.
	Duration time.Duration
	// GracefulStop is how long iterations still running when Duration ends
	// may take to finish before they are cancelled.
	GracefulStop time.Duration
	// DiscardResponseBodies drops response bodies instead of handing them
	// to the script.
	DiscardResponseBodies bool
	// SetupTimeout and TeardownTimeout bound the script's setup() and
	// teardown().
	SetupTimeout, TeardownTimeout time.Duration
	// Scenarios are the resolved workloads; one constant-vus scenario
	// when the script uses the vus/duration shorthand.
	Scenarios []Scenario
	// ScenariosReplaced reports that a typed --vus/--duration (or
	// LOADTOOL_VUS/LOADTOOL_DURATION) replaced the script's scenarios or
	// stages with one constant-vus scenario.
	ScenariosReplaced bool
}

// Validate reports every invalid field in c.
func (c Config) Validate() error {
	var errs []error
	if c.Script == "" {
		errs = append(errs, errors.New("script path is required"))
	}
	if c.VUs < 1 {
		errs = append(errs, fmt.Errorf("vus must be at least 1, got %d", c.VUs))
	}
	if c.Duration <= 0 {
		errs = append(errs, fmt.Errorf("duration must be positive, got %s", c.Duration))
	}
	if c.GracefulStop < 0 {
		errs = append(errs, fmt.Errorf("graceful-stop must not be negative, got %s", c.GracefulStop))
	}
	return errors.Join(errs...)
}
