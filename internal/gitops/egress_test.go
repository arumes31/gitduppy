package gitops

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gitduppy/gitduppy/pkg/netguard"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

// withEgressPolicy sets the package egress policy for one test and restores
// the default (and drops pooled connections) afterwards.
func withEgressPolicy(t *testing.T, p netguard.Policy) {
	t.Helper()
	SetEgressPolicy(p)
	t.Cleanup(func() {
		SetEgressPolicy(netguard.Policy{AllowPrivate: true})
		gitTransport.current.Load().CloseIdleConnections()
	})
}

func TestGoGitTransportBlocksLoopbackAtDial(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.NotFound(w, nil)
	}))
	defer srv.Close()
	withEgressPolicy(t, netguard.Policy{AllowPrivate: true})

	_, err := git.PlainCloneContext(context.Background(), t.TempDir(), true, &git.CloneOptions{URL: srv.URL + "/repo.git"})
	if err == nil || !strings.Contains(err.Error(), netguard.ErrBlockedAddress.Error()) {
		t.Fatalf("clone of loopback URL: err=%v, want blocked address", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("server was contacted %d times despite the policy", hits.Load())
	}

	// With loopback allowed the same request reaches the server (404 there).
	SetEgressPolicy(netguard.Policy{AllowPrivate: true, AllowLoopback: true})
	_, err = git.PlainCloneContext(context.Background(), t.TempDir(), true, &git.CloneOptions{URL: srv.URL + "/repo.git"})
	if err == nil || hits.Load() == 0 {
		t.Fatalf("expected the allowed request to reach the server: err=%v hits=%d", err, hits.Load())
	}
}

func TestGoGitTransportBlocksRedirectToBlockedHost(t *testing.T) {
	// The redirect target is a link-local (metadata) address, blocked under
	// every policy; the redirect hook must refuse it before any dial.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/repo.git/info/refs?service=git-upload-pack", http.StatusFound)
	}))
	defer srv.Close()
	withEgressPolicy(t, netguard.Policy{AllowPrivate: true, AllowLoopback: true})

	_, err := git.PlainCloneContext(context.Background(), t.TempDir(), true, &git.CloneOptions{URL: srv.URL + "/repo.git"})
	if err == nil || !strings.Contains(err.Error(), netguard.ErrBlockedAddress.Error()) {
		t.Fatalf("redirect to metadata address: err=%v, want blocked address", err)
	}
}

func TestCheckGitRedirectCapsHops(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/a", nil)
	via := make([]*http.Request, maxGitRedirects)
	if err := checkGitRedirect(req, via); err == nil {
		t.Fatal("expected redirect cap error")
	}
}

func TestGitTransportLimitsErrorBodyAndDisablesGzip(t *testing.T) {
	big := strings.Repeat("x", 4*maxGitErrorBody)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "" {
			t.Errorf("transport requested compression: %q", r.Header.Get("Accept-Encoding"))
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, big)
	}))
	defer srv.Close()
	withEgressPolicy(t, netguard.Policy{AllowPrivate: true, AllowLoopback: true})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := gitTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if len(body) != maxGitErrorBody {
		t.Fatalf("error body length=%d, want capped at %d", len(body), maxGitErrorBody)
	}
}

