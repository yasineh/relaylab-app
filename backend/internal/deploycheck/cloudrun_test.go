package deploycheck

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// placeholders are the deploy-time values the manifests may use. A variable
// not listed here fails the check, so a new one gets documented.
var placeholders = map[string]string{
	"REGION":                  "europe-north1",
	"IMAGE_APP":               "europe-north1-docker.pkg.dev/example-project/relaylab/app@sha256:" + strings.Repeat("0", 64),
	"IMAGE_AUTH":              "europe-north1-docker.pkg.dev/example-project/relaylab/auth@sha256:" + strings.Repeat("1", 64),
	"APP_ORIGIN":              "https://relaylab-app-example.a.run.app",
	"APP_SERVICE_ACCOUNT":     "relaylab-app@example-project.iam.gserviceaccount.com",
	"WORKER_SERVICE_ACCOUNT":  "relaylab-worker@example-project.iam.gserviceaccount.com",
	"MIGRATE_SERVICE_ACCOUNT": "relaylab-migrate@example-project.iam.gserviceaccount.com",
	"TRUSTED_PROXY_CIDRS":     "",
	"REPLAY_ENABLED":          "false",
	"DEMO_ENABLED":            "true",
	"RETENTION_EVENT_TTL":     "24h",
	"DEMO_VIDEO_URL":          "",
	"PUBLIC_REPO_URL":         "",
	"SMTP_HOST":               "smtp.example.com",
	"SMTP_PORT":               "587",
	"SMTP_USER":               "relaylab",
	"MAIL_FROM":               "RelayLab <no-reply@example.com>",
}

var varRef = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// secretEnv are variables that must only ever come from Secret Manager.
var secretEnv = regexp.MustCompile(`(SECRET|_KEY$|PASSWORD|DATABASE_URL|_DIRECT_URL|MIGRATOR_URL|MIGRATION_DATABASE_URL)`)

type envVar struct {
	Name      string `yaml:"name"`
	Value     *string
	ValueFrom *struct {
		SecretKeyRef *struct {
			Name string `yaml:"name"`
			Key  string `yaml:"key"`
		} `yaml:"secretKeyRef"`
	} `yaml:"valueFrom"`
}

type probe struct {
	HTTPGet *struct {
		Path string `yaml:"path"`
		Port int    `yaml:"port"`
	} `yaml:"httpGet"`
	PeriodSeconds    int `yaml:"periodSeconds"`
	FailureThreshold int `yaml:"failureThreshold"`
}

// The schema below covers every field the manifests may use; strict
// decoding rejects anything else, so a misspelt key fails the check.
type resources struct {
	Limits map[string]string `yaml:"limits"`
}

type container struct {
	Name    string   `yaml:"name"`
	Image   string   `yaml:"image"`
	Command []string `yaml:"command"`
	Ports   []struct {
		Name          string `yaml:"name"`
		ContainerPort int    `yaml:"containerPort"`
	} `yaml:"ports"`
	Resources     *resources `yaml:"resources"`
	Env           []envVar   `yaml:"env"`
	StartupProbe  *probe     `yaml:"startupProbe"`
	LivenessProbe *probe     `yaml:"livenessProbe"`
}

type podSpec struct {
	ServiceAccountName   string      `yaml:"serviceAccountName"`
	TimeoutSeconds       int         `yaml:"timeoutSeconds"`
	ContainerConcurrency int         `yaml:"containerConcurrency"`
	MaxRetries           *int        `yaml:"maxRetries"`
	Containers           []container `yaml:"containers"`
}

