package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

const testOverrideKey = "ZGV2LW9ubHktcmVwbGF5LW92ZXJyaWRlLWtleS0wMDM=" // "dev-only-replay-override-key-003"

func replayEnv(kv map[string]string) Getenv {
	return func(k string) string { return kv[k] }
}

func TestReplayDefaultsByEnvironment(t *testing.T) {
	dev, err := LoadReplay(replayEnv(map[string]string{"REPLAY_OVERRIDE_ENC_KEY": testOverrideKey}), EnvDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	want := Replay{
		Enabled: true, ProjectConcurrency: 2, ProjectQueueDepth: 20, UserRatePerMinute: 30, MaxTries: 3,
		LeaseSeconds: 60, MaxLeaseLosses: 3, ResponseExcerptBytes: 64 << 10,
	}
	got := *dev
	got.OverrideKey = Secret{}
	if got != want || len(dev.OverrideKey.Reveal()) != 32 {
		t.Fatalf("development defaults %+v", got)
	}
	// Production: off unless REPLAY_ENABLED=true; then the key is required.
	prod, err := LoadReplay(replayEnv(nil), EnvProduction)
	if err != nil || prod.Enabled {
		t.Fatalf("production default: %+v %v", prod, err)
	}
	if _, err := LoadReplay(replayEnv(map[string]string{"REPLAY_ENABLED": "true"}), EnvProduction); err == nil {
		t.Fatal("production enabled without a key")
	} else {
		assertFieldError(t, err, "REPLAY_OVERRIDE_ENC_KEY")
	}
	on, err := LoadReplay(replayEnv(map[string]string{"REPLAY_ENABLED": "true", "REPLAY_OVERRIDE_ENC_KEY": testOverrideKey}), EnvProduction)
	if err != nil || !on.Enabled {
		t.Fatalf("production enabled: %+v %v", on, err)
	}
	off, err := LoadReplay(replayEnv(map[string]string{"REPLAY_ENABLED": "false"}), EnvDevelopment)
	if err != nil || off.Enabled {
		t.Fatalf("development disabled: %+v %v", off, err)
	}
}

func TestReplayOverridesAndBounds(t *testing.T) {
	r, err := LoadReplay(replayEnv(map[string]string{
		"REPLAY_OVERRIDE_ENC_KEY": testOverrideKey, "REPLAY_PROJECT_QUEUE_DEPTH": "5", "REPLAY_MAX_TRIES": "1",
		"REPLAY_RESPONSE_EXCERPT_BYTES": "262144",
	}), EnvDevelopment)
	if err != nil || r.ProjectQueueDepth != 5 || r.MaxTries != 1 || r.ResponseExcerptBytes != 256<<10 {
		t.Fatalf("%+v %v", r, err)
	}
	for k, v := range map[string]string{
		"REPLAY_ENABLED":                "yes",
		"REPLAY_PROJECT_CONCURRENCY":    "0",
		"REPLAY_MAX_TRIES":              "11",
		"REPLAY_LEASE_SECONDS":          "5",
		"REPLAY_RESPONSE_EXCERPT_BYTES": "262145",
		"REPLAY_USER_RATE_PER_MINUTE":   "x",
		"REPLAY_OVERRIDE_ENC_KEY":       "c2hvcnQ=",
	} {
		env := map[string]string{"REPLAY_OVERRIDE_ENC_KEY": testOverrideKey, k: v}
		_, err := LoadReplay(replayEnv(env), EnvDevelopment)
		assertFieldError(t, err, k)
	}
}

// TestReplayLimitsNotHardCoded: replay code reads its limits from Replay.
// Defaults of 10 or more are checked as literals; the small ones (2, 3)
// appear too often in unrelated code to be checked this way.
func TestReplayLimitsNotHardCoded(t *testing.T) {
	known := map[int64]string{}
	for _, s := range replaySpecs {
		if s.def >= 10 {
			known[int64(s.def)] = s.env
		}
	}
	root := repoRoot(t)
	fset := token.NewFileSet()
	err := filepath.WalkDir(filepath.Join(root, "internal", "replay"), func(path string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if isDuration(n) {
				return false
			}
			if v, ok := constInt(n); ok {
				if env, hit := known[v]; hit {
					t.Errorf("%s: literal %d duplicates %s; read it from config.Replay", fset.Position(n.Pos()), v, env)
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
