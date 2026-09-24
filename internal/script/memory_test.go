package script

import (
	"context"
	"net/http"
	"runtime"
	"testing"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
)

// retainedVUs is how many VUs the retained-memory benchmark creates; large
// enough that per-VU numbers are not dominated by measurement noise.
const retainedVUs = 1000

// liveHeap returns the live heap in bytes after a full collection.
func liveHeap() uint64 {
	runtime.GC()
	runtime.GC() // second cycle so finalizer-held memory is released too
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// BenchmarkVURetainedMemory reports heap bytes each VU keeps alive
// (retained-B/VU), unlike BenchmarkNewVU which reports bytes allocated.
// It excludes goroutine stacks and HTTP connections, which belong to the
// engine and the shared client rather than the VU's runtime.
func BenchmarkVURetainedMemory(b *testing.B) {
	p, err := Compile("test.ts", []byte(`
const BASE_URL = "http://localhost:8080";
export default function (): void {
	let total = 0;
	for (let i = 0; i < 10; i++) total += i;
}`))
	if err != nil {
		b.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		iterate bool
	}{
		{"after-init", false},
		{"after-first-iteration", true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			var perVU float64
			for b.Loop() {
				vus := make([]*VU, retainedVUs)
				before := liveHeap()
				for i := range vus {
					vu, err := p.NewVU(http.DefaultClient)
					if err != nil {
						b.Fatal(err)
					}
					vus[i] = vu
				}
				if tc.iterate {
					rec := &metrics.Recorder{}
					for _, vu := range vus {
						vu.Iterate(context.Background(), rec)
					}
				}
				after := liveHeap()
				runtime.KeepAlive(vus)
				perVU = float64(after-before) / retainedVUs
			}
			b.ReportMetric(perVU, "retained-B/VU")
		})
	}
}
