package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Server timeouts (design: bootstrap-foundation). SSE routes opt out explicitly
// in later changes via http.ResponseController deadlines.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
)

// NewServer returns an http.Server with RelayLab's timeouts. maxHeaderBytes is
// the configured header limit; the HeaderLimit middleware enforces it precisely
// and the server value is a connection-level backstop.
func NewServer(addr string, h http.Handler, maxHeaderBytes int, logger *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
}

// Serve runs srv on ln until ctx is cancelled, then calls onShutdown (used to
// flip readiness to 503), stops accepting connections, and waits up to grace
// for in-flight requests to finish.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, grace time.Duration, onShutdown func()) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	if onShutdown != nil {
		onShutdown()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
