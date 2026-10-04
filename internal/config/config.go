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
	// VUs is the number of concurrent virtual users.
	VUs int
	// Duration is how long VUs keep starting new iterations.
	Duration time.Duration
	// GracefulStop is how long iterations still running when Duration ends
	// may take to finish before they are cancelled.
	GracefulStop time.Duration
	// DiscardResponseBodies drops response bodies instead of handing them
	// to the script.
	DiscardResponseBodies bool
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
