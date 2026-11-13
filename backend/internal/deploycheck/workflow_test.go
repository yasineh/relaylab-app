package deploycheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type workflow struct {
	On          map[string]any `yaml:"on"`
	Permissions map[string]string
	Jobs        map[string]struct {
		Environment any               `yaml:"environment"`
		Permissions map[string]string `yaml:"permissions"`
		Steps       []struct {
			Name string            `yaml:"name"`
			If   string            `yaml:"if"`
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func loadWorkflow(t *testing.T, name string) workflow {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	var w workflow
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	return w
}

// 5.2: pushes, pull requests, schedules, and other workflows never start a
// deploy; it is manual, gated by the production environment, dry run by
// default, and touches the cloud only outside dry runs.
func TestDeployWorkflowIsManualAndGated(t *testing.T) {
	w := loadWorkflow(t, "deploy.yml")
	if len(w.On) != 1 {
		t.Fatalf("triggers %v: only workflow_dispatch is allowed", w.On)
	}
	dispatch, ok := w.On["workflow_dispatch"].(map[string]any)
	if !ok {
		t.Fatalf("triggers %v", w.On)
	}
	dry := dispatch["inputs"].(map[string]any)["dry_run"].(map[string]any)
	if dry["default"] != true || dry["type"] != "boolean" {
		t.Fatalf("dry_run input %v", dry)
	}
	if w.Permissions["contents"] != "read" || len(w.Permissions) != 1 {
		t.Fatalf("workflow permissions %v", w.Permissions)
	}
	job, ok := w.Jobs["deploy"]
	if !ok || len(w.Jobs) != 1 {
		t.Fatalf("jobs %v", w.Jobs)
	}
	if job.Environment != "production" {
		t.Fatalf("environment %v", job.Environment)
	}
	if job.Permissions["id-token"] != "write" {
		t.Fatal("Workload Identity Federation needs id-token: write (no stored keys)")
	}
	ciCheck, render := -1, -1
	for i, s := range job.Steps {
		if strings.Contains(s.Run, "actions/workflows/ci.yml/runs") {
			ciCheck = i
		}
		if strings.Contains(s.Run, "deploy/render.sh --check") {
			render = i
		}
		cloud := strings.Contains(s.Uses, "google-github-actions/") || strings.Contains(s.Run, "gcloud ") || strings.Contains(s.Run, "docker push")
		if cloud && s.If != "env.DRY_RUN != 'true'" {
			t.Errorf("step %q touches the cloud but also runs in dry runs", s.Name)
		}
		if s.Uses != "" && !strings.Contains(s.Uses, "@") || strings.Contains(s.Uses, "@v") {
			t.Errorf("step %q: actions must be pinned to a commit SHA, got %q", s.Name, s.Uses)
		}
	}
	if ciCheck < 0 || render < 0 {
		t.Fatal("missing the CI-status check or the offline manifest validation")
	}
	// Migrations run before the app is deployed, and traffic moves last.
	order := []string{"Migrate", "Deploy the app with no traffic", "Health check the new revision", "Move traffic to the new revision"}
	last := -1
	for _, name := range order {
		idx := -1
		for i, s := range job.Steps {
			if strings.HasPrefix(s.Name, name) {
				idx = i
			}
		}
		if idx <= last {
			t.Fatalf("step %q is missing or out of order", name)
		}
		last = idx
	}
}

// CI itself stays manual too (owner decision): no workflow runs on push.
func TestNoWorkflowRunsAutomatically(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	for _, f := range files {
		w := loadWorkflow(t, filepath.Base(f))
		for trigger := range w.On {
			if trigger != "workflow_dispatch" {
				t.Errorf("%s runs on %s", filepath.Base(f), trigger)
			}
		}
	}
}
