package gitops

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gitduppy/gitduppy/pkg/netguard"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

const (
	// maxGitErrorBody caps how much of a non-2xx response body is handed to
	// go-git. Its http.NewErr reads the whole (transparently gunzipped) body
	// into memory to build the error message, so an unbounded body from a
	// hostile server would otherwise be an unbounded allocation.
	maxGitErrorBody = 64 << 10
	// maxGitRedirects caps redirect hops on the discovery request.
	maxGitRedirects = 5
	// gitResponseHeaderTimeout bounds how long a remote may stall before
	// sending response headers. The whole operation is additionally bounded by
	// the caller's context (the worker's clone timeout); no overall
	// http.Client.Timeout is set because it would also cap the pack download.
	gitResponseHeaderTimeout = 5 * time.Minute
)

// egressPolicy is the address policy applied to every outbound Git connection:
// go-git's HTTP(S) transport (dial-time check of the resolved address, so
// redirects and DNS rebinding cannot reach a blocked range) and the local
// egress proxy native git/git-lfs subprocesses are forced through. Private
// networks are allowed by default because self-hosted Git servers commonly
// live there; loopback is opt-in via SetEgressPolicy. Link-local and cloud
// metadata addresses are always blocked by netguard.
//
//nolint:gochecknoglobals
var egressPolicy atomic.Pointer[netguard.Policy]

// gitTransport is the RoundTripper installed into go-git for http and https.
//
//nolint:gochecknoglobals
var gitTransport = &policyRoundTripper{}

func init() {
	SetEgressPolicy(netguard.Policy{AllowPrivate: true})

	// The http.Client's CheckRedirect runs in addition to go-git's own hook
	// (scheme-downgrade, credential stripping, discovery-only redirects).
	gitClient := githttp.NewClientWithOptions(&http.Client{
		Transport:     gitTransport,
		CheckRedirect: checkGitRedirect,
	}, &githttp.ClientOptions{RedirectPolicy: githttp.FollowInitialRedirects})
	client.InstallProtocol("http", gitClient)
	client.InstallProtocol("https", gitClient)
}

// SetEgressPolicy replaces the address policy for outbound Git traffic. It is
// called when the clone worker is constructed (security.allow_private_network_targets
// controls AllowLoopback). Safe for concurrent use; in-flight connections keep
// the policy they were dialed with.
func SetEgressPolicy(p netguard.Policy) {
	egressPolicy.Store(&p)

	tr := p.Transport()
	// Never ask for gzip: Go would transparently inflate it, which would let a
	// small compressed error body expand past any byte budget measured on the
	// wire. Smart-HTTP pack data is already compressed.
	tr.DisableCompression = true
	tr.ResponseHeaderTimeout = gitResponseHeaderTimeout
	if old := gitTransport.current.Swap(tr); old != nil {
		old.CloseIdleConnections()
	}
}

// EgressPolicy returns the address policy currently applied to Git traffic.
func EgressPolicy() netguard.Policy {
	if p := egressPolicy.Load(); p != nil {
		return *p
	}
	return netguard.Policy{AllowPrivate: true}
}

// policyRoundTripper forwards to the current policy-checked http.Transport and
// bounds the body of every non-2xx response.
type policyRoundTripper struct {
	current atomic.Pointer[http.Transport]
}

func (t *policyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.current.Load().RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		resp.Body = limitBody(resp.Body, maxGitErrorBody)
		resp.ContentLength = -1
	}
	return resp, nil
}

// limitBody returns a ReadCloser that yields at most n bytes of rc and closes
// rc itself on Close.
func limitBody(rc io.ReadCloser, n int64) io.ReadCloser {
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(rc, n), rc}
}

// checkGitRedirect caps redirect chains and refuses a hop whose host resolves
// to a blocked address. The dial-time check in the transport is the actual
// enforcement (it sees the address really connected to); this only fails
// earlier with a clearer error.
func checkGitRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxGitRedirects {
		return fmt.Errorf("http redirect: stopped after %d redirects", len(via))
	}
	if err := EgressPolicy().CheckHost(req.Context(), req.URL.Hostname()); errors.Is(err, netguard.ErrBlockedAddress) {
		return fmt.Errorf("http redirect: %w", err)
	}
	return nil
}
