package outbound

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"relaylab/internal/replay/outbound/ippolicy"
)

// Resolver looks up a host name's addresses (A and AAAA).
// *net.Resolver satisfies it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Options configure a Client. Durations and sizes come from LIMIT_OUTBOUND_*.
type Options struct {
	AllowHTTP    bool
	AllowedPorts []int
	BlockedHosts []string
	// TestAllowlist is REPLAY_TEST_ALLOWLIST: exact "host:port" entries the
	// end-to-end stack may reach although internal. Config refuses it in
	// production. Empty everywhere else.
	TestAllowlist []string

	ConnectTimeout         time.Duration // TCP connect, and each DNS lookup
	TLSHandshakeTimeout    time.Duration
	ResponseHeaderTimeout  time.Duration
	TotalTimeout           time.Duration // the whole request, including the body read
	MaxResponseBytes       int           // response body cap (decompressed)
	MaxResponseHeaderBytes int           // default and spec value: MaxResponseHeaderBytes
	MaxConcurrency         int

	// UserAgent is sent on every request, e.g. "RelayLab-Replay/1.0".
	UserAgent string
	Logger    *slog.Logger
	Metrics   *Metrics // may be nil

	// Unexported test hooks (package tests only).
	resolver       Resolver
	rootCAs        *x509.CertPool
	allow          func(netip.AddrPort) bool
	isPublic       func(netip.Addr) bool
	acquireTimeout time.Duration
}

// Request is one user-directed request.
//
// Header holds headers derived from elsewhere (for replay, the original
// event's); unsafe ones are stripped silently (see strippedHeader).
// Explicit holds headers the user typed in for this request. They may set
// Authorization, but a forbidden name (Cookie, Proxy-Authorization, Host,
// hop-by-hop, forwarding; see explicitForbidden) fails the request with
// invalid_request instead of being dropped. Explicit headers replace Header
// entries of the same name.
type Request struct {
	Method   string
	URL      string
	Header   http.Header
	Explicit http.Header
	Body     []byte
}

// Result is the outcome of one guarded request. For Succeeded, Status and the
// (possibly truncated) Body describe the response; any HTTP status, including
// 3xx, 4xx, and 5xx, is a success. For failures only Category and Message are
// set, and Message is safe to show to users.
type Result struct {
	Category  Category
	Message   string
	Status    int
	Header    http.Header
	Body      []byte
	Truncated bool
	// Location is the sanitized Location header of a 3xx response, for
	// display. Redirects are never followed.
	Location string
	Duration time.Duration
}

// Client is the guarded outbound HTTP client. It is safe for concurrent use.
type Client struct {
	o        Options
	pol      policy
	resolver Resolver
	dialer   *net.Dialer
	http     *http.Client
	sem      chan struct{}
	active   atomic.Bool
	// testAddrs are socket addresses that allowlisted test names resolved
	// to; the dial-time check admits exactly these.
	testAddrs sync.Map // netip.AddrPort -> struct{}
	logger   *slog.Logger
}

// acquireTimeout is how long a request waits for a concurrency slot (spec).
const acquireTimeout = 2 * time.Second

// MaxResponseHeaderBytes caps response headers (spec: 32 KiB); larger
// headers fail with response_too_large.
const MaxResponseHeaderBytes = 32 << 10

// New builds a client and runs the activation self-check. A client whose
// self-check fails is inactive: Active reports false and Do sends nothing.
func New(o Options) *Client {
	c := &Client{o: o, logger: o.Logger}
	if c.logger == nil {
		c.logger = slog.New(slog.DiscardHandler)
	}
	c.pol = newPolicy(o.AllowHTTP, o.AllowedPorts, o.BlockedHosts, o.TestAllowlist)
	c.pol.allow = o.allow
	if o.isPublic != nil {
		c.pol.isPublic = o.isPublic
	}
	c.resolver = o.resolver
	if c.resolver == nil {
		c.resolver = &net.Resolver{}
	}
	if o.acquireTimeout <= 0 {
		c.o.acquireTimeout = acquireTimeout
	}
	if o.MaxResponseHeaderBytes <= 0 {
		c.o.MaxResponseHeaderBytes = MaxResponseHeaderBytes
	}
	c.sem = make(chan struct{}, max(o.MaxConcurrency, 1))
	c.dialer = &net.Dialer{Timeout: o.ConnectTimeout, Control: c.control}
	transport := &http.Transport{
		Proxy:                  nil, // never HTTP_PROXY / HTTPS_PROXY
		DialContext:            c.dial,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: o.rootCAs},
		TLSHandshakeTimeout:    o.TLSHandshakeTimeout,
		ResponseHeaderTimeout:  o.ResponseHeaderTimeout,
		MaxResponseHeaderBytes: int64(c.o.MaxResponseHeaderBytes),
		DisableKeepAlives:      true, // every request dials, so every request is validated
		ForceAttemptHTTP2:      true,
	}
	c.http = &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // report the 3xx; never follow it
		},
	}
	c.active.Store(c.selfCheck())
	if !c.active.Load() {
		c.logger.Error("outbound guard self-check failed; outbound requests are disabled")
	}
	if len(o.TestAllowlist) > 0 {
		c.logger.Warn("REPLAY_TEST_ALLOWLIST is set: these internal destinations are reachable (end-to-end tests only)",
			slog.Any("destinations", o.TestAllowlist))
	}
	return c
}

