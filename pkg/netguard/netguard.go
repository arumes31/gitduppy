// Package netguard keeps server-side outbound requests (webhook deliveries,
// clone URLs) from being pointed at internal network services or cloud
// metadata endpoints.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ErrBlockedAddress is returned when a destination resolves to an address the
// active Policy does not allow.
var ErrBlockedAddress = errors.New("destination address is not allowed")

// cgnat is the shared address space (RFC 6598), not covered by netip.IsPrivate.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Policy selects which address ranges outbound connections may reach.
// Unspecified, link-local (including the 169.254.169.254 cloud metadata
// endpoint) and multicast addresses are always blocked.
type Policy struct {
	// AllowPrivate permits RFC 1918, unique-local (fc00::/7) and CGNAT ranges.
	AllowPrivate bool
	// AllowLoopback permits 127.0.0.0/8 and ::1.
	AllowLoopback bool
}

// CheckAddr reports whether addr may be contacted under the policy.
func (p Policy) CheckAddr(addr netip.Addr) error {
	addr = addr.Unmap()
	switch {
	case !addr.IsValid(),
		addr.IsUnspecified(),
		addr.IsLinkLocalUnicast(),
		addr.IsLinkLocalMulticast(),
		addr.IsInterfaceLocalMulticast(),
		addr.IsMulticast(),
		addr.Is4() && addr.As4()[0] == 0: // 0.0.0.0/8 ("this network")
		return fmt.Errorf("%w: %s", ErrBlockedAddress, addr)
	case addr.IsLoopback():
		if !p.AllowLoopback {
			return fmt.Errorf("%w: %s (loopback)", ErrBlockedAddress, addr)
		}
	case addr.IsPrivate() || cgnat.Contains(addr):
		if !p.AllowPrivate {
			return fmt.Errorf("%w: %s (private network)", ErrBlockedAddress, addr)
		}
	}
	return nil
}

// CheckHost resolves host (a name or IP literal, without port) and returns an
// error if any of its addresses is blocked. A resolution failure is returned
// as-is so callers can decide whether to fail open (e.g. when a proxy does the
// resolving) or closed.
func (p Policy) CheckHost(ctx context.Context, host string) error {
	if addr, err := netip.ParseAddr(host); err == nil {
		return p.CheckAddr(addr)
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return err
	}
	for _, a := range addrs {
		if err := p.CheckAddr(a); err != nil {
			return fmt.Errorf("%s: %w", host, err)
		}
	}
	return nil
}

// control is a net.Dialer Control hook that enforces the policy on the address
// actually being connected to, after DNS resolution, which defeats DNS
// rebinding between a validation step and the request.
func (p Policy) control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w: unparseable address %q", ErrBlockedAddress, host)
	}
	return p.CheckAddr(addr)
}

// Transport returns an http.Transport whose connections are checked against
// the policy at dial time. Environment proxies are ignored because the proxy,
// not this process, would then pick the final destination.
func (p Policy) Transport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   p.control,
	}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// NoRedirect is an http.Client CheckRedirect hook that returns the redirect
// response instead of following it, so a permitted host cannot bounce a
// request to a blocked one.
func NoRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// HTTPClient returns an http.Client using Transport and NoRedirect.
func (p Policy) HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		Transport:     p.Transport(),
		CheckRedirect: NoRedirect,
	}
}
