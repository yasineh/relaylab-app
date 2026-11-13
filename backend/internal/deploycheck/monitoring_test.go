package deploycheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var monitoringPlaceholders = map[string]string{
	"GCP_PROJECT":                "example-project",
	"APP_HOST":                   "relaylab-app-example.a.run.app",
	"UPTIME_CHECK_ID":            "relaylab-app-readyz-abc",
	"ALERT_NOTIFICATION_CHANNEL": "projects/example-project/notificationChannels/123",
}

// The optional monitoring templates render with placeholders and parse; the
// alert policies notify the configurable channel.
func TestMonitoringTemplatesRender(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(repoRoot(t), "deploy", "monitoring", "*.json"))
	if len(files) != 3 {
		t.Fatalf("found %d templates", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out := varRef.ReplaceAllStringFunc(string(b), func(m string) string {
			name := varRef.FindStringSubmatch(m)[1]
			v, ok := monitoringPlaceholders[name]
			if !ok {
				t.Errorf("%s: unknown variable %s", filepath.Base(f), name)
			}
			return v
		})
		var doc map[string]any
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%s: %v", filepath.Base(f), err)
		}
		if strings.HasPrefix(filepath.Base(f), "alert-") {
			ch, _ := doc["notificationChannels"].([]any)
			if len(ch) != 1 || ch[0] != monitoringPlaceholders["ALERT_NOTIFICATION_CHANNEL"] {
				t.Errorf("%s: notification channel %v", filepath.Base(f), doc["notificationChannels"])
			}
		}
	}
}
