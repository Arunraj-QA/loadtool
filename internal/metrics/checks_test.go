package metrics

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestChecksMergeInFirstRunOrder(t *testing.T) {
	recs := NewRecorders(2)
	// VU 1 runs "b" first, then VU 0 runs "a": the run's first-seen order
	// is b, a, even though recorder 0 comes first.
	recs[1].RecordCheck("b", true, "")
	recs[0].RecordCheck("a", false, "boom")
	recs[0].RecordCheck("b", false, "")
	recs[1].RecordCheck("a", true, "")
	recs[1].RecordCheck("a", false, "later")

	got := Merge(recs).Checks
	want := []CheckResult{
		{Name: "b", Passes: 1, Fails: 1},
		{Name: "a", Passes: 1, Fails: 2, FirstError: "boom"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Checks = %+v, want %+v", got, want)
	}
}

func TestChecksWithPrivateNameTables(t *testing.T) {
	var a, b Recorder // zero values: each has its own name table
	a.RecordCheck("x", true, "")
	b.RecordCheck("y", true, "")
	b.RecordCheck("x", false, "")

	got := Merge([]*Recorder{&a, &b}).Checks
	want := []CheckResult{{Name: "x", Passes: 1, Fails: 1}, {Name: "y", Passes: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Checks = %+v, want %+v", got, want)
	}
}

func TestNoChecks(t *testing.T) {
	if got := Merge(NewRecorders(3)).Checks; got != nil {
		t.Errorf("Checks = %+v, want nil", got)
	}
}

// Recorders of one run register names concurrently (run with -race).
func TestConcurrentCheckRecording(t *testing.T) {
	const vus, perVU = 8, 1000
	recs := NewRecorders(vus)
	var wg sync.WaitGroup
	for _, r := range recs {
		wg.Go(func() {
			for i := range perVU {
				r.RecordCheck(fmt.Sprint("check-", i%5), i%2 == 0, "")
			}
		})
	}
	wg.Wait()

	checks := Merge(recs).Checks
	if len(checks) != 5 {
		t.Fatalf("got %d checks, want 5", len(checks))
	}
	total := 0
	for _, c := range checks {
		total += c.Passes + c.Fails
	}
	if total != vus*perVU {
		t.Errorf("total = %d, want %d", total, vus*perVU)
	}
}

func BenchmarkRecordCheck(b *testing.B) {
	r := NewRecorders(1)[0]
	r.RecordCheck("status is 200", true, "") // first use registers the name
	b.ReportAllocs()
	for b.Loop() {
		r.RecordCheck("status is 200", true, "")
	}
}
