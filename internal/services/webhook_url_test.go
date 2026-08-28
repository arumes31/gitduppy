package services

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"
)

type fakeWebhookResolver struct {
	addresses map[string][]netip.Addr
	err       error
}

func (r fakeWebhookResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.addresses[host], nil
}

func TestValidateWebhookURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rawURL    string
		addresses []netip.Addr
		wantErr   bool
	}{
		{name: "public HTTPS destination", rawURL: "https://hooks.example.com/events", addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}},
		{name: "HTTP rejected", rawURL: "http://hooks.example.com/events", addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}, wantErr: true},
		{name: "userinfo rejected", rawURL: "https://user:pass@hooks.example.com/events", addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}, wantErr: true},
		{name: "loopback literal rejected", rawURL: "https://127.0.0.1/events", wantErr: true},
		{name: "private DNS result rejected", rawURL: "https://hooks.example.com/events", addresses: []netip.Addr{netip.MustParseAddr("10.0.0.8")}, wantErr: true},
		{name: "mixed public and private DNS rejected", rawURL: "https://hooks.example.com/events", addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("192.168.1.4")}, wantErr: true},
		{name: "documentation network rejected", rawURL: "https://hooks.example.com/events", addresses: []netip.Addr{netip.MustParseAddr("2001:db8::1")}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolver := fakeWebhookResolver{addresses: map[string][]netip.Addr{"hooks.example.com": tt.addresses}}
			err := validateWebhookURL(t.Context(), resolver, tt.rawURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateWebhookURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSecureWebhookDialContextPinsValidatedAddress(t *testing.T) {
	t.Parallel()

	resolver := fakeWebhookResolver{addresses: map[string][]netip.Addr{
		"hooks.example.com": {netip.MustParseAddr("8.8.8.8")},
	}}
	var dialed string
	dial := secureWebhookDialContext(resolver, func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = address
		return nil, errors.New("test dial stopped")
	})

	_, err := dial(t.Context(), "tcp", "hooks.example.com:443")
	if err == nil {
		t.Fatal("dial should return the injected test error")
	}
	if dialed != "8.8.8.8:443" {
		t.Fatalf("dial address = %q, want validated IP", dialed)
	}
}

func TestSecureWebhookDialContextRejectsReboundPrivateAddress(t *testing.T) {
	t.Parallel()

	resolver := fakeWebhookResolver{addresses: map[string][]netip.Addr{
		"hooks.example.com": {netip.MustParseAddr("169.254.169.254")},
	}}
	dialCalled := false
	dial := secureWebhookDialContext(resolver, func(_ context.Context, _, _ string) (net.Conn, error) {
		dialCalled = true
		return nil, nil
	})

	if _, err := dial(t.Context(), "tcp", "hooks.example.com:443"); err == nil {
		t.Fatal("private rebound address should be rejected")
	}
	if dialCalled {
		t.Fatal("underlying dialer must not receive a private address")
	}
}

func TestWebhookHTTPClientRefusesRedirects(t *testing.T) {
	t.Parallel()

	client := newWebhookHTTPClient(time.Second)
	err := client.CheckRedirect(&http.Request{}, []*http.Request{{}})
	if !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect() error = %v, want http.ErrUseLastResponse", err)
	}
}
