package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newAPI(0, newSigner()).routes())
	t.Cleanup(srv.Close)
	return srv
}

// do sends a request and returns the status and decoded JSON body.
func do(t *testing.T, c *http.Client, method, url, body string, header ...string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var v map[string]any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("%s %s: body %q is not a JSON object: %v", method, url, b, err)
		}
	}
	return res.StatusCode, v
}

func TestProducts(t *testing.T) {
	srv := newTestServer(t)
	c := srv.Client()
	if code, v := do(t, c, "GET", srv.URL+"/api/products", ""); code != 200 || len(v["products"].([]any)) != len(products) {
		t.Errorf("list: %d %v", code, v)
	}
	if code, v := do(t, c, "GET", srv.URL+"/api/products/3", ""); code != 200 || v["name"] != "Ceramic cup" {
		t.Errorf("product 3: %d %v", code, v)
	}
	for _, id := range []string{"99", "abc"} {
		if code, _ := do(t, c, "GET", srv.URL+"/api/products/"+id, ""); code != 404 {
			t.Errorf("product %s: %d, want 404", id, code)
		}
	}
}

func TestOrders(t *testing.T) {
	srv := newTestServer(t)
	c := srv.Client()
	code, v := do(t, c, "POST", srv.URL+"/api/orders", `{"productId":2,"quantity":3}`)
	if code != 201 || v["totalCents"] != float64(3*3900) || v["id"] != float64(1) {
		t.Errorf("order: %d %v", code, v)
	}
	for _, body := range []string{
		`{"productId":99,"quantity":1}`, `{"productId":1,"quantity":0}`, `{"productId":1,"quantity":101}`,
		`not json`, `{"productId":1,"quantity":1,"extra":true}`,
	} {
		if code, v := do(t, c, "POST", srv.URL+"/api/orders", body); code != 400 || v["error"] == nil {
			t.Errorf("order %s: %d %v, want 400 with an error", body, code, v)
		}
	}
}

func TestLoginCookieAndToken(t *testing.T) {
	srv := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}

	if code, _ := do(t, c, "GET", srv.URL+"/api/me", ""); code != 401 {
		t.Fatalf("me before login: %d, want 401", code)
	}
	if code, _ := do(t, c, "POST", srv.URL+"/api/login", `{"username":"ana","password":"wrong"}`); code != 401 {
		t.Fatalf("wrong password: %d, want 401", code)
	}
	code, v := do(t, c, "POST", srv.URL+"/api/login", `{"username":"ana","password":"demo"}`)
	if code != 200 || v["token"] == "" {
		t.Fatalf("login: %d %v", code, v)
	}
	token := v["token"].(string)

	// The cookie the login set identifies the user.
	if code, v := do(t, c, "GET", srv.URL+"/api/me", ""); code != 200 || v["username"] != "ana" {
		t.Errorf("me with the cookie: %d %v", code, v)
	}
	// So does the token, without cookies.
	plain := srv.Client()
	if code, v := do(t, plain, "GET", srv.URL+"/api/me", "", "Authorization", "Bearer "+token); code != 200 || v["username"] != "ana" {
		t.Errorf("me with the token: %d %v", code, v)
	}
	// A changed token is rejected.
	forged := strings.Replace(token, token[:4], "Ym9i", 1) // "bob" in base64
	if code, _ := do(t, plain, "GET", srv.URL+"/api/me", "", "Authorization", "Bearer "+forged); code != 401 {
		t.Errorf("forged token: %d, want 401", code)
	}
	// Logging out clears the cookie.
	if code, _ := do(t, c, "POST", srv.URL+"/api/logout", ""); code != 204 {
		t.Errorf("logout: %d, want 204", code)
	}
	if code, _ := do(t, c, "GET", srv.URL+"/api/me", ""); code != 401 {
		t.Errorf("me after logout: %d, want 401", code)
	}
}

// The server answers HTTP/2 without TLS (h2c) and HTTP/1.1 on one port.
func TestServesH2CAndHTTP1(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, ln, newAPI(0, newSigner()).routes()) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()

	for _, tt := range []struct {
		name  string
		proto string
		set   func(*http.Protocols)
	}{
		{"h2c", "HTTP/2.0", func(p *http.Protocols) { p.SetUnencryptedHTTP2(true) }},
		{"http1", "HTTP/1.1", func(p *http.Protocols) { p.SetHTTP1(true) }},
	} {
		tr := &http.Transport{Protocols: new(http.Protocols)}
		tt.set(tr.Protocols)
		res, err := (&http.Client{Transport: tr}).Get("http://" + ln.Addr().String() + "/health")
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		res.Body.Close()
		tr.CloseIdleConnections()
		if res.Proto != tt.proto || res.StatusCode != 200 {
			t.Errorf("%s: %s %d, want %s 200", tt.name, res.Proto, res.StatusCode, tt.proto)
		}
	}
}

// /ws/echo echoes text and binary messages.
func TestWebSocketEcho(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/echo", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	for _, m := range []struct {
		typ  websocket.MessageType
		data string
	}{{websocket.MessageText, "hello"}, {websocket.MessageBinary, "\x01\x02"}} {
		if err := c.Write(ctx, m.typ, []byte(m.data)); err != nil {
			t.Fatal(err)
		}
		typ, data, err := c.Read(ctx)
		if err != nil || typ != m.typ || string(data) != m.data {
			t.Errorf("echo of %q: %v %q %v", m.data, typ, data, err)
		}
	}
	if err := c.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Errorf("close: %v", err)
	}
}
