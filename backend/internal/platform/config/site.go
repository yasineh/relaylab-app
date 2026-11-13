package config

import (
	"net/url"
	"strings"
)

// Site holds optional public links for the landing page (add-landing-page).
// Each is shown only when set, so the page never offers a dead link.
type Site struct {
	// DemoVideoURL is DEMO_VIDEO_URL: the published product demo video.
	DemoVideoURL string
	// RepoURL is PUBLIC_REPO_URL: the public source repository. Leave it
	// unset while the repository is private; the documentation link is
	// derived from it too.
	RepoURL string
}

// LoadSite reads DEMO_VIDEO_URL and PUBLIC_REPO_URL. Both must be absolute
// https URLs without credentials.
func LoadSite(getenv Getenv) (*Site, error) {
	s := &Site{}
	for _, f := range []struct {
		env string
		dst *string
	}{{"DEMO_VIDEO_URL", &s.DemoVideoURL}, {"PUBLIC_REPO_URL", &s.RepoURL}} {
		raw := strings.TrimSpace(getenv(f.env))
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return nil, &FieldError{Var: f.env, Reason: "must be an https URL"}
		}
		*f.dst = strings.TrimSuffix(u.String(), "/")
	}
	return s, nil
}
