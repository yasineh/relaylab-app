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

// TestNoScatteredLimitLiterals fails if a known limit default is hard-coded in
// Go code outside limits.go, so limits stay in one place (D17).
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
			ast.Inspect(f, func(n ast.Node) bool {
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
