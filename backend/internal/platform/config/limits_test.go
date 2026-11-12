package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLimitDefaults(t *testing.T) {
	l, err := LoadLimits(envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if l.HTTPMaxHeaderBytes != 32*1024 || l.APIMaxBodyBytes != 64*1024 {
		t.Fatalf("defaults = %+v", l)
	}
	if l.IngestMaxBodyBytes != 262144 || l.IngestMaxHeaderFields != 100 || l.IngestBodyTimeoutSeconds != 10 ||
		l.IngestRatePerEndpointPerMinute != 60 || l.IngestRateEndpointBurst != 30 ||
		l.IngestRatePerIPPerMinute != 120 || l.IngestRateIPBurst != 60 {
		t.Fatalf("ingest defaults = %+v", l)
	}
	if l.PageDefault != 50 || l.PageMax != 100 {
		t.Fatalf("page defaults = %+v", l)
	}
}

func TestPageDefaultMustNotExceedMax(t *testing.T) {
	_, err := LoadLimits(envFrom(map[string]string{"LIMIT_PAGE_DEFAULT": "80", "LIMIT_PAGE_MAX": "40"}))
	assertFieldError(t, err, "LIMIT_PAGE_DEFAULT")
	if _, err := LoadLimits(envFrom(map[string]string{"LIMIT_PAGE_DEFAULT": "40", "LIMIT_PAGE_MAX": "40"})); err != nil {
		t.Fatalf("equal default and max: %v", err)
	}
}

func TestIngestBodyLimitHardCap(t *testing.T) {
	if _, err := LoadLimits(envFrom(map[string]string{"LIMIT_INGEST_MAX_BODY_BYTES": "1048576"})); err != nil {
		t.Fatalf("1 MiB rejected: %v", err)
	}
	_, err := LoadLimits(envFrom(map[string]string{"LIMIT_INGEST_MAX_BODY_BYTES": "1048577"}))
	assertFieldError(t, err, "LIMIT_INGEST_MAX_BODY_BYTES")
}

// TestLimitGuardStillCatchesLiterals proves the duration and allowlist
// exemptions do not hide a real duplicated limit.
func TestLimitGuardStillCatchesLiterals(t *testing.T) {
	src := `package x
import "time"
const NameMaxChars = 100
const fields = 100
var d = 30 * time.Second
var body = 256 << 10`
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []int64
	ast.Inspect(f, func(n ast.Node) bool {
		if isDuration(n) {
			return false
		}
		if v, ok := constInt(n); ok {
			found = append(found, v)
			return false
		}
		return true
	})
	// The guard sees 100 (twice: the allowlist is per file, not global), and 256<<10, but not the duration.
	if len(found) != 3 || found[2] != 262144 {
		t.Fatalf("found %v", found)
	}
}

func TestLimitOverride(t *testing.T) {
	l, err := LoadLimits(envFrom(map[string]string{"LIMIT_HTTP_MAX_HEADER_BYTES": "16384"}))
	if err != nil {
		t.Fatal(err)
	}
	if l.HTTPMaxHeaderBytes != 16384 {
		t.Fatalf("override not applied: %+v", l)
	}
}

func TestLimitInvalidOverride(t *testing.T) {
	for _, v := range []string{"-1", "0", "abc", "1.5"} {
		_, err := LoadLimits(envFrom(map[string]string{"LIMIT_HTTP_MAX_HEADER_BYTES": v}))
		assertFieldError(t, err, "LIMIT_HTTP_MAX_HEADER_BYTES")
	}
	// Load surfaces limit errors too.
	env := validEnv()
	env["LIMIT_API_MAX_BODY_BYTES"] = "-1"
	_, err := Load(envFrom(env))
	assertFieldError(t, err, "LIMIT_API_MAX_BODY_BYTES")
}

func TestLimitSpecsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range LimitDocs() {
		if !strings.HasPrefix(d.Env, "LIMIT_") || seen[d.Env] || d.Default <= 0 || d.Doc == "" {
			t.Errorf("bad limit spec %+v", d)
		}
		seen[d.Env] = true
	}
}

// unrelatedConstants are named constants whose value happens to equal a limit
// default but which encode a different rule. Keyed by path relative to the
// module root.
var unrelatedConstants = map[string]map[string]bool{
	// Endpoint display-name length (also a database CHECK), not a header count.
	filepath.Join("internal", "endpoints", "service.go"): {"NameMaxChars": true},
	// Histogram bucket bounds in seconds; the 10 s top bound is not a timeout.
	filepath.Join("internal", "ingestion", "metrics.go"): {"durationBuckets": true},
	// Content type filter length, not a page size.
	filepath.Join("internal", "events", "service.go"): {"contentTypeFilterMaxChars": true},
	// Outbound (replay) response cap; the same value as the API body limit by coincidence.
	filepath.Join("internal", "platform", "config", "outbound.go"): {
		"defaultOutboundMaxResponseBytes": true, "maxOutboundResponseBytes": true,
	},
	// Outbound response header cap from the outbound-request-guard spec, not the inbound header limit.
	filepath.Join("internal", "replay", "outbound", "client.go"): {"MaxResponseHeaderBytes": true},
	// Test-helper retry count for shared-catalog DDL, not a stream limit.
	filepath.Join("internal", "platform", "db", "dbtest", "dbtest.go"): {"concurrentUpdateAttempts": true},
}

// TestNoScatteredLimitLiterals fails if a known limit default is hard-coded in
// Go code outside limits.go, so limits stay in one place (D17). Duration
// expressions such as 30*time.Second are not limits and are skipped, as are
// the named constants in unrelatedConstants.
func TestNoScatteredLimitLiterals(t *testing.T) {
	known := map[int64]string{}
	for _, d := range LimitDocs() {
		known[int64(d.Default)] = d.Env
	}
	root := repoRoot(t)
	fset := token.NewFileSet()
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, de fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if de.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
				filepath.Base(path) == "limits.go" {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			allowed := unrelatedConstants[rel]
			ast.Inspect(f, func(n ast.Node) bool {
				if isDuration(n) {
					return false
				}
				if vs, ok := n.(*ast.ValueSpec); ok && len(vs.Names) == 1 && allowed[vs.Names[0].Name] {
					return false
				}
				if v, ok := constInt(n); ok {
					if env, hit := known[v]; hit {
						t.Errorf("%s: literal %d duplicates %s; read it from config.Limits", fset.Position(n.Pos()), v, env)
					}
					return false
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// isDuration reports a multiplication with a time unit, such as 30*time.Second.
func isDuration(n ast.Node) bool {
	b, ok := n.(*ast.BinaryExpr)
	if !ok || b.Op != token.MUL {
		return false
	}
	for _, side := range []ast.Expr{b.X, b.Y} {
		if sel, ok := side.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "time" {
				return true
			}
		}
	}
	return false
}

// constInt evaluates integer literals and simple literal expressions like 32 << 10.
func constInt(n ast.Node) (int64, bool) {
	switch e := n.(type) {
	case *ast.BasicLit:
		if e.Kind != token.INT {
			return 0, false
		}
		v, err := strconv.ParseInt(strings.ReplaceAll(e.Value, "_", ""), 0, 64)
		return v, err == nil
	case *ast.ParenExpr:
		return constInt(e.X)
	case *ast.BinaryExpr:
		x, ok1 := constInt(e.X)
		y, ok2 := constInt(e.Y)
		if !ok1 || !ok2 {
			return 0, false
		}
		switch e.Op {
		case token.SHL:
			return x << uint(y), true
		case token.MUL:
			return x * y, true
		}
	}
	return 0, false
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