func TestEgressProxyEnforcesPolicy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	proxy, err := startEgressProxy()
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL())

	get := func(proxy *url.URL) (int, error) {
		tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
		defer tr.CloseIdleConnections()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, target.URL, nil)
		resp, err := tr.RoundTrip(req)
		if err != nil {
			return 0, err
		}
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}

	withEgressPolicy(t, netguard.Policy{AllowPrivate: true})
	if code, err := get(proxyURL); err != nil || code != http.StatusForbidden {
		t.Fatalf("plain HTTP to loopback via proxy: code=%d err=%v, want 403", code, err)
	}
	if code := connectStatus(t, proxy, target.Listener.Addr().String(), true); code != http.StatusForbidden {
		t.Fatalf("CONNECT to loopback: status=%d, want 403", code)
	}

	noAuth := *proxyURL
	noAuth.User = nil
	if code, err := get(&noAuth); err != nil || code != http.StatusProxyAuthRequired {
		t.Fatalf("unauthenticated proxy use: code=%d err=%v, want 407", code, err)
	}

	SetEgressPolicy(netguard.Policy{AllowPrivate: true, AllowLoopback: true})
	if code, err := get(proxyURL); err != nil || code != http.StatusOK {
		t.Fatalf("plain HTTP with loopback allowed: code=%d err=%v, want 200", code, err)
	}
	if code := connectStatus(t, proxy, target.Listener.Addr().String(), true); code != http.StatusOK {
		t.Fatalf("CONNECT with loopback allowed: status=%d, want 200", code)
	}
}

// connectStatus issues a CONNECT for target through the proxy and returns the
// response status code.
func connectStatus(t *testing.T, p *egressProxy, target string, auth bool) int {
	t.Helper()
	var d net.Dialer
	conn, err := d.DialContext(context.Background(), "tcp", p.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: http.Header{}}
	if auth {
		req.SetBasicAuth(p.user, p.password)
		req.Header.Set("Proxy-Authorization", req.Header.Get("Authorization"))
		req.Header.Del("Authorization")
	}
	if err := req.Write(conn); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestNativeEgressEnvOverridesProxySettings(t *testing.T) {
	t.Setenv("NO_PROXY", "*")
	t.Setenv("HTTPS_PROXY", "http://elsewhere:3128")
	env := nativeEgressEnv("http://u:p@127.0.0.1:1")
	seen := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if isProxyEnvName(k) {
			if prev, dup := seen[k]; dup {
				t.Fatalf("%s set twice (%q, %q)", k, prev, v)
			}
			seen[k] = v
		}
	}
	if seen["NO_PROXY"] != "" || seen["no_proxy"] != "" {
		t.Fatalf("NO_PROXY not cleared: %v", seen)
	}
	if seen["HTTPS_PROXY"] != "http://u:p@127.0.0.1:1" {
		t.Fatalf("HTTPS_PROXY=%q", seen["HTTPS_PROXY"])
	}
}