// Active reports whether the self-check passed. Dependent features (replay)
// must refuse to run when it is false.
func (c *Client) Active() bool { return c.active.Load() }

// allowedMethods are the methods a replay may use (the ones ingestion stores).
var allowedMethods = map[string]bool{
	"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "OPTIONS": true,
}

// Do sends one guarded request. It never panics on hostile input and always
// returns exactly one outcome.
func (c *Client) Do(ctx context.Context, req Request) Result {
	start := time.Now()
	res, host := c.do(ctx, req)
	res.Duration = time.Since(start)
	if res.Category != Succeeded {
		res.Message = messages[res.Category]
	}
	c.o.Metrics.observe(res.Category)
	// Category, host, and timing only: never header values or bodies.
	attrs := []any{
		slog.String("category", string(res.Category)),
		slog.String("method", req.Method),
		slog.Int64("duration_ms", res.Duration.Milliseconds()),
	}
	if host != "" {
		attrs = append(attrs, slog.String("host", host))
	}
	if res.Category == Succeeded {
		attrs = append(attrs, slog.Int("status", res.Status), slog.Bool("truncated", res.Truncated))
	}
	c.logger.Info("outbound request", attrs...)
	return res
}

func failed(e *Error) Result { return Result{Category: e.Category} }

func (c *Client) do(ctx context.Context, req Request) (Result, string) {
	if !c.active.Load() {
		return failed(fail(GuardInactive)), ""
	}
	if !allowedMethods[req.Method] {
		return failed(fail(InvalidRequest)), ""
	}
	dest, err := c.pol.validateURL(req.URL)
	if err != nil {
		return failed(err), logHost(req.URL)
	}
	host := dest.target.name
	if dest.target.isIP() {
		host = dest.target.ip.String()
	}
	header, herr := c.outboundHeader(req.Header, req.Explicit)
	if herr != nil {
		return failed(herr), host
	}

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-time.After(c.o.acquireTimeout):
		return failed(fail(CapacityExceeded)), host
	case <-ctx.Done():
		return failed(fail(CapacityExceeded)), host
	}

	ctx, cancel := context.WithTimeout(ctx, c.o.TotalTimeout)
	defer cancel()

	// Pre-flight resolution: fail early when any answer is non-public. The
	// dial repeats both the lookup and the check (see dial and control).
	if !dest.target.isIP() {
		if _, err := c.resolve(ctx, dest.target.name, dest.port); err != nil {
			return failed(err), host
		}
	}

	hreq, rerr := http.NewRequestWithContext(ctx, req.Method, dest.url.String(), bytes.NewReader(req.Body))
	if rerr != nil {
		return failed(fail(InvalidURL)), host
	}
	hreq.Header = header
	resp, derr := c.http.Do(hreq)
	if derr != nil {
		return failed(categorize(derr)), host
	}
	defer func() { _ = resp.Body.Close() }()

	body, truncated, berr := readCapped(resp.Body, c.o.MaxResponseBytes)
	if berr != nil {
		return failed(categorize(berr)), host
	}
	res := Result{
		Category: Succeeded, Status: resp.StatusCode, Header: resp.Header.Clone(),
		Body: body, Truncated: truncated,
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		res.Location = sanitizeLocation(resp.Header.Get("Location"))
	}
	return res, host
}

// readCapped reads at most limit bytes. The transport has already removed
// gzip encoding, so the cap applies to decompressed bytes; reading stops at
// the cap and the connection is closed rather than drained.
func readCapped(r io.Reader, limit int) ([]byte, bool, error) {
	b, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(b) > limit {
		return b[:limit], true, nil
	}
	return b, false, nil
}

