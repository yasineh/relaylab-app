package outbound

import "runtime/debug"

// UserAgent is sent on every guarded request: RelayLab-Replay/<version>,
// where version is the build's VCS revision (first 12 characters) or "dev".
func UserAgent() string {
	v := "dev"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 12 {
				v = s.Value[:12]
			}
		}
	}
	return "RelayLab-Replay/" + v
}
