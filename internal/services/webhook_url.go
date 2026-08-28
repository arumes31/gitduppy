package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type webhookResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type webhookDialFunc func(ctx context.Context, network, address string) (net.Conn, error)

const webhookDNSValidationTimeout = 5 * time.Second

var nonPublicWebhookPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func parseWebhookURL(rawURL string) (*url.URL, error) {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: webhook URL is invalid", ErrValidation)
	}
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: webhook URL must use HTTPS", ErrValidation)
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("%w: webhook URL must include a hostname", ErrValidation)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("%w: webhook URL must not include credentials", ErrValidation)
	}
	return parsed, nil
}

func isPublicWebhookIP(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range nonPublicWebhookPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func resolvePublicWebhookIPs(ctx context.Context, resolver webhookResolver, hostname string) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(hostname); err == nil {
		if !isPublicWebhookIP(literal) {
			return nil, fmt.Errorf("webhook destination resolves to a non-public address")
		}
		return []netip.Addr{literal.Unmap()}, nil
	}

	addresses, err := resolver.LookupNetIP(ctx, "ip", hostname)
	if err != nil {
		return nil, fmt.Errorf("resolve webhook destination: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("webhook destination has no IP addresses")
	}
	for _, address := range addresses {
		if !isPublicWebhookIP(address) {
			return nil, fmt.Errorf("webhook destination resolves to a non-public address")
		}
	}
	return addresses, nil
}

func validateWebhookURL(ctx context.Context, resolver webhookResolver, rawURL string) error {
	parsed, err := parseWebhookURL(rawURL)
	if err != nil {
		return err
	}
	resolveCtx, cancel := context.WithTimeout(ctx, webhookDNSValidationTimeout)
	defer cancel()
	if _, err := resolvePublicWebhookIPs(resolveCtx, resolver, parsed.Hostname()); err != nil {
		return fmt.Errorf("%w: %w", ErrValidation, err)
	}
	return nil
}

func secureWebhookDialContext(resolver webhookResolver, dial webhookDialFunc) webhookDialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		hostname, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("parse webhook destination: %w", err)
		}
		addresses, err := resolvePublicWebhookIPs(ctx, resolver, hostname)
		if err != nil {
			return nil, err
		}

		var dialErrors []error
		for _, resolved := range addresses {
			conn, dialErr := dial(ctx, network, net.JoinHostPort(resolved.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			dialErrors = append(dialErrors, dialErr)
		}
		return nil, fmt.Errorf("dial webhook destination: %w", errors.Join(dialErrors...))
	}
}

func newWebhookHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = secureWebhookDialContext(net.DefaultResolver, dialer.DialContext)

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func sanitizeWebhookDeliveryError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if strings.Contains(message, "non-public address") {
		return "webhook destination is not public"
	}
	return message
}
