package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandlerRespondsWithBodyAfterDelay(t *testing.T) {
	const delay = 20 * time.Millisecond
	srv := httptest.NewServer(handler(delay, 128))
	t.Cleanup(srv.Close)

	start := time.Now()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if len(body) != 128 || resp.ContentLength != 128 {
		t.Errorf("body = %d bytes, Content-Length = %d, want 128", len(body), resp.ContentLength)
	}
	// Allow for coarse clocks (Windows ticks every ~0.5ms).
	if took < delay-2*time.Millisecond {
		t.Errorf("responded after %v, want at least ~%v", took, delay)
	}
}

func TestHandlerNoDelayEmptyBody(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(0, 0).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("got %d with %d bytes, want 200 with empty body", rec.Code, rec.Body.Len())
	}
}

func TestHandlerStopsWaitingWhenClientCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler(time.Hour, 2).ServeHTTP(rec, req)
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
	go func() { errc <- serve(ctx, ln, handler(0, 2)) }()

	resp, err := http.Get("http://" + ln.Addr().String())
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
