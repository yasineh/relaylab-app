package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
)

const defaultHealthURL = "http://127.0.0.1:8080/healthz"

// runHealthcheck probes a liveness URL on this host: 0 when it answers 200,
// 1 otherwise. Only loopback http URLs are accepted, so the probe can't be
// pointed elsewhere.
func runHealthcheck(args []string) int {
	target := defaultHealthURL
	if len(args) > 0 {
		target = args[0]
	}
	if err := probe(target, 3*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		return 1
	}
	return 0
}

func probe(target string, timeout time.Duration) error {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		return fmt.Errorf("only http://127.0.0.1 URLs are probed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil) //nolint:gosec // G704: only loopback http URLs pass the check above
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req) //nolint:gosec // G704: loopback only, see above
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	return nil
}
