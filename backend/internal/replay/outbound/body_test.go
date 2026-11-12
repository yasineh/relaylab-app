package outbound

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestLargeBodyTruncated(t *testing.T) {
	e := newEnv(t)
	_, port := e.server("example.com", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		chunk := bytes.Repeat([]byte("a"), 1<<20)
		for range 10 { // 10 MiB
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	e.build()
	r := e.do(http.MethodGet, "https://example.com:"+port+"/", nil, nil)
	expectResult(t, r, Succeeded)
	if r.Status != http.StatusCreated || !r.Truncated || len(r.Body) != e.opts.MaxResponseBytes {
		t.Fatalf("status %d truncated %v len %d", r.Status, r.Truncated, len(r.Body))
	}
}

func TestBodyAtCapIsNotTruncated(t *testing.T) {
	e := newEnv(t)
	_, port := e.server("example.com", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), 64<<10))
	})
	e.build()
	r := e.do(http.MethodGet, "https://example.com:"+port+"/", nil, nil)
	if r.Truncated || len(r.Body) != 64<<10 {
		t.Fatalf("truncated %v len %d", r.Truncated, len(r.Body))
	}
}

// TestGzipBombCappedOnDecompressedBytes: a small gzip stream that would expand
// to 1 GiB. Reading stops at the cap of decompressed bytes and the connection
// is closed, so the server never gets far.
func TestGzipBombCappedOnDecompressedBytes(t *testing.T) {
	e := newEnv(t)
	var sentPlain atomic.Int64
	var sawAcceptEncoding atomic.Value
	_, port := e.server("example.com", func(w http.ResponseWriter, r *http.Request) {
		sawAcceptEncoding.Store(r.Header.Get("Accept-Encoding"))
		w.Header().Set("Content-Encoding", "gzip")
		gz, _ := gzip.NewWriterLevel(w, gzip.BestCompression)
		zeros := make([]byte, 1<<20)
		for range 1024 { // 1 GiB of zeros, compressed ~1000:1
			if _, err := gz.Write(zeros); err != nil {
				return
			}
			sentPlain.Add(int64(len(zeros)))
		}
		_ = gz.Close()
	})
	e.build()
	start := time.Now()
	// A caller-supplied Accept-Encoding is dropped, so the transport
	// negotiates and decodes gzip itself and the cap sees decoded bytes.
	r := e.do(http.MethodGet, "https://example.com:"+port+"/", http.Header{"Accept-Encoding": {"identity"}}, nil)
	expectResult(t, r, Succeeded)
	if !r.Truncated || len(r.Body) != e.opts.MaxResponseBytes || !bytes.Equal(r.Body, make([]byte, len(r.Body))) {
		t.Fatalf("truncated %v len %d", r.Truncated, len(r.Body))
	}
	if ae, _ := sawAcceptEncoding.Load().(string); ae != "gzip" {
		t.Fatalf("server saw Accept-Encoding %q, want the transport's gzip", ae)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %s", d)
	}
	time.Sleep(100 * time.Millisecond)
	if n := sentPlain.Load(); n >= 1<<30 {
		t.Fatalf("server produced the whole bomb (%d bytes)", n)
	}
}
