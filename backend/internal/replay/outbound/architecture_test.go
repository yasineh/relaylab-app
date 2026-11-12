package outbound

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// forbiddenOutbound lists net/http and net identifiers that send requests or
// open connections without the guard.
var forbiddenOutbound = map[string]map[string]bool{
	"net/http": {
		"Client": true, "Transport": true, "DefaultClient": true, "DefaultTransport": true,
		"Get": true, "Head": true, "Post": true, "PostForm": true, "NewRequest": false,
	},
	"net": {"Dial": true, "DialTimeout": true, "Dialer": true, "DialTCP": true, "DialUDP": true, "DialIP": true},
}

// outboundViolations reports uses of forbidden identifiers in one Go file.
func outboundViolations(fset *token.FileSet, f *ast.File) []string {
	names := map[string]string{} // local import name -> path
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if _, watched := forbiddenOutbound[path]; !watched {
			continue
		}
		name := filepath.Base(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = path
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if path, ok := names[pkg.Name]; ok && forbiddenOutbound[path][sel.Sel.Name] {
			out = append(out, fset.Position(sel.Pos()).String()+": "+path+"."+sel.Sel.Name)
		}
		return true
	})
	return out
}

// TestOnlyOutboundSendsReplayRequests: every package under internal/replay
// other than outbound must send through outbound.Client.
func TestOnlyOutboundSendsReplayRequests(t *testing.T) {
	root, err := filepath.Abs("..") // internal/replay
	if err != nil {
		t.Fatal(err)
	}
	self, _ := filepath.Abs(".")
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == self || strings.HasPrefix(path, self+string(filepath.Separator)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, v := range outboundViolations(fset, f) {
			t.Errorf("%s: use outbound.Client instead", v)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestArchitectureCheckDetectsViolations proves the check above would fire.
func TestArchitectureCheckDetectsViolations(t *testing.T) {
	src := `package jobs
import (
	"net"
	h "net/http"
)
func a() { _ = &h.Client{}; _ = h.Transport{}; _, _ = h.Get("x"); _ = h.DefaultClient; _ = net.Dialer{} }
func b() { _, _ = h.NewRequest("GET", "x", nil) }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "jobs.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := outboundViolations(fset, f)
	if len(got) != 5 {
		t.Fatalf("found %d violations, want 5: %v", len(got), got)
	}
}
