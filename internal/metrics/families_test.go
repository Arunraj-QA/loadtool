package metrics

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func mustFamilies(t testing.TB, defs ...Def) *Families {
	t.Helper()
	f, err := NewFamilies(defs)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestNewFamiliesValidates(t *testing.T) {
	tests := []struct {
		defs []Def
		want string
	}{
		{[]Def{{"Grpc_reqs", Counter}}, "must be <protocol>_<what>"},
		{[]Def{{"reqs", Counter}}, "must be <protocol>_<what>"},
		{[]Def{{"http_reqs", Counter}}, "is a built-in metric"},
		{[]Def{{"checks_x", 0}}, "unknown kind"},
		{[]Def{{"ws_sessions", Counter}, {"ws_sessions", Rate}}, "declared twice"},
	}
	for _, tt := range tests {
		if _, err := NewFamilies(tt.defs); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("NewFamilies(%v) = %v, want an error containing %q", tt.defs, err, tt.want)
		}
	}
	f := mustFamilies(t, Def{"grpc_req_duration", Trend}, Def{"grpc_reqs", Counter})
	if id, ok := f.ID("grpc_reqs"); !ok || id != 1 {
		t.Errorf("ID(grpc_reqs) = %d, %v", id, ok)
	}
	if _, ok := f.ID("nope_x"); ok {
		t.Error("ID found an undeclared family")
	}
}

func TestFamiliesRecordAndSummarize(t *testing.T) {
	f := mustFamilies(t,
		Def{"grpc_req_duration", Trend},
		Def{"grpc_reqs", Counter},
		Def{"grpc_req_failed", Rate},
		Def{"grpc_unused", Counter},
	)
	recs := NewRecorders(20) // more recorders than shards
	for _, r := range recs {
		r.UseFamilies(f)
	}
	for i, r := range recs {
		ok := i%4 != 0 // 5 of 20 fail
		r.Trend(0, time.Duration(i+1)*time.Millisecond, ok)
		r.Add(1, 2)
		r.Rate(2, ok)
	}
	s := f.Summarize()
	if len(s) != 4 || s[0].Name != "grpc_req_duration" || s[3].Name != "grpc_unused" {
		t.Fatalf("Summarize = %+v, want the four families in order", s)
	}
	trend := s[0]
	if trend.Kind != Trend || trend.Count != 20 || trend.Failed != 5 || trend.Min != time.Millisecond || trend.Max != 20*time.Millisecond {
		t.Errorf("trend = %+v", trend)
	}
	if trend.Mean != 10500*time.Microsecond {
		t.Errorf("mean = %v, want 10.5ms", trend.Mean)
	}
	if p, ok := trend.Percentile(50); !ok || p < 9900*time.Microsecond || p > 10100*time.Microsecond {
		t.Errorf("p50 = %v, %v; want about 10ms", p, ok)
	}
	if s[1].Count != 40 {
		t.Errorf("counter = %d, want 40", s[1].Count)
	}
	if s[2].Count != 20 || s[2].Trues != 15 {
		t.Errorf("rate = %d of %d, want 15 of 20", s[2].Trues, s[2].Count)
	}
	if s[3].Used() || !s[0].Used() {
		t.Error("Used: want the recorded family used and the other not")
	}
	if _, ok := s[3].Percentile(50); ok {
		t.Error("Percentile on a family without samples should report no data")
	}
}

// A recorder without families, like the throwaway one setup and teardown
// use, drops family samples, so they are never counted.
func TestRecorderWithoutFamiliesDrops(t *testing.T) {
	f := mustFamilies(t, Def{"ws_sessions", Counter})
	var r Recorder
	r.Add(0, 1)
	if s := f.Summarize(); s[0].Count != 0 {
		t.Errorf("count = %d, want 0", s[0].Count)
	}
	var nilFams *Families
	if nilFams.Summarize() != nil || nilFams.Defs() != nil {
		t.Error("a nil Families should summarize to nothing")
	}
}

func TestFamilyKindMismatchPanics(t *testing.T) {
	f := mustFamilies(t, Def{"ws_sessions", Counter})
	r := &Recorder{}
	r.UseFamilies(f)
	defer func() {
		if p := recover(); p == nil || !strings.Contains(p.(string), `"ws_sessions" is a counter, not a trend`) {
			t.Errorf("panic = %v", p)
		}
	}()
	r.Trend(0, time.Millisecond, true)
}

// Recorders on many goroutines record into shared shards without losing
// counts (run with -race).
func TestFamiliesConcurrentRecording(t *testing.T) {
	f := mustFamilies(t, Def{"kafka_produce_duration", Trend}, Def{"kafka_messages_produced", Counter}, Def{"kafka_produce_failed", Rate})
	const vus, each = 64, 500
	recs := NewRecorders(vus)
	var wg sync.WaitGroup
	for _, r := range recs {
		r.UseFamilies(f)
		wg.Go(func() {
			for j := range each {
				r.Trend(0, time.Duration(j)*time.Microsecond, true)
				r.Add(1, 1)
				r.Rate(2, j%2 == 0)
			}
		})
	}
	wg.Wait()
	s := f.Summarize()
	if s[0].Count != vus*each || s[1].Count != vus*each || s[2].Count != vus*each || s[2].Trues != vus*each/2 {
		t.Errorf("got %d, %d, %d/%d; want %d each and half true", s[0].Count, s[1].Count, s[2].Trues, s[2].Count, vus*each)
	}
}

// Recording a family allocates nothing.
func TestFamilyRecordingDoesNotAllocate(t *testing.T) {
	f := mustFamilies(t, Def{"ws_connecting", Trend}, Def{"ws_sessions", Counter}, Def{"ws_session_failed", Rate})
	r := NewRecorders(1)[0]
	r.UseFamilies(f)
	if n := testing.AllocsPerRun(1000, func() {
		r.Trend(0, time.Millisecond, true)
		r.Add(1, 1)
		r.Rate(2, true)
	}); n != 0 {
		t.Errorf("%v allocations per record, want 0", n)
	}
}

// The cost of recording one Trend sample with many VUs recording at
// once; compare BenchmarkRecorderRecordParallel for an HTTP sample.
func BenchmarkFamilyTrendParallel(b *testing.B) {
	f := mustFamilies(b, Def{"grpc_req_duration", Trend})
	recs := NewRecorders(64)
	for _, r := range recs {
		r.UseFamilies(f)
	}
	var next sync.Mutex
	i := 0
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		next.Lock()
		r := recs[i%len(recs)]
		i++
		next.Unlock()
		for pb.Next() {
			r.Trend(0, 12*time.Millisecond, true)
		}
	})
}

// BenchmarkFamilyCounterParallel is the same for a Counter.
func BenchmarkFamilyCounterParallel(b *testing.B) {
	f := mustFamilies(b, Def{"ws_msgs_sent", Counter})
	recs := NewRecorders(64)
	for _, r := range recs {
		r.UseFamilies(f)
	}
	var next sync.Mutex
	i := 0
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		next.Lock()
		r := recs[i%len(recs)]
		i++
		next.Unlock()
		for pb.Next() {
			r.Add(0, 1)
		}
	})
}
