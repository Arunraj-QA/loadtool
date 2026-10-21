// Package netguard keeps a test to public internet destinations
// (ADR-023): it refuses loopback, private, link-local (cloud metadata
// included), shared, reserved and multicast addresses.
//
// Control checks every connection's resolved address, so a host name
// that resolves, or later re-resolves, to a refused address (DNS
// rebinding) cannot be reached. CheckURL rejects what can be rejected
// before a test starts.
package netguard

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
)

// ErrRefused is wrapped by every refusal.
var ErrRefused = errors.New("destination refused: only public internet addresses are allowed")

// refused are ranges outside the public internet that netip's own
// predicates do not cover.
var refused = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // shared address space (carrier NAT)
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("192.88.99.0/24"),  // 6to4 relay
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, and broadcast
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64: may embed a private IPv4 address
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local NAT64
	netip.MustParsePrefix("100::/64"),        // discard
	netip.MustParsePrefix("2001::/23"),       // IETF protocol assignments (Teredo included)
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("2002::/16"),       // 6to4: may embed a private IPv4 address
}

// CheckAddr returns an error wrapping ErrRefused unless a is a public
// unicast address.
func CheckAddr(a netip.Addr) error {
	a = a.Unmap() // ::ffff:10.0.0.1 is 10.0.0.1
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return fmt.Errorf("%w (%s)", ErrRefused, a)
	}
	for _, p := range refused {
		if p.Contains(a) {
			return fmt.Errorf("%w (%s)", ErrRefused, a)
		}
	}
	return nil
}

// Control is a net.Dialer Control function: it refuses a connection whose
// resolved address is not public. The dialer calls it for every
// connection, after resolution.
func Control(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w (%s)", ErrRefused, address)
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w (%s)", ErrRefused, address)
	}
	return CheckAddr(a)
}

// CheckURL parses an absolute http or https URL with a host and no user
// information. An IP-literal host must be public, and local host names
// (localhost, *.localhost, *.local, *.internal, metadata names) are
// refused; other names are checked when connecting (Control).
func CheckURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("the URL must start with http:// or https://")
	}
	if u.User != nil {
		return nil, fmt.Errorf("put credentials in the authentication settings, not in the URL")
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("the URL has no host")
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if err := CheckAddr(a); err != nil {
			return nil, err
		}
		return u, nil
	}
	name := strings.TrimSuffix(strings.ToLower(host), ".")
	for _, local := range []string{"localhost", ".localhost", ".local", ".internal", ".home.arpa"} {
		if name == strings.TrimPrefix(local, ".") || (strings.HasPrefix(local, ".") && strings.HasSuffix(name, local)) {
			return nil, fmt.Errorf("%w (%s)", ErrRefused, host)
		}
	}
	return u, nil
}
