package doccheck

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Every documentation page is reachable from docs/README.md, and the
// decision index lists D1–D20 (spec: "Decisions indexed").
func TestDocsIndexLinksEveryPage(t *testing.T) {
	root := repoRoot(t)
	index, err := os.ReadFile(filepath.Join(root, "docs", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range docFiles(t) {
		rel, _ := filepath.Rel(filepath.Join(root, "docs"), f)
		if strings.HasPrefix(rel, "..") || rel == "README.md" || strings.HasPrefix(rel, "decisions/0") {
			continue // the root README, the index itself, and records listed by decisions/README.md
		}
		if !strings.Contains(string(index), "("+rel+")") {
			t.Errorf("docs/README.md does not link %s", rel)
		}
	}
	decisions, err := os.ReadFile(filepath.Join(root, "docs", "decisions", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 20; i++ {
		if !strings.Contains(string(decisions), "[D"+strconv.Itoa(i)+"](") {
			t.Errorf("decisions/README.md does not list D%d", i)
		}
	}
	readme, _ := os.ReadFile(filepath.Join(root, "README.md"))
	for _, want := range []string{"Not deployed yet", "(docs/README.md)", "(docs/architecture/overview.md)", "(docs/architecture/erd.md)", "(docs/demo/relaylab-demo.webm)", "(docs/demo/walkthrough.md)"} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README.md lacks %q", want)
		}
	}
}