type metadata struct {
	Name        string            `yaml:"name"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
}

type manifest struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   metadata `yaml:"metadata"`
	Spec       struct {
		Template struct {
			Metadata metadata `yaml:"metadata"`
			Spec     struct {
				podSpec   `yaml:",inline"`
				TaskCount int      `yaml:"taskCount"`
				Template  struct { // Jobs nest one level deeper
					Spec podSpec `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		} `yaml:"template"`
		Traffic []struct {
			LatestRevision bool `yaml:"latestRevision"`
			Percent        int  `yaml:"percent"`
		} `yaml:"traffic"`
	} `yaml:"spec"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	return filepath.Join(wd, "..", "..", "..")
}

// render substitutes placeholders the way deploy/render.sh does.
func render(t *testing.T, raw string) string {
	t.Helper()
	return varRef.ReplaceAllStringFunc(raw, func(m string) string {
		name := varRef.FindStringSubmatch(m)[1]
		v, ok := placeholders[name]
		if !ok {
			t.Errorf("unknown variable ${%s}: add it to the placeholders and docs/deployment.md", name)
		}
		return v
	})
}

func load(t *testing.T) map[string]manifest {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(repoRoot(t), "deploy", "cloudrun", "*.yaml"))
	if len(files) != 4 {
		t.Fatalf("found %d manifests, want 4", len(files))
	}
	out := map[string]manifest{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var m manifest
		dec := yaml.NewDecoder(strings.NewReader(render(t, string(b))))
		dec.KnownFields(true)
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("%s: %v", filepath.Base(f), err)
		}
		out[m.Metadata.Name] = m
	}
	return out
}

func containersOf(m manifest) []container {
	if m.Kind == "Job" {
		return m.Spec.Template.Spec.Template.Spec.Containers
	}
	return m.Spec.Template.Spec.Containers
}

func TestManifestsRenderAndValidate(t *testing.T) {
	ms := load(t)
	want := map[string]string{
		"relaylab-app": "Service", "relaylab-worker": "Service",
		"relaylab-migrate-auth": "Job", "relaylab-migrate-app": "Job",
	}
	for name, kind := range want {
		m, ok := ms[name]
		if !ok {
			t.Fatalf("missing %s", name)
		}
		if m.Kind != kind {
			t.Errorf("%s: kind %s", name, m.Kind)
		}
		if kind == "Service" && m.APIVersion != "serving.knative.dev/v1" || kind == "Job" && m.APIVersion != "run.googleapis.com/v1" {
			t.Errorf("%s: apiVersion %s", name, m.APIVersion)
		}
		if m.Metadata.Labels["cloud.googleapis.com/location"] == "" {
			t.Errorf("%s: no location label", name)
		}
		cs := containersOf(m)
		if len(cs) == 0 {
			t.Fatalf("%s: no containers", name)
		}
		for _, c := range cs {
			if c.Name == "" || len(c.Command) == 0 || !strings.Contains(c.Image, "@sha256:") {
				t.Errorf("%s/%s: name, command, and a digest image are required", name, c.Name)
			}
			envNames := map[string]bool{}
			for _, e := range c.Env {
				if envNames[e.Name] {
					t.Errorf("%s/%s: %s set twice", name, c.Name, e.Name)
				}
				envNames[e.Name] = true
				ref := e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil
				if secretEnv.MatchString(e.Name) && (!ref || e.Value != nil) {
					t.Errorf("%s/%s: %s must come from Secret Manager (secretKeyRef), never a literal", name, c.Name, e.Name)
				}
				if ref && (e.ValueFrom.SecretKeyRef.Name == "" || e.ValueFrom.SecretKeyRef.Key == "") {
					t.Errorf("%s/%s: %s has an incomplete secret reference", name, c.Name, e.Name)
				}
				if e.Value == nil && !ref {
					t.Errorf("%s/%s: %s has no value", name, c.Name, e.Name)
				}
			}
			if !envNames["APP_ENV"] {
				t.Errorf("%s/%s: APP_ENV not set", name, c.Name)
			}
		}
	}

	app := ms["relaylab-app"]
	if app.Spec.Template.Spec.TimeoutSeconds != 3600 {
		t.Error("app: request timeout must be 3600 s for live streams")
	}
	if app.Spec.Template.Metadata.Annotations["autoscaling.knative.dev/maxScale"] != "3" {
		t.Error("app: max instances must be 3 (per-instance rate limits)")
	}
	cs := containersOf(app)
	if len(cs) != 2 || cs[0].Name != "api" || cs[1].Name != "auth" {
		t.Fatal("app: containers must be api (ingress) then the auth sidecar")
	}
	if len(cs[0].Ports) != 1 || cs[0].Ports[0].ContainerPort != 8080 || len(cs[1].Ports) != 0 {
		t.Error("app: only the api container may expose a port")
	}
	if !slices.ContainsFunc(cs[0].Env, func(e envVar) bool {
		return e.Name == "AUTH_SERVICE_URL" && e.Value != nil && *e.Value == "http://localhost:3001"
	}) {
		t.Error("app: the api must reach auth on localhost:3001")
	}
	if !strings.Contains(app.Spec.Template.Metadata.Annotations["run.googleapis.com/container-dependencies"], `"api":["auth"]`) {
		t.Error("app: the api must start after auth")
	}
	for _, c := range cs {
		if c.StartupProbe == nil || c.StartupProbe.HTTPGet == nil || c.StartupProbe.HTTPGet.Path != "/readyz" {
			t.Errorf("app/%s: needs a /readyz startup probe", c.Name)
		}
	}

	w := ms["relaylab-worker"]
	if w.Metadata.Annotations["run.googleapis.com/ingress"] != "internal" {
		t.Error("worker: ingress must be internal")
	}
	a := w.Spec.Template.Metadata.Annotations
	if a["autoscaling.knative.dev/minScale"] != "1" || a["run.googleapis.com/cpu-throttling"] != "false" {
		t.Error("worker: min 1 instance with CPU always allocated")
	}
	if c := containersOf(w)[0]; c.Command[0] != "/usr/local/bin/worker" {
		t.Error("worker: wrong command")
	}

	if c := containersOf(ms["relaylab-migrate-auth"])[0]; c.Command[len(c.Command)-1] != "src/cli/migrate.ts" {
		t.Error("migrate-auth: must run the Better Auth migrations")
	}
	if c := containersOf(ms["relaylab-migrate-app"])[0]; c.Command[len(c.Command)-1] != "migrate" {
		t.Error("migrate-app: must run goose")
	}
}

// Rendered manifests contain no literal secret: nothing that looks like a
// key, token, or credentialed URL.
func TestManifestsContainNoSecrets(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(repoRoot(t), "deploy", "cloudrun", "*.yaml"))
	suspicious := regexp.MustCompile(`(?i)(postgres(ql)?://|[A-Za-z0-9+/]{40,}={0,2}|BEGIN [A-Z ]*PRIVATE KEY|sk_live_|AKIA[0-9A-Z]{16})`)
	for _, f := range files {
		b, _ := os.ReadFile(f)
		out := render(t, string(b))
		// Placeholder image digests are long hex, not base64 secrets.
		out = regexp.MustCompile(`@sha256:[0-9a-f]{64}`).ReplaceAllString(out, "")
		if m := suspicious.FindString(out); m != "" {
			t.Errorf("%s contains something secret-like: %q", filepath.Base(f), m)
		}
	}
}
