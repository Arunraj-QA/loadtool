package netguard

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestCheckAddr(t *testing.T) {
	public := []string{"8.8.8.8", "1.1.1.1", "93.184.215.14", "2606:4700:4700::1111", "2a00:1450:4001:80b::200e"}
	refused := []string{
		"127.0.0.1", "127.255.0.9", "10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "169.254.0.1", "100.64.0.1", "0.0.0.0", "0.1.2.3", "224.0.0.1",
		"239.255.255.250", "255.255.255.255", "240.0.0.1", "192.0.2.10", "198.18.0.1",
		"::", "::1", "fe80::1", "fc00::1", "fd12:3456::1", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254",
		"64:ff9b::a00:1", "2002:a00:1::1", "2001:db8::1", "2001::1",
	}
	for _, s := range public {
		if err := CheckAddr(netip.MustParseAddr(s)); err != nil {
			t.Errorf("%s refused: %v", s, err)
		}
	}
	for _, s := range refused {
		if err := CheckAddr(netip.MustParseAddr(s)); !errors.Is(err, ErrRefused) {
			t.Errorf("%s allowed (err %v)", s, err)
		}
	}
	if err := CheckAddr(netip.Addr{}); !errors.Is(err, ErrRefused) {
		t.Errorf("the zero address allowed")
	}
}

func TestControl(t *testing.T) {
	for addr, ok := range map[string]bool{
		"8.8.8.8:443": true, "[2606:4700:4700::1111]:443": true,
		"127.0.0.1:80": false, "[::1]:80": false, "169.254.169.254:80": false,
		"no-port": false, "example.com:80": false, // Control sees resolved IPs only
	} {
		if err := Control("tcp", addr, nil); (err == nil) != ok {
			t.Errorf("Control(%s) = %v, want allowed=%v", addr, err, ok)
		}
	}
}

func TestCheckURL(t *testing.T) {
	ok := []string{"https://api.example.com/v1/items?x=1", "http://8.8.8.8/", "https://[2606:4700:4700::1111]/"}
	bad := []string{
		"", "api.example.com/v1", "ftp://example.com/", "file:///etc/passwd", "https:///path",
		"https://user:pass@example.com/", "http://localhost:8080/", "http://LOCALHOST./",
		"http://api.localhost/", "http://printer.local/", "http://metadata.google.internal/computeMetadata/v1/",
		"http://127.0.0.1/", "http://[::1]/", "http://169.254.169.254/latest/meta-data/", "http://10.0.0.5/",
		"http://router.home.arpa/", "://bad",
	}
	for _, s := range ok {
		if _, err := CheckURL(s); err != nil {
			t.Errorf("%q refused: %v", s, err)
		}
	}
	for _, s := range bad {
		if _, err := CheckURL(s); err == nil {
			t.Errorf("%q allowed", s)
		}
	}
}

// A dialer with Control refuses a host name that resolves to a refused
// address, which is how DNS rebinding is stopped: the check is on the
// resolved IP of every connection.
func TestControlStopsResolvedLocalAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	d := &net.Dialer{Timeout: 2 * time.Second, Control: Control}
	// "localhost" resolves to a loopback address.
	if c, err := d.Dial("tcp", net.JoinHostPort("localhost", port)); err == nil {
		c.Close()
		t.Fatal("connected to localhost through the guard")
	} else if !errors.Is(err, ErrRefused) {
		t.Fatalf("error %v, want ErrRefused", err)
	}
	// Without the guard the same server is reachable.
	c, err := (&net.Dialer{Timeout: 2 * time.Second}).Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}
