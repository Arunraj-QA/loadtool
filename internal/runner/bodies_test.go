package runner

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
)

// The memory regression of ADR-013: with large responses, a script that
// does not ask for bodies must not allocate them. Bytes allocated per
// request stay far below the body size by default, and reach it when the
// script keeps bodies (which shows the measurement can see them).
func TestDefaultDiscardsLargeBodies(t *testing.T) {
	const size = 1 << 20
	body := bytes.Repeat([]byte("x"), size)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(size))
		w.Write(body)
	}))
	t.Cleanup(srv.Close)

	for _, tt := range []struct {
		name, options string
		check         func(perRequest uint64) bool
		want          string
	}{
		{"default", `{}`, func(b uint64) bool { return b < 64<<10 }, "under 64 KiB"},
		{"kept", `{ discardResponseBodies: false }`, func(b uint64) bool { return b >= size/2 }, "at least 512 KiB"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := scriptFile(t, `import http from "loadtool/http";
export const options = `+tt.options+`;
export default function (): void { http.get("`+srv.URL+`"); }`)
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			res, err := Run(context.Background(), Params{
				Config:    config.Config{Script: path, GracefulStop: time.Second},
				Overrides: config.Overrides{VUs: intp(2), Duration: durp(300 * time.Millisecond)},
			})
			runtime.ReadMemStats(&after)
			if err != nil {
				t.Fatal(err)
			}
			n := uint64(res.Summary.Requests)
			if n < 10 || res.Summary.Failures != 0 {
				t.Fatalf("%d requests, %d failed; want at least 10, all successful", n, res.Summary.Failures)
			}
			// TotalAlloc counts the whole process, the test server and
			// the run's fixed costs included, which only makes this an
			// over-estimate.
			perRequest := (after.TotalAlloc - before.TotalAlloc) / n
			t.Logf("%d requests, %d bytes allocated per request", n, perRequest)
			if !tt.check(perRequest) {
				t.Errorf("%d bytes allocated per request, want %s", perRequest, tt.want)
			}
		})
	}
}