func TestNativeEgressArgs(t *testing.T) {
	args := strings.Join(nativeEgressArgs("http://p", netguard.Policy{}), " ")
	for _, want := range []string{"http.proxy=http://p", "lfs.transfer.enablehrefrewrite=true", ".insteadOf=http://127.", ".insteadOf=https://localhost"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
	if args := nativeEgressArgs("http://p", netguard.Policy{AllowLoopback: true}); len(args) != 2 {
		t.Errorf("loopback allowed: args=%v, want only http.proxy", args)
	}
}

func TestLFSConfigArgsGitScheme(t *testing.T) {
	args := lfsConfigArgs("git://host.example:9418/a/b.git")
	if len(args) != 2 || args[1] != "lfs.url=https://host.example/a/b.git/info/lfs" {
		t.Fatalf("lfsConfigArgs(git://)=%v", args)
	}
}

func TestIsSSHRemote(t *testing.T) {
	cases := map[string]bool{
		"git@github.com:user/repo.git":  true,
		"ssh://git@host:22/a.git":       true,
		"https://github.com/u/r.git":    false,
		"C:\\repos\\a":                  false,
		"/srv/git/a.git":                false,
		"file:///srv/git/a.git":         false,
		"git+ssh://git@host/a/b.git":    true,
		"host.example:path/to/repo.git": true,
	}
	for in, want := range cases {
		if got := isSSHRemote(in); got != want {
			t.Errorf("isSSHRemote(%q)=%v want %v", in, got, want)
		}
	}
}

func TestRefuseLFSEndpointOverride(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := RunGitCommand(ctx, dir, "init"); err != nil {
		t.Skipf("git init unavailable: %v", err)
	}
	if err := refuseLFSEndpointOverride(ctx, dir); err != nil {
		t.Fatalf("no .lfsconfig: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".lfsconfig"), []byte("[lfs]\n\turl = http://10.0.0.5/lfs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refuseLFSEndpointOverride(ctx, dir); err == nil {
		t.Fatal("expected .lfsconfig lfs.url to be refused")
	}
	if err := os.WriteFile(filepath.Join(dir, ".lfsconfig"), []byte("[remote \"origin\"]\n\tlfsurl = http://10.0.0.5/lfs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refuseLFSEndpointOverride(ctx, dir); err == nil {
		t.Fatal("expected .lfsconfig remote.origin.lfsurl to be refused")
	}
	if err := os.WriteFile(filepath.Join(dir, ".lfsconfig"), []byte("[lfs]\n\tfetchinclude = docs/*\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refuseLFSEndpointOverride(ctx, dir); err != nil {
		t.Fatalf("harmless .lfsconfig refused: %v", err)
	}
}

func TestRunGitCommandLimited(t *testing.T) {
	out, err := RunGitCommandLimited(context.Background(), ".", 5, "version")
	if !errors.Is(err, ErrOutputTooLarge) || len(out) != 5 {
		t.Fatalf("out=%q err=%v, want 5 bytes and ErrOutputTooLarge", out, err)
	}
	out, err = GitOutputLimited(context.Background(), ".", 1<<10, "version")
	if err != nil || !strings.HasPrefix(out, "git version") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestReconcileOriginURL(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{"https://old.example/a.git"}}); err != nil {
		t.Fatal(err)
	}
	if err := reconcileOriginURL(repo, "https://new.example/a.git"); err != nil {
		t.Fatal(err)
	}
	reopened, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := reopened.Remote("origin")
	if err != nil {
		t.Fatal(err)
	}
	if got := remote.Config().URLs; len(got) != 1 || got[0] != "https://new.example/a.git" {
		t.Fatalf("origin URLs=%v", got)
	}
	if err := reconcileOriginURL(repo, ""); err == nil {
		t.Fatal("empty authorized URL must be rejected")
	}
}

// TestFetchRepositoryFollowsCurrentURL checks that a fetch uses the URL passed
// in (the repository's current, validated URL), not a stale on-disk origin.
func TestFetchRepositoryFollowsCurrentURL(t *testing.T) {
	ctx := context.Background()
	makeOrigin := func(content string) string {
		dir := t.TempDir()
		if _, err := RunGitCommand(ctx, dir, "init", "-b", "main"); err != nil {
			t.Skipf("git init failed: %v", err)
		}
		for _, args := range [][]string{{"config", "user.name", "T"}, {"config", "user.email", "t@example.com"}} {
			if _, err := RunGitCommand(ctx, dir, args...); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := RunGitCommand(ctx, dir, "add", "f.txt"); err != nil {
			t.Fatal(out)
		}
		if out, err := RunGitCommand(ctx, dir, "commit", "-m", content); err != nil {
			t.Fatal(out)
		}
		return dir
	}
	oldOrigin := makeOrigin("old")
	newOrigin := makeOrigin("new")

	base := t.TempDir()
	g := NewGitOperations(base)
	clone := filepath.Join(base, "mirror")
	if err := g.CloneRepository(ctx, &CloneOptions{URL: oldOrigin, Path: clone, Bare: true}); err != nil {
		t.Fatal(err)
	}
	if err := g.FetchRepository(ctx, &CloneOptions{URL: newOrigin, Path: clone, Bare: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := g.GetRemoteURL(clone); got != newOrigin {
		t.Fatalf("origin=%q, want %q", got, newOrigin)
	}
	out, err := RunGitCommand(ctx, newOrigin, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunGitCommand(ctx, clone, "cat-file", "-e", strings.TrimSpace(out)); err != nil {
		t.Fatalf("commit from the current URL was not fetched: %v", err)
	}
}
