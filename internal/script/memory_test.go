package script

import (
	"context"
	"net/http"
	"runtime"
	"testing"

	"github.com/Arunraj-QA/loadtool/internal/metrics"
	"github.com/Arunraj-QA/loadtool/internal/protocol"
	"github.com/Arunraj-QA/loadtool/internal/protocols/graphql"
	"github.com/Arunraj-QA/loadtool/internal/protocols/grpc"
	"github.com/Arunraj-QA/loadtool/internal/protocols/kafka"
	"github.com/Arunraj-QA/loadtool/internal/protocols/ws"
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
	// Built-in modules and console methods are built on first use, so the
	// cost depends on what the script imports.
	httpOnly := `
import http from "loadtool/http";
const BASE_URL = "http://localhost:8080";
export default function (): void {
	let total = 0;
	for (let i = 0; i < 10; i++) total += i;
	// Referenced but never called, so esbuild keeps the import.
	if (total < 0) http.get(BASE_URL);
}`
	scripts := []struct {
		name, src string
		// mods are registered as the runner registers them; a script that
		// imports none of them should pay nothing for them.
		mods []protocol.Module
	}{
		{"no-imports", `
const BASE_URL = "http://localhost:8080";
export default function (): void {
	let total = 0;
	for (let i = 0; i < 10; i++) total += i;
}`, nil},
		{"http", httpOnly, nil},
		{"http-all-modules", httpOnly, []protocol.Module{ws.Module{}, grpc.Module{}, graphql.Module{}, kafka.Module{}}},
		{"http-sleep", `
import http from "loadtool/http";
import { sleep } from "loadtool";
const BASE_URL = "http://localhost:8080";
export default function (): void {
	let total = 0;
	for (let i = 0; i < 10; i++) total += i;
	// Referenced but never called, so esbuild keeps the imports.
	if (total < 0) {
		http.get(BASE_URL);
		sleep(1);
	}
}`, nil},
		{"http-setup-teardown", `
import http from "loadtool/http";
const BASE_URL = "http://localhost:8080";
export function setup() { return { token: "t" }; }
export default function (data: any): void {
	let total = 0;
	for (let i = 0; i < 10; i++) total += i;
	if (total < 0) http.get(BASE_URL + data.token);
}
export function teardown(data: any) {}`, nil},
	}

	for _, sc := range scripts {
		p, err := Compile("test.ts", []byte(sc.src), sc.mods...)
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
			b.Run(sc.name+"/"+tc.name, func(b *testing.B) {
				var perVU float64
				for b.Loop() {
					vus := make([]*VU, retainedVUs)
					before := liveHeap()
					for i := range vus {
						vu, err := p.NewVU(context.Background(), 1, http.DefaultClient)
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
}
