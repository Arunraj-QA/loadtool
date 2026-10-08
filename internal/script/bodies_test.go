package script

import (
	"bytes"
	"context"
	"net/http"
	"runtime"
	"strconv"
	"testing"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// The memory regression of ADR-013: a discarded body must not be
// allocated. Bytes allocated per iteration of an existing VU stay far below
// the body size when bodies are discarded, and reach it when they are kept
// (which shows the measurement can see them). Measuring per iteration,
// after warm-up, leaves out the fixed costs of compiling the script and
// creating the VU, which made a per-run average depend on how many
// requests a slow (race-enabled) run managed.
func TestDiscardedBodiesAreNotAllocated(t *testing.T) {
	const size = 1 << 20
	body := bytes.Repeat([]byte("x"), size)
	src, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(size))
		w.Write(body)
	}, `import http from "loadtool/http";
export default function () { http.get("BASE_URL"); }`)

	for _, tt := range []struct {
		name    string
		discard bool
		check   func(perIteration uint64) bool
		want    string
	}{
		{"discarded", true, func(b uint64) bool { return b < 64<<10 }, "under 64 KiB"},
		{"kept", false, func(b uint64) bool { return b >= size/2 }, "at least 512 KiB"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			vu := newVU(t, compile(t, "test.ts", src).WithDiscardResponseBodies(tt.discard))
			// One recorder for every iteration, as a VU has in a run: a
			// recorder allocates its histogram shard on first use.
			rec := &metrics.Recorder{}
			for range 3 { // warm up: connection, shard, lazy module objects
				vu.Iterate(context.Background(), rec)
			}
			if s := metrics.Merge([]*metrics.Recorder{rec}); s.Failures != 0 || s.ScriptErrors != 0 {
				t.Fatalf("warm-up failed: %+v", s)
			}
			const n = 20
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			for range n {
				vu.Iterate(context.Background(), rec)
			}
			runtime.ReadMemStats(&after)
			// TotalAlloc counts the whole process, the test server
			// included, which only makes this an over-estimate.
			perIteration := (after.TotalAlloc - before.TotalAlloc) / n
			t.Logf("%d bytes allocated per iteration", perIteration)
			if !tt.check(perIteration) {
				t.Errorf("%d bytes allocated per iteration, want %s", perIteration, tt.want)
			}
		})
	}
}
