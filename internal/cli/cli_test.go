package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func execute(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := NewRootCmd(&out, &errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestRootCommand(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantOutput string
	}{
		{name: "help", args: []string{"--help"}, wantOutput: "run"},
		{name: "version", args: []string{"--version"}, wantOutput: Version},
		{name: "run help", args: []string{"run", "--help"}, wantOutput: "run <script>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, err := execute(t, tt.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(out, tt.wantOutput) {
				t.Errorf("output %q does not contain %q", out, tt.wantOutput)
			}
		})
	}
}

func TestRunNotImplemented(t *testing.T) {
	_, _, err := execute(t, "run", "script.ts")
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("got error %v, want %v", err, ErrNotImplemented)
	}
	if !strings.Contains(err.Error(), "script.ts") {
		t.Errorf("error %q does not name the script", err)
	}
}

func TestRunArgumentValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing script", args: []string{"run"}},
		{name: "too many scripts", args: []string{"run", "a.ts", "b.ts"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := execute(t, tt.args...)
			if err == nil {
				t.Fatal("expected an argument error, got nil")
			}
			if errors.Is(err, ErrNotImplemented) {
				t.Fatal("arguments should be validated before running")
			}
		})
	}
}

func TestUnknownCommand(t *testing.T) {
	if _, _, err := execute(t, "bogus"); err == nil {
		t.Fatal("expected error for unknown command")
	}
}
