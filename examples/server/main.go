// Command server is the demo API the examples run against: a small shop
// with products, orders, a login and a WebSocket echo. It uses the
// standard library and github.com/coder/websocket, and keeps no
// per-session state (sessions are signed, not stored), so its memory
// stays flat under load.
//
//	go run ./examples/server                  # http://127.0.0.1:8090
//	go run ./examples/server -addr 127.0.0.1:9000 -delay 20ms
//
// Endpoints (JSON in and out):
//
//	GET  /health               {"status":"ok"}
//	GET  /api/products         every product
//	GET  /api/products/{id}    one product; 404 if unknown
//	POST /api/orders           {"productId":1,"quantity":2}: 201 with the order; 400 if invalid
//	POST /api/login            {"username":"…","password":"demo"}: 200 with a token and a
//	                           session cookie; 401 for another password
//	GET  /api/me               the user of the session cookie or of
//	                           "Authorization: Bearer <token>"; 401 without one
//	POST /api/logout           clears the session cookie
//	GET  /ws/echo              WebSocket: echoes every message, after the
//	                           -delay (text and binary)
//	POST /graphql              GraphQL: products, product(id), fail and the
//	                           placeOrder mutation, after the -delay
//
// On -grpc-addr (127.0.0.1:8091) it serves the gRPC greeter service of
// examples/proto/greeter.proto, with server reflection. On -kafka-port
// (9092) it runs an in-process Kafka broker (franz-go's kfake) with the
// topics orders and events, so the Kafka examples need no Docker.
//
// It speaks HTTP/1.1 and HTTP/2 without TLS (h2c) on the same port.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/grpc"

	"github.com/Arunraj-QA/loadtool/internal/protocols/graphql/graphqltest"
	"github.com/Arunraj-QA/loadtool/internal/protocols/grpc/grpctest"
	"github.com/Arunraj-QA/loadtool/internal/protocols/kafka/kafkatest"
)

// Password is the one password every demo user has.
const Password = "demo"

const sessionCookie = "session"

type product struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	PriceCents int    `json:"priceCents"`
}

var products = []product{
	{1, "Espresso beans, 1 kg", 2490},
	{2, "Milk frother", 3900},
	{3, "Ceramic cup", 1200},
	{4, "Hand grinder", 5400},
	{5, "Paper filters (100)", 450},
}

func findProduct(id int) (product, bool) {
	for _, p := range products {
		if p.ID == id {
			return p, true
		}
	}
	return product{}, false
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "listen address")
	grpcAddr := flag.String("grpc-addr", "127.0.0.1:8091", "gRPC listen address (greeter service with reflection); empty to turn it off")
	kafkaPort := flag.Int("kafka-port", 9092, "port of the in-process Kafka broker (topics orders and events); 0 to turn it off")
	delay := flag.Duration("delay", 5*time.Millisecond, "time every /api/ request, WebSocket echo and gRPC SayHello waits before answering")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("demo API listening on http://%s (HTTP/1.1 and h2c; /api/ delay %s; password %q)\n", ln.Addr(), *delay, Password)
	if *grpcAddr != "" {
		gln, err := net.Listen("tcp", *grpcAddr)
		if err != nil {
			log.Fatal(err)
		}
		stopGRPC, err := serveGRPC(gln, *delay)
		if err != nil {
			log.Fatal(err)
		}
		defer stopGRPC()
		fmt.Printf("demo gRPC listening on %s (greeter.Greeter, reflection on)\n", gln.Addr())
	}
	if *kafkaPort != 0 {
		cluster, err := kafkatest.NewCluster(*kafkaPort, "orders", "events")
		if err != nil {
			log.Fatal(err)
		}
		defer cluster.Close()
		fmt.Printf("demo Kafka listening on %s (in-process; topics orders and events, %d partitions each)\n", strings.Join(cluster.ListenAddrs(), ","), kafkatest.Partitions)
	}
	if err := serve(ctx, ln, newAPI(*delay, newSigner()).routes()); err != nil {
		log.Fatal(err)
	}
}

