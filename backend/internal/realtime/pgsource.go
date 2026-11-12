package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"relaylab/internal/platform/config"
)

// Channel is the NOTIFY channel ingestion publishes to.
const Channel = "events"

// ListenerApplicationName identifies the listening connection in
// pg_stat_activity (operators and tests use it).
const ListenerApplicationName = "relaylab-event-listener"

const (
	// idleCheck is how long to wait for a notification before pinging, so a
	// silently dead connection is noticed.
	idleCheck   = 30 * time.Second
	pingTimeout = 5 * time.Second
)

// PGSource listens on a dedicated connection outside the pool.
type PGSource struct {
	cfg    *pgx.ConnConfig
	logger *slog.Logger
}

// NewPGSource returns a Source for the runtime database URL.
func NewPGSource(dsn config.Secret, logger *slog.Logger) (*PGSource, error) {
	cfg, err := pgx.ParseConfig(dsn.Reveal())
	if err != nil {
		return nil, errors.New("invalid database configuration") // never echo the DSN
	}
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams["application_name"] = ListenerApplicationName
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &PGSource{cfg: cfg, logger: logger}, nil
}

// Listen implements Source.
func (p *PGSource) Listen(ctx context.Context, connected func(), deliver func(Notification)) error {
	conn, err := pgx.ConnectConfig(ctx, p.cfg)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pingTimeout)
		defer cancel()
		_ = conn.Close(cctx)
	}()
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{Channel}.Sanitize()); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	connected()
	for {
		wctx, cancel := context.WithTimeout(ctx, idleCheck)
		n, err := conn.WaitForNotification(wctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(wctx.Err(), context.DeadlineExceeded) {
				pctx, pcancel := context.WithTimeout(ctx, pingTimeout)
				err = conn.Ping(pctx)
				pcancel()
				if err == nil {
					continue
				}
			}
			return err
		}
		msg, ok := parsePayload(n.Payload)
		if !ok {
			// Payloads are written by ingestion only; never log the content.
			p.logger.Warn("ignored malformed event notification", slog.Int("bytes", len(n.Payload)))
			continue
		}
		deliver(msg)
	}
}

func parsePayload(s string) (Notification, bool) {
	var raw struct {
		ProjectID string `json:"project_id"`
		EventID   string `json:"event_id"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return Notification{}, false
	}
	p, err1 := uuid.Parse(raw.ProjectID)
	e, err2 := uuid.Parse(raw.EventID)
	if err1 != nil || err2 != nil || p == uuid.Nil || e == uuid.Nil {
		return Notification{}, false
	}
	return Notification{ProjectID: p, EventID: e}, true
}
