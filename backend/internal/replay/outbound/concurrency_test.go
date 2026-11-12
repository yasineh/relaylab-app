package outbound

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSaturationFailsWithCapacityExceeded(t *testing.T) {
	e := newEnv(t)
	e.opts.MaxConcurrency = 2
	e.opts.acquireTimeout = 200 * time.Millisecond
	var hits atomic.Int32
	release := make(chan struct{})
	_, port := e.server("example.com", func(_ http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	e.build()
	url := "https://example.com:" + port + "/"

	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { expectResult(t, e.do(http.MethodGet, url, nil, nil), Succeeded) })
	}
	deadline := time.Now().Add(2 * time.Second)
	for hits.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	expectResult(t, e.do(http.MethodGet, url, nil, nil), CapacityExceeded)
	if d := time.Since(start); d < 200*time.Millisecond || d > time.Second {
		t.Fatalf("waited %s for a slot, want about the acquire timeout", d)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("%d requests reached the server; the rejected one must not connect", n)
	}
	close(release)
	wg.Wait()
	// Slots are released: the next request succeeds.
	expectResult(t, e.do(http.MethodGet, url, nil, nil), Succeeded)
}

func TestDefaultAcquireTimeoutIsTwoSeconds(t *testing.T) {
	if c := New(newEnv(t).opts); c.o.acquireTimeout != 2*time.Second {
		t.Fatalf("acquire timeout %s", c.o.acquireTimeout)
	}
}