// resolve looks up name and returns its addresses only when every answer may
// be reached: one non-public answer fails the whole lookup, so an attacker
// cannot mix public and internal answers.
func (c *Client) resolve(ctx context.Context, name string, port int) ([]netip.Addr, *Error) {
	lctx, cancel := context.WithTimeout(ctx, c.o.ConnectTimeout)
	defer cancel()
	addrs, err := c.resolver.LookupNetIP(lctx, "ip", name)
	if err != nil || len(addrs) == 0 {
		return nil, fail(DNSFailure)
	}
	testOnly := c.pol.testAllowed(name, port)
	out := make([]netip.Addr, len(addrs))
	for i, a := range addrs {
		a = ippolicy.Normalize(a)
		if testOnly {
			// An allowlisted test name: admit exactly what it resolves to, at this port.
			c.testAddrs.Store(netip.AddrPortFrom(a, uint16(port)), struct{}{}) //nolint:gosec // port validated 1-65535
		} else if !c.pol.addrAllowed(a, port) {
			return nil, fail(BlockedAddress)
		}
		out[i] = a
	}
	return out, nil
}

// dial resolves the host again at connection time and connects only to
// validated addresses. A DNS answer that changed since pre-flight (rebinding)
// is checked like any other; control then checks the exact socket address.
func (c *Client) dial(ctx context.Context, _, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fail(InvalidURL)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fail(InvalidURL)
	}
	var addrs []netip.Addr
	if ip, perr := netip.ParseAddr(host); perr == nil {
		if !c.pol.addrAllowed(ip, port) {
			return nil, fail(BlockedAddress)
		}
		addrs = []netip.Addr{ip}
	} else {
		var ferr *Error
		if addrs, ferr = c.resolve(ctx, host, port); ferr != nil {
			return nil, ferr
		}
	}
	var last error
	for _, a := range addrs {
		conn, err := c.dialer.DialContext(ctx, "tcp", netip.AddrPortFrom(a, uint16(port)).String()) //nolint:gosec // port validated 1-65535
		if err == nil {
			return conn, nil
		}
		last = err
		var ge *Error
		if errors.As(err, &ge) || ctx.Err() != nil {
			break
		}
	}
	return nil, last
}

// control runs on the socket the dialer is about to connect, with the exact
// destination address. It is the authoritative check: whatever produced the
// address, a non-public destination is never connected.
func (c *Client) control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fail(BlockedAddress)
	}
	ap = netip.AddrPortFrom(ippolicy.Normalize(ap.Addr()), ap.Port())
	if _, ok := c.testAddrs.Load(ap); ok {
		return nil
	}
	if !c.pol.addrAllowed(ap.Addr(), int(ap.Port())) {
		return fail(BlockedAddress)
	}
	return nil
}

// categorize maps a transport or body error to a failure category.
func categorize(err error) *Error {
	var ge *Error
	if errors.As(err, &ge) {
		return ge
	}
	var (
		certErr    *tls.CertificateVerificationError
		unknownCA  x509.UnknownAuthorityError
		invalidErr x509.CertificateInvalidError
		hostErr    x509.HostnameError
		alertErr   tls.AlertError
		recordErr  tls.RecordHeaderError
	)
	switch {
	case errors.As(err, &certErr), errors.As(err, &unknownCA), errors.As(err, &invalidErr),
		errors.As(err, &hostErr), errors.As(err, &alertErr), errors.As(err, &recordErr):
		return fail(TLSFailure)
	case strings.Contains(err.Error(), "server response headers exceeded"):
		return fail(ResponseTooLarge)
	case errors.Is(err, context.DeadlineExceeded):
		return fail(Timeout)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fail(Timeout)
	}
	return fail(ConnectFailure)
}

// sanitizeLocation keeps a redirect target for display only: no credentials,
// no control characters, bounded length.
func sanitizeLocation(loc string) string {
	if loc == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range loc {
		if r < ' ' || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	s := b.String()
	if i := strings.Index(s, "://"); i >= 0 {
		rest := s[i+3:]
		end := strings.IndexAny(rest, "/?#")
		if end < 0 {
			end = len(rest)
		}
		if at := strings.LastIndex(rest[:end], "@"); at >= 0 {
			s = s[:i+3] + rest[at+1:]
		}
	}
	if len(s) > MaxURLBytes {
		s = s[:MaxURLBytes]
	}
	return s
}

// logHost is the host name of a rejected URL for logs: lower-case and
// bounded. slog's JSON encoding escapes anything else, so it cannot inject
// log lines. It is empty when the URL does not parse.
func logHost(raw string) string {
	if len(raw) > MaxURLBytes {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	if len(h) > maxHostLen {
		h = h[:maxHostLen]
	}
	return h
}
