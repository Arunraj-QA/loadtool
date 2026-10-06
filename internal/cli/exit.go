package cli

import "errors"

// ExitThresholdsFailed is the exit code when the test ran fully but at
// least one threshold failed. k6 uses the same code, so CI recipes carry
// over (ADR-008).
const ExitThresholdsFailed = 99

// ExitError is an error that asks for a specific process exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode returns the process exit code for err: 0 for nil, the code of
// an ExitError, and 1 for any other error.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := errors.AsType[*ExitError](err); ok {
		return e.Code
	}
	return 1
}
