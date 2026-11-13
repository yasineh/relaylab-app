package config

import "testing"

func TestLoadSite(t *testing.T) {
	s, err := LoadSite(replayEnv(nil))
	if err != nil || s.DemoVideoURL != "" || s.RepoURL != "" {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	s, err = LoadSite(replayEnv(map[string]string{
		"DEMO_VIDEO_URL": "https://youtu.be/demo123", "PUBLIC_REPO_URL": "https://github.com/example/relaylab/",
	}))
	if err != nil || s.DemoVideoURL != "https://youtu.be/demo123" || s.RepoURL != "https://github.com/example/relaylab" {
		t.Fatalf("set: %+v %v", s, err)
	}
	for _, bad := range []string{"http://youtu.be/x", "javascript:alert(1)", "https://user:pw@example.com/", "/relative"} {
		_, err := LoadSite(replayEnv(map[string]string{"DEMO_VIDEO_URL": bad}))
		assertFieldError(t, err, "DEMO_VIDEO_URL")
	}
}
