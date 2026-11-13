package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"relaylab/internal/platform/config"
)

// Pools work through a transaction-mode pooler: no named prepared statements
// by default, unless the URL picks a mode itself. Pools connect lazily, so no
// database is needed.
func TestOpenUsesPoolerSafeExecMode(t *testing.T) {
	ctx := context.Background()
	p, err := Open(ctx, config.NewSecret("postgres://u:p@127.0.0.1:1/d"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if m := p.Config().ConnConfig.DefaultQueryExecMode; m != pgx.QueryExecModeCacheDescribe {
		t.Fatalf("default exec mode %v", m)
	}
	q, err := Open(ctx, config.NewSecret("postgres://u:p@127.0.0.1:1/d?default_query_exec_mode=exec"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if m := q.Config().ConnConfig.DefaultQueryExecMode; m != pgx.QueryExecModeExec {
		t.Fatalf("URL-chosen exec mode %v", m)
	}
	d, err := OpenDirect(ctx, config.NewSecret("postgres://u:p@127.0.0.1:1/d"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Config().MaxConns != 4 {
		t.Fatalf("direct pool size %d", d.Config().MaxConns)
	}
}
