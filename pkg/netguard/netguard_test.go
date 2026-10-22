package netguard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestPolicyCheckAddr(t *testing.T) {
	strict := Policy{}
	lax := Policy{AllowPrivate: true, AllowLoopback: true}
	cases := []struct {
		addr          string
		strictAllowed bool
		laxAllowed    bool
	}{
		{"8.8.8.8", true, true},
		{"2606:4700::1111", true, true},
		{"127.0.0.1", false, true},
		{"::1", false, true},
		{"10.1.2.3", false, true},
		{"172.16.0.1", false, true},
		{"192.168.1.1", false, true},
		{"100.64.0.1", false, true},
		{"fd00::1", false, true},
		{"::ffff:10.0.0.1", false, true},
		// Always blocked, whatever the policy.
		{"169.254.169.254", false, false},
		{"fe80::1", false, false},
		{"0.0.0.0", false, false},
		{"::", false, false},
		{"0.1.2.3", false, false},
		{"224.0.0.1", false, false},
	}
	for _, tc := range cases {
		addr := netip.MustParseAddr(tc.addr)
		if got := strict.CheckAddr(addr) == nil; got != tc.strictAllowed {
			t.Errorf("strict.CheckAddr(%s) allowed=%v want %v", tc.addr, got, tc.strictAllowed)
		}
		if got := lax.CheckAddr(addr) == nil; got != tc.laxAllowed {
			t.Errorf("lax.CheckAddr(%s) allowed=%v want %v", tc.addr, got, tc.laxAllowed)
		}
	}
}

func TestPolicyCheckHostLiteral(t *testing.T) {
	if err := (Policy{}).CheckHost(context.Background(), "127.0.0.1"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected loopback literal to be blocked, got %v", err)
	}
	if err := (Policy{}).CheckHost(context.Background(), "localhost"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected localhost to be blocked, got %v", err)
	}
}

func TestHTTPClientBlocksLoopbackAtDialTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	resp, err := Policy{}.HTTPClient(5 * time.Second).Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected request to a loopback server to be blocked")
	}
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected ErrBlockedAddress, got %v", err)
	}

	resp, err = Policy{AllowLoopback: true}.HTTPClient(5 * time.Second).Get(srv.URL)
	if err != nil {
		t.Fatalf("loopback should be reachable when allowed: %v", err)
	}
	_ = resp.Body.Close()
}

func TestHTTPClientDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	resp, err := Policy{AllowLoopback: true}.HTTPClient(5 * time.Second).Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected the redirect response itself, got %d", resp.StatusCode)
	}
}
