package main

import (
	"context"
	"encoding/json"
	"go/build"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// get sends a request to a test server running newMux and returns the
// response with its body read.
func get(t *testing.T, srv *httptest.Server, method, path string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

func newServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newMux(delay))
	t.Cleanup(srv.Close)
	return srv
}

func TestAPITest(t *testing.T) {
	const delay = 20 * time.Millisecond
	srv := newServer(t, delay)

	start := time.Now()
	resp, body := get(t, srv, http.MethodGet, "/api/test")
	took := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if body != testBody || resp.ContentLength != int64(len(testBody)) {
		t.Errorf("body = %q (Content-Length %d), want %q", body, resp.ContentLength, testBody)
	}
	if !json.Valid([]byte(body)) {
		t.Errorf("body is not valid JSON: %s", body)
	}
	// Allow for coarse clocks (Windows ticks every ~0.5ms).
	if took < delay-2*time.Millisecond {
		t.Errorf("responded after %v, want at least ~%v", took, delay)
	}
}

func TestResponsesAreIdentical(t *testing.T) {
	srv := newServer(t, 0)
	first, firstBody := get(t, srv, http.MethodGet, "/api/test")
	for range 5 {
		resp, body := get(t, srv, http.MethodGet, "/api/test")
		if body != firstBody {
			t.Fatalf("body changed: %q then %q", firstBody, body)
		}
		if got, want := headerString(resp.Header), headerString(first.Header); got != want {
			t.Fatalf("headers changed:\n%s\nthen\n%s", want, got)
		}
	}
	if d := first.Header.Get("Date"); d != "" {
		t.Errorf("Date header %q present; it makes responses differ over time", d)
	}
}

func headerString(h http.Header) string {
	var sb strings.Builder
	if err := h.Write(&sb); err != nil {
		panic(err)
	}
	return sb.String()
}

func TestHealthIgnoresDelay(t *testing.T) {
	srv := newServer(t, time.Hour)
	resp, body := get(t, srv, http.MethodGet, "/health")
	if resp.StatusCode != http.StatusOK || body != healthBody {
		t.Fatalf("got %d %q, want 200 %q", resp.StatusCode, body, healthBody)
	}
}

func TestRouting(t *testing.T) {
	srv := newServer(t, 0)
	tests := []struct {
		method, path string
		want         int
	}{
		{http.MethodHead, "/api/test", http.StatusOK},
		{http.MethodPost, "/api/test", http.StatusMethodNotAllowed},
		{http.MethodPost, "/health", http.StatusMethodNotAllowed},
		{http.MethodGet, "/", http.StatusNotFound},
		{http.MethodGet, "/api/test/extra", http.StatusNotFound},
		{http.MethodGet, "/api", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			if resp, _ := get(t, srv, tt.method, tt.path); resp.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

func TestHandlerStopsWaitingWhenClientCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		jsonHandler(testBody, time.Hour).ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler kept waiting after the client cancelled")
	}
	if rec.Body.Len() != 0 {
		t.Errorf("wrote %d bytes to a cancelled request", rec.Body.Len())
	}
}

func TestServeShutsDownOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- serve(ctx, ln, newMux(0)) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("serve returned %v, want nil after shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after cancel")
	}
}

// TestImportsStandardLibraryOnly keeps the target independent of LoadTool
// (and of any other dependency) so it treats every tool the same.
func TestImportsStandardLibraryOnly(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		// Standard library paths have no dot in their first element.
		if first, _, _ := strings.Cut(imp, "/"); strings.Contains(first, ".") {
			t.Errorf("imports %q; the benchmark server must use only the standard library", imp)
		}
	}
}
