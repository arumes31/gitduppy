package gitops

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gitduppy/gitduppy/pkg/netguard"
)

// egressProxy is a minimal in-process HTTP proxy, listening on loopback only,
// that native git and git-lfs subprocesses are pointed at (http.proxy and the
// *_PROXY environment). go-git's in-process transport enforces the address
// policy at dial time, but the git-lfs binary resolves and connects on its own
// and follows object download URLs handed out by the LFS API; routing it
// through this proxy applies the same policy to every CONNECT tunnel and plain
// HTTP request it makes. A random per-process credential keeps other local
// users from borrowing it.
type egressProxy struct {
	ln       net.Listener
	srv      *http.Server
	user     string
	password string
}

//nolint:gochecknoglobals
var (
	egressProxyOnce sync.Once
	egressProxyInst *egressProxy
	errEgressProxy  error
)

const (
	egressDialTimeout = 10 * time.Second
	// loopbackRewriteBase is substituted for loopback URL prefixes in LFS
	// object hrefs (see nativeEgressArgs). The .invalid TLD never resolves.
	loopbackRewriteBase = "http://loopback-blocked.invalid/"
)

// sharedEgressProxy starts the proxy on first use and returns it.
func sharedEgressProxy() (*egressProxy, error) {
	egressProxyOnce.Do(func() {
		egressProxyInst, errEgressProxy = startEgressProxy()
	})
	return egressProxyInst, errEgressProxy
}

func startEgressProxy() (*egressProxy, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("egress proxy: %w", err)
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("egress proxy: %w", err)
	}
	p := &egressProxy{ln: ln, user: "gitduppy", password: hex.EncodeToString(secret)}
	p.srv = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

// Close stops the proxy and drops its connections. The shared instance lives
// for the whole process; this is for tests.
func (p *egressProxy) Close() error {
	return p.srv.Close()
}

// URL returns the proxy URL including its credential.
func (p *egressProxy) URL() string {
	u := url.URL{Scheme: "http", User: url.UserPassword(p.user, p.password), Host: p.ln.Addr().String()}
	return u.String()
}

func (p *egressProxy) authorized(r *http.Request) bool {
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(p.user+":"+p.password))
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(want)) == 1
}

func (p *egressProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.authorized(r) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="gitduppy"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if r.Method == http.MethodConnect {
		p.serveConnect(w, r)
		return
	}
	p.serveForward(w, r)
}

// policyDialer returns a dialer whose Control hook rejects any connection to
// an address the current egress policy blocks, after DNS resolution.
func policyDialer(policy netguard.Policy) *net.Dialer {
	return &net.Dialer{
		Timeout: egressDialTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			addr, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("%w: unparseable address %q", netguard.ErrBlockedAddress, host)
			}
			return policy.CheckAddr(addr)
		},
	}
}

func (p *egressProxy) serveConnect(w http.ResponseWriter, r *http.Request) {
	if _, _, err := net.SplitHostPort(r.Host); err != nil {
		http.Error(w, "invalid CONNECT target", http.StatusBadRequest)
		return
	}
	upstream, err := policyDialer(EgressPolicy()).DialContext(r.Context(), "tcp", r.Host)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, netguard.ErrBlockedAddress) {
			status = http.StatusForbidden
		}
		http.Error(w, "egress denied or unreachable", status)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	// Bytes the client already sent after the CONNECT head (buffered by the
	// server's reader) must reach the upstream first.
	if n := buf.Reader.Buffered(); n > 0 {
		pending, _ := buf.Peek(n)
		if _, err := upstream.Write(pending); err != nil {
			_ = client.Close()
			_ = upstream.Close()
			return
		}
	}
	go pipe(upstream, client)
	pipe(client, upstream)
}

// pipe copies src to dst and closes both ends when done, which also unblocks
// the copy running in the opposite direction.
func pipe(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	_ = dst.Close()
	_ = src.Close()
}

