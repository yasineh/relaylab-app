package retention

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"relaylab/internal/platform/config"
)

// TestDocumentedDefaultsMatchConfig: the settings table in
// docs/security/data-retention.md states the defaults LoadRetention applies.
func TestDocumentedDefaultsMatchConfig(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "security", "data-retention.md"))
	if err != nil {
		t.Fatal(err)
	}
	def, err := config.LoadRetention(func(string) string { return "" }, config.EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	dur := func(d time.Duration) string {
		s := d.String() // "168h0m0s", "10m0s"
		if strings.HasSuffix(s, "h0m0s") {
			return strings.TrimSuffix(s, "0m0s")
		}
		return strings.TrimSuffix(s, "0s")
	}
	want := map[string]string{
		"RETENTION_EVENT_TTL":              dur(def.EventTTL),
		"RETENTION_MAX_EVENTS_PER_PROJECT": "10000",
		"RETENTION_AUDIT_TTL":              dur(def.AuditTTL),
		"RETENTION_JOB_TTL":                dur(def.JobTTL),
		"RETENTION_INTERVAL":               dur(def.Interval),
	}
	if def.MaxEventsPerProject != 10000 {
		t.Fatalf("cap default %d; update this test and the doc", def.MaxEventsPerProject)
	}
	row := regexp.MustCompile("(?m)^\\| `(RETENTION_[A-Z_]+)` \\| ([0-9a-z]+)")
	seen := map[string]bool{}
	for _, m := range row.FindAllStringSubmatch(string(doc), -1) {
		seen[m[1]] = true
		if want[m[1]] != m[2] {
			t.Errorf("%s documented as %s, config default %s", m[1], m[2], want[m[1]])
		}
	}
	for k := range want {
		if !seen[k] {
			t.Errorf("%s missing from the settings table", k)
		}
	}
	// The batch size and run budget the doc states.
	if batchRows != 1000 || runBudget != 2*time.Minute {
		t.Fatal("doc states 1,000-row batches and a 2-minute budget")
	}
}
