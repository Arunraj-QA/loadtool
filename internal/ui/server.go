package ui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"strings"
	"time"
)

//go:embed static
var static embed.FS

// tokenHeader carries the session token on every API call.
const tokenHeader = "X-LoadTool-Token"

// maxAPIBody bounds a request to the controller.
const maxAPIBody = 1 << 20

// Server is the controller's HTTP interface.
type Server struct {
	ctl   *Controller
	token string
	// hosts are the Host values accepted, such as "127.0.0.1:7878" and
	// "localhost:7878".
	hosts map[string]bool
	mux   *http.ServeMux
}

// NewServer serves ctl for clients reaching it as one of hosts. The token
// must accompany every API call.
func NewServer(ctl *Controller, token string, hosts ...string) *Server {
	s := &Server{ctl: ctl, token: token, hosts: map[string]bool{}, mux: http.NewServeMux()}
	for _, h := range hosts {
		s.hosts[strings.ToLower(h)] = true
	}
	files, err := fs.Sub(static, "static")
	if err != nil {
		panic(err) // embedded at build time
	}
	s.mux.Handle("GET /", http.FileServerFS(files))
	s.mux.HandleFunc("POST /api/preflight", s.preflight)
	s.mux.HandleFunc("POST /api/runs", s.start)
	s.mux.HandleFunc("GET /api/runs/{id}", s.status)
	s.mux.HandleFunc("POST /api/runs/{id}/cancel", s.cancel)
	s.mux.HandleFunc("GET /api/runs/{id}/report.json", s.report)
	s.mux.HandleFunc("GET /api/runs/{id}/report.html", s.report)
	return s
}

// NewToken returns a random session token.
func NewToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; form-action 'none'; base-uri 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	// Only this machine's browser, addressed by name or loopback IP:
	// another Host is a DNS-rebinding attempt.
	if !s.hosts[strings.ToLower(r.Host)] {
		http.Error(w, "unexpected Host", http.StatusMisdirectedRequest)
		return
	}
	// A cross-site page may send requests; it cannot read or set the
	// token, and its Origin gives it away.
	if o := r.Header.Get("Origin"); o != "" && !s.hosts[strings.ToLower(strings.TrimPrefix(o, "http://"))] {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(tokenHeader)), []byte(s.token)) != 1 {
			writeError(w, http.StatusUnauthorized, "missing or wrong session token: open the URL loadtool ui printed")
			return
		}
		if r.Method == http.MethodPost {
			if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "send JSON")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxAPIBody)
		}
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) preflight(w http.ResponseWriter, r *http.Request) {
	var req APIRequest
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.ctl.opts.RequestTimeout+5*time.Second)
	defer cancel()
	pv, err := s.ctl.Preflight(ctx, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pv)
}

func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	var c RunConfig
	if !decode(w, r, &c) {
		return
	}
	id, err := s.ctl.Start(c)
	switch {
	case errors.Is(err, errBusy):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
	}
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	st, err := s.ctl.Status(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	if err := s.ctl.Cancel(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	kind, ctype := "json", "application/json"
	if strings.HasSuffix(r.URL.Path, ".html") {
		kind, ctype = "html", "text/html; charset=utf-8"
	}
	b, err := s.ctl.Report(r.PathValue("id"), kind)
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", `attachment; filename="loadtool-report.`+kind+`"`)
	w.Write(b)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
