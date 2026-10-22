package protocol

import (
	"time"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// NoFamily marks an absent family in Families.
const NoFamily metrics.FamilyID = -1

// Families names the metric families one kind of operation records
// (ADR-018 §8): a Trend of durations, a Counter of operations and a Rate
// of failures. Any may be NoFamily.
type Families struct {
	Duration, Count, Failed metrics.FamilyID
}

// Outcome is one finished operation.
type Outcome struct {
	Duration time.Duration
	// Err is nil on success.
	Err error
	// Code is set by the module for its own errors (CodeServer); when it
	// is empty and Err is set, Record classifies Err.
	Code ErrorCode
	// Sent is false when the operation never left LoadTool (an invalid
	// request): it has no duration.
	Sent bool
}

// Record records out in f and returns its error code, for the result's
// error_code. Nothing is recorded when the VU's context has ended, so
// operations cut short by the end of the test are not counted (as
// httpclient.Do). It must be called on the VU's goroutine.
func Record(vu VU, f Families, out Outcome) ErrorCode {
	code := out.Code
	if code == CodeNone && out.Err != nil {
		code = Classify(out.Err)
		if !out.Sent {
			code = CodeInvalid
		}
	}
	ctx, rec := vu.Context(), vu.Recorder()
	if ctx == nil || ctx.Err() != nil || rec == nil {
		return code
	}
	failed := code != CodeNone
	if f.Duration != NoFamily && out.Sent {
		rec.Trend(f.Duration, out.Duration, !failed)
	}
	if f.Count != NoFamily {
		rec.Add(f.Count, 1)
	}
	if f.Failed != NoFamily {
		rec.Rate(f.Failed, failed)
	}
	return code
}

// FamilyIDs looks up family IDs by name in env.Families. A module calls it
// in NewRun with the names it declared in Metrics.
func FamilyIDs(env RunEnv, names ...string) ([]metrics.FamilyID, error) {
	ids := make([]metrics.FamilyID, len(names))
	for i, n := range names {
		id, ok := env.Families.ID(n)
		if !ok {
			return nil, &missingFamilyError{name: n}
		}
		ids[i] = id
	}
	return ids, nil
}

type missingFamilyError struct{ name string }

func (e *missingFamilyError) Error() string {
	return "protocol: metric family " + e.name + " is not declared in the run's families"
}
