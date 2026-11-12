package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEnvExampleKeysAreRead: every variable in .env.example is read by
// something: the Go configuration (this package), the auth service, Docker
// Compose, or the deploy scripts. A key nothing reads is stale or misspelled.
func TestEnvExampleKeysAreRead(t *testing.T) {
	root := repoRoot(t)
	example, err := os.ReadFile(filepath.Join(root, "..", ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus strings.Builder
	add := func(path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		corpus.Write(b)
		corpus.WriteByte('\n')
	}
	goFiles, _ := filepath.Glob(filepath.Join(root, "internal", "platform", "config", "*.go"))
	for _, f := range goFiles {
		if !strings.HasSuffix(f, "_test.go") {
			add(f)
		}
	}
	add(filepath.Join(root, "..", "docker-compose.yml"))
	for _, dir := range []string{filepath.Join(root, "..", "auth", "src"), filepath.Join(root, "..", "deploy"), filepath.Join(root, "scripts")} {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && !strings.Contains(p, ".test.") {
				add(p)
			}
			return nil
		})
	}
	text := corpus.String()
	keys := regexp.MustCompile(`(?m)^#?\s?([A-Z][A-Z0-9_]+)=`).FindAllStringSubmatch(string(example), -1)
	if len(keys) < 20 {
		t.Fatalf("found only %d keys in .env.example", len(keys))
	}
	for _, k := range keys {
		if !regexp.MustCompile(`\b` + k[1] + `\b`).MatchString(text) {
			t.Errorf(".env.example has %s, but nothing reads it", k[1])
		}
	}
}
