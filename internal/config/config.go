// Package config holds and validates the settings for a test run.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Config describes a single load-test run.
type Config struct {
	// Script is the path to the test script.
	Script string
	// URL is the HTTP target each iteration requests.
	URL string
	// VUs is the number of concurrent virtual users.
	VUs int
	// Duration is how long VUs keep starting new iterations.
	Duration time.Duration
}

// Validate reports every invalid field in c.
func (c Config) Validate() error {
	var errs []error
	if c.Script == "" {
		errs = append(errs, errors.New("script path is required"))
	}
	if err := validateURL(c.URL); err != nil {
		errs = append(errs, err)
	}
	if c.VUs < 1 {
		errs = append(errs, fmt.Errorf("vus must be at least 1, got %d", c.VUs))
	}
	if c.Duration <= 0 {
		errs = append(errs, fmt.Errorf("duration must be positive, got %s", c.Duration))
	}
	return errors.Join(errs...)
}

func validateURL(raw string) error {
	if raw == "" {
		return errors.New("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("url %q has no host", raw)
	}
	return nil
}
