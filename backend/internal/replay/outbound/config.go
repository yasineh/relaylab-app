package outbound

import (
	"log/slog"

	"relaylab/internal/platform/config"
)

// OptionsFrom builds client options from the worker's outbound configuration.
func OptionsFrom(c *config.Outbound, userAgent string, logger *slog.Logger, m *Metrics) Options {
	return Options{
		AllowHTTP:             c.AllowHTTP,
		AllowedPorts:          c.AllowedPorts,
		BlockedHosts:          c.BlockedHosts,
		TestAllowlist:         c.TestAllowlist,
		ConnectTimeout:        c.ConnectTimeout,
		TLSHandshakeTimeout:   c.TLSHandshakeTimeout,
		ResponseHeaderTimeout: c.ResponseHeaderTimeout,
		TotalTimeout:          c.TotalTimeout,
		MaxResponseBytes:      c.MaxResponseBytes,
		MaxConcurrency:        c.MaxConcurrency,
		UserAgent:             userAgent,
		Logger:                logger,
		Metrics:               m,
	}
}
