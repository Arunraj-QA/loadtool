package runner

import (
	"context"
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Arunraj-QA/loadtool/internal/config"
	"github.com/Arunraj-QA/loadtool/internal/report"
)

// tlsH2Server is an HTTPS server with HTTP/2 answering JSON, and the TLS
// settings that trust it.
func tlsH2Server(t *testing.T) (*httptest.Server, *tls.Config) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"proto":"` + r.Proto + `"}`))
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // rejected certificates are expected
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, srv.Client().Transport.(*http.Transport).TLSClientConfig
}

func runTLS(t *testing.T, src string, tlsConfig *tls.Config) (report.Result, error) {
	t.Helper()
	return Run(context.Background(), Params{
		Config:    config.Config{Script: scriptFile(t, src), GracefulStop: time.Second},
		TLSConfig: tlsConfig,
	})
}

// A whole run over HTTPS with HTTP/2: requests, checks, thresholds and the
// protocol counts all work as over HTTP/1.1.
func TestRunOverHTTP2(t *testing.T) {
	srv, tlsConfig := tlsH2Server(t)
	for _, tt := range []struct {
		version, wantProto string
	}{
		{"auto", "HTTP/2.0"}, // negotiated through ALPN
		{"2", "HTTP/2.0"},
		{"1.1", "HTTP/1.1"},
	} {
		t.Run(tt.version, func(t *testing.T) {
			res, err := runTLS(t, `import http from "loadtool/http";
import { check } from "loadtool";
export const options = {
	vus: 4, duration: "300ms", httpVersion: "`+tt.version+`",
	thresholds: { http_req_failed: ["rate==0"], checks: ["rate==1"], http_req_duration: ["p(95)<1000"] },
};
export default function () {
	const res = http.get("`+srv.URL+`");
	check(res, {
		"protocol": (r) => r.proto === "`+tt.wantProto+`",
		"server agrees": (r) => r.json().proto === "`+tt.wantProto+`",
	});
}`, tlsConfig)
			if err != nil {
				t.Fatal(err)
			}
			s := res.Summary
			if s.Requests == 0 || s.Failures != 0 || s.ScriptErrors != 0 {
				t.Fatalf("summary %+v", s)
			}
			http2 := tt.wantProto == "HTTP/2.0"
			if http2 && s.Protocols.HTTP2 != s.Requests || !http2 && s.Protocols.HTTP1 != s.Requests {
				t.Errorf("protocols %+v for %d requests, want all %s", s.Protocols, s.Requests, tt.wantProto)
			}
			for _, c := range s.Checks {
				if c.Fails != 0 {
					t.Errorf("check %q failed %d times", c.Name, c.Fails)
				}
			}
			for _, th := range res.Thresholds {
				if !th.Passed {
					t.Errorf("threshold %s %s failed (observed %v)", th.Metric, th.Expr, th.Observed)
				}
			}
		})
	}
}

// A failing threshold over HTTP/2 fails like over HTTP/1.1.
func TestHTTP2ThresholdFailure(t *testing.T) {
	srv, tlsConfig := tlsH2Server(t)
	res, err := runTLS(t, `import http from "loadtool/http";
export const options = { vus: 2, duration: "200ms", thresholds: { http_reqs: ["count<0"] } };
export default function () { http.get("`+srv.URL+`"); }`, tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Thresholds) != 1 || res.Thresholds[0].Passed || res.Summary.Protocols.HTTP2 == 0 {
		t.Errorf("thresholds %+v, protocols %+v; want a failed threshold on an HTTP/2 run", res.Thresholds, res.Summary.Protocols)
	}
}

// Without the test server's certificate, HTTPS fails, HTTP/2 or not: the
// runner does not weaken TLS.
func TestHTTP2RunWithoutTrustFails(t *testing.T) {
	srv, _ := tlsH2Server(t)
	res, err := runTLS(t, `import http from "loadtool/http";
export const options = { vus: 1, duration: "100ms" };
export default function () {
	const res = http.get("`+srv.URL+`");
	if (!res.error.includes("certificate")) throw new Error("error: " + res.error);
}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Successes != 0 || res.Summary.ScriptErrors != 0 || res.Summary.Failures == 0 {
		t.Errorf("summary %+v, want only certificate failures", res.Summary)
	}
}
