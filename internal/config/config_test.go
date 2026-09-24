package config

import (
	"strings"
	"testing"
	"time"
)

func valid() Config {
	return Config{Script: "test.ts", URL: "http://localhost:8080/", VUs: 10, Duration: time.Second}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"valid", func(*Config) {}, ""},
		{"https", func(c *Config) { c.URL = "https://example.com" }, ""},
		{"missing script", func(c *Config) { c.Script = "" }, "script path is required"},
		{"missing url", func(c *Config) { c.URL = "" }, "url is required"},
		{"bad scheme", func(c *Config) { c.URL = "ftp://example.com" }, "scheme must be http or https"},
		{"no host", func(c *Config) { c.URL = "http://" }, "has no host"},
		{"unparsable url", func(c *Config) { c.URL = "http://[::1" }, "invalid url"},
		{"zero vus", func(c *Config) { c.VUs = 0 }, "vus must be at least 1"},
		{"zero duration", func(c *Config) { c.Duration = 0 }, "duration must be positive"},
		{"negative duration", func(c *Config) { c.Duration = -time.Second }, "duration must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := valid()
			tt.mutate(&c)
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateReportsAllErrors(t *testing.T) {
	err := Config{}.Validate()
	if err == nil {
		t.Fatal("expected error for empty config")
	}
	for _, want := range []string{"script", "url", "vus", "duration"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
