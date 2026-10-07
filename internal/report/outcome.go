package report

// Exit codes of a run that produced a result (docs/cli.md).
const (
	ExitPassed           = 0
	ExitFailed           = 1
	ExitThresholdsFailed = 99 // as in k6, so CI recipes carry over
)

// Outcome is the verdict of a run. It is the single rule behind both the
// process exit code and the JSON summary's outcome, so the two always
// agree.
type Outcome struct {
	Passed bool
	// ExitCode is ExitPassed, ExitThresholdsFailed, or ExitFailed when
	// the run was interrupted or teardown failed (which win over failed
	// thresholds: partial results are no threshold verdict).
	ExitCode int
	// Reasons say why the run did not pass, in that order; empty when it
	// passed.
	Reasons []string
}

// Verdict decides the outcome of a finished run.
func Verdict(r Result) Outcome {
	var reasons []string
	code := ExitPassed
	if r.Interrupted {
		reasons = append(reasons, "interrupted")
		code = ExitFailed
	}
	if r.TeardownError != "" {
		reasons = append(reasons, r.TeardownError)
		code = ExitFailed
	}
	for _, t := range r.Thresholds {
		if !t.Passed {
			reasons = append(reasons, "threshold failed: "+t.Metric+" "+t.Expr)
			if code == ExitPassed {
				code = ExitThresholdsFailed
			}
		}
	}
	return Outcome{Passed: code == ExitPassed, ExitCode: code, Reasons: reasons}
}
