package outbound

import "testing"

func TestHostBlocklist(t *testing.T) {
	b := hostBlocklist{configured: []string{"relaylab.example.com", "ep-cool-1.eu.aws.neon.tech", "metadata.google.internal", "metadata"}}
	for _, raw := range []string{
		"localhost", "LOCALHOST.", "api.localhost", "postgres", "metadata", "worker.svc", "printer.local",
		"metadata.google.internal", "METADATA.google.internal.", "kubernetes.default.svc.cluster.local",
		"router.home.arpa", "box.localdomain", "x.internal",
		"relaylab.example.com", "Ingest.RelayLab.example.com", "ep-cool-1.eu.aws.neon.tech",
	} {
		tg, err := canonicalHost(raw)
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if !b.blocked(tg.name) {
			t.Errorf("%q not blocked", raw)
		}
	}
	for _, name := range []string{"hooks.example.com", "example.com", "notrelaylab.example.com", "localhost.example.com", "internal.example.com"} {
		if b.blocked(name) {
			t.Errorf("%q blocked", name)
		}
	}
}