// hopHeaders are connection-specific headers a proxy must not forward.
//
//nolint:gochecknoglobals
var hopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func removeHopHeaders(h http.Header) {
	for _, f := range h.Values("Connection") {
		for _, name := range strings.Split(f, ",") {
			h.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range hopHeaders {
		h.Del(name)
	}
}

// serveForward proxies a plain-HTTP (absolute-form) request. Redirects are not
// followed here: they go back to the client, whose next request passes through
// the proxy (and the policy) again.
func (p *egressProxy) serveForward(w http.ResponseWriter, r *http.Request) {
	if r.URL == nil || r.URL.Scheme != "http" || r.URL.Host == "" {
		http.Error(w, "only absolute http:// requests or CONNECT are proxied", http.StatusBadRequest)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	removeHopHeaders(out.Header)

	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           policyDialer(EgressPolicy()).DialContext,
		DisableCompression:    true,
		ResponseHeaderTimeout: gitResponseHeaderTimeout,
		DisableKeepAlives:     true,
	}
	resp, err := tr.RoundTrip(out)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, netguard.ErrBlockedAddress) {
			status = http.StatusForbidden
		}
		http.Error(w, "egress denied or unreachable", status)
		return
	}
	defer resp.Body.Close()
	removeHopHeaders(resp.Header)
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// proxyEnvNames are every environment variable spelling git (libcurl) and
// git-lfs read for proxy selection and proxy bypass.
//
//nolint:gochecknoglobals
var proxyEnvNames = []string{
	"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy",
	"ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy",
}

// nativeEgressEnv returns the environment for a network-capable native
// git/git-lfs subprocess: the parent environment with every inherited proxy
// setting replaced so all HTTP(S) traffic goes to proxyURL and nothing is
// exempted via NO_PROXY. GIT_LFS_SKIP_SMUDGE is deliberately not set here: the
// callers are the explicit LFS transfer commands.
func nativeEgressEnv(proxyURL string) []string {
	env := make([]string, 0, len(os.Environ())+len(proxyEnvNames))
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if isProxyEnvName(name) {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HTTP_PROXY="+proxyURL, "http_proxy="+proxyURL,
		"HTTPS_PROXY="+proxyURL, "https_proxy="+proxyURL,
		"ALL_PROXY="+proxyURL, "all_proxy="+proxyURL,
		"NO_PROXY=", "no_proxy=",
	)
}

func isProxyEnvName(name string) bool {
	for _, n := range proxyEnvNames {
		// Environment names are case-insensitive on Windows.
		if strings.EqualFold(name, n) {
			return true
		}
	}
	return false
}

// nativeEgressArgs returns the "git -c" arguments that force native git and
// git-lfs HTTP traffic through the egress proxy. git-lfs (like Go's own proxy
// selection) never proxies requests whose host is "localhost" or a loopback IP
// literal, so when loopback targets are not allowed, LFS object hrefs with such
// a prefix are rewritten to an unresolvable host instead of being fetched
// directly.
func nativeEgressArgs(proxyURL string, policy netguard.Policy) []string {
	args := []string{"-c", "http.proxy=" + proxyURL}
	if policy.AllowLoopback {
		return args
	}
	args = append(args, "-c", "lfs.transfer.enablehrefrewrite=true")
	// One rewrite base per prefix so each url.<base>.insteadOf has a single value.
	n := 0
	// "[::" and "[0" cover every spelling of ::1 and of IPv4-mapped/compatible
	// loopback (e.g. [::0:1], [0:0:0:0:0:ffff:7f00:1], [0000::1]); global
	// unicast IPv6 never starts with either.
	for _, prefix := range []string{"localhost", "127.", "[::", "[0"} {
		for _, scheme := range []string{"http://", "https://"} {
			base := fmt.Sprintf("%s%d/", loopbackRewriteBase, n)
			args = append(args, "-c", "url."+base+".insteadOf="+scheme+prefix)
			n++
		}
	}
	return args
}
