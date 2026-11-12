package outbound

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestSecurityDocLinksExistingTests: docs/security/outbound-replay.md maps
// each brief item (claude.md §8.2, 1-23) to tests. Every item must name at
// least one test, and every named test must exist.
func TestSecurityDocLinksExistingTests(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "docs", "security", "outbound-replay.md"))
	if err != nil {
		t.Fatal(err)
	}
	defined := map[string]bool{}
	decl := regexp.MustCompile(`(?m)^func (Test\w+)\(`)
	for _, dir := range []string{".", "ippolicy", filepath.Join("..", "..", "platform", "config")} {
		files, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
		for _, f := range files {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range decl.FindAllStringSubmatch(string(src), -1) {
				defined[m[1]] = true
			}
		}
	}
	row := regexp.MustCompile(`(?m)^\| (\d+) \|.*\| ([^|]*) \|$`)
	seen := map[int]bool{}
	for _, m := range row.FindAllStringSubmatch(string(doc), -1) {
		n, _ := strconv.Atoi(m[1])
		seen[n] = true
		names := strings.Split(m[2], ",")
		if len(names) == 0 || strings.TrimSpace(names[0]) == "" {
			t.Errorf("item %d names no test", n)
		}
		for _, name := range names {
			if name = strings.TrimSpace(name); !defined[name] {
				t.Errorf("item %d names %q, which is not a test", n, name)
			}
		}
	}
	for i := 1; i <= 23; i++ {
		if !seen[i] {
			t.Errorf("brief item %d is missing from the coverage table", i)
		}
	}
}