// serveGRPC serves the greeter service (examples/proto/greeter.proto, with
// server reflection) on ln. The returned function stops it, letting calls
// in progress finish.
func serveGRPC(ln net.Listener, delay time.Duration) (stop func(), err error) {
	srv := grpc.NewServer()
	if err := grpctest.Register(srv, delay); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ln)
	}()
	return func() {
		srv.GracefulStop()
		<-done
	}, nil
}

// serve runs the server on ln until ctx is done, then shuts it down.
func serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, Protocols: new(http.Protocols)}
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// signer makes and checks session values: the user name and an HMAC of
// it, so a session needs no server-side storage.
type signer struct{ key []byte }

func newSigner() signer {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return signer{key}
}

func (s signer) mac(user string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(user))
	return hex.EncodeToString(m.Sum(nil))
}

func (s signer) sign(user string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(user)) + "." + s.mac(user)
}

// verify returns the user of a signed value.
func (s signer) verify(v string) (string, bool) {
	enc, sig, ok := strings.Cut(v, ".")
	if !ok {
		return "", false
	}
	user, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || !hmac.Equal([]byte(sig), []byte(s.mac(string(user)))) {
		return "", false
	}
	return string(user), true
}

type api struct {
	delay  time.Duration
	signer signer
	orders atomic.Int64
}

func newAPI(delay time.Duration, s signer) *api { return &api{delay: delay, signer: s} }

func (a *api) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/echo", a.wsEcho)
	gql, err := graphqltest.Handler(a.delay)
	if err != nil {
		panic(err) // the schema is fixed: an error is a bug
	}
	mux.Handle("POST /graphql", gql)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/products", a.listProducts)
	mux.HandleFunc("GET /api/products/{id}", a.getProduct)
	mux.HandleFunc("POST /api/orders", a.createOrder)
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("GET /api/me", a.me)
	mux.HandleFunc("POST /api/logout", a.logout)
	return a.delayed(mux)
}

// delayed waits a.delay before /api/ requests, like a real backend; a
// request cancelled meanwhile gets no response.
func (a *api) delayed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.delay > 0 && strings.HasPrefix(r.URL.Path, "/api/") {
			t := time.NewTimer(a.delay)
			select {
			case <-t.C:
			case <-r.Context().Done():
				t.Stop()
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *api) listProducts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"products": products})
}

func (a *api) getProduct(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	p, ok := findProduct(id)
	if err != nil || !ok {
		writeError(w, http.StatusNotFound, "no such product")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *api) createOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProductID int `json:"productId"`
		Quantity  int `json:"quantity"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	p, ok := findProduct(in.ProductID)
	if !ok {
		writeError(w, http.StatusBadRequest, "no such product")
		return
	}
	if in.Quantity < 1 || in.Quantity > 100 {
		writeError(w, http.StatusBadRequest, "quantity must be 1 to 100")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": a.orders.Add(1), "productId": p.ID, "quantity": in.Quantity, "totalCents": p.PriceCents * in.Quantity,
	})
}

func (a *api) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if !readJSON(w, r, &in) {
		return
	}
	if in.Username == "" || len(in.Username) > 64 || in.Password != Password {
		writeError(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	token := a.signer.sign(in.Username)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]string{"username": in.Username, "token": token})
}

func (a *api) me(w http.ResponseWriter, r *http.Request) {
	user, ok := a.user(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "log in first")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": user})
}

func (a *api) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

// user returns the logged-in user, from a bearer token or else the
// session cookie.
func (a *api) user(r *http.Request) (string, bool) {
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return a.signer.verify(token)
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		return a.signer.verify(c.Value)
	}
	return "", false
}

// readJSON decodes a small JSON body into v, answering 400 if it cannot.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	w.Write(b)
}

// wsEcho echoes every WebSocket message after the -delay, like a backend
// that answers each request. It ends when the client closes.
func (a *api) wsEcho(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept has answered the request
	}
	defer c.CloseNow()
	// A hijacked connection's request context is not cancelled when the
	// client goes away; reads fail instead, which ends the loop.
	ctx := context.Background()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if a.delay > 0 {
			time.Sleep(a.delay)
		}
		if err := c.Write(ctx, typ, data); err != nil {
			return
		}
	}
}
