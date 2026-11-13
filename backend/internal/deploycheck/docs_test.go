package deploycheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docs/deployment.md says plainly that nothing is deployed, and holds no
// live URL and nothing secret-like, until deploy-public-demo verifies a
// deployment (spec: "Honest deployment documentation").
func TestDeploymentDocIsHonest(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "deployment.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	if !strings.Contains(doc, "**Status: RelayLab is not deployed.**") || !strings.Contains(doc, "There is no public demo URL") {
		t.Fatal("the not-deployed status is missing")
	}
	for _, m := range regexp.MustCompile(`https://[a-z0-9.-]+\.run\.app`).FindAllString(doc, -1) {
		if !strings.Contains(m, "xxxx") {
			t.Errorf("live-looking URL %s", m)
		}
	}
	if m := regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}|postgres://[^…<\s]*:[^…<\s]+@[a-z]`).FindString(doc); m != "" {
		t.Errorf("secret-like value %q", m)
	}
	for _, section := range []string{"## Topology", "## Configuration", "### Secrets", "## Database", "### Neon", "### Cloud SQL",
		"## Migrations", "## Deploy workflow", "## Rollback", "## Monitoring"} {
		if !strings.Contains(doc, section) {
			t.Errorf("missing section %q", section)
		}
	}
	// Every secret the manifests reference is documented.
	files, _ := filepath.Glob(filepath.Join(repoRoot(t), "deploy", "cloudrun", "*.yaml"))
	for _, f := range files {
		m, _ := os.ReadFile(f)
		for _, s := range regexp.MustCompile(`name: (relaylab-[a-z-]+)\n\s+key: latest`).FindAllStringSubmatch(string(m), -1) {
			if !strings.Contains(doc, s[1]) {
				t.Errorf("secret %s (in %s) is not documented", s[1], filepath.Base(f))
			}
		}
	}
}
