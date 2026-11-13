package outbound

import (
	"strings"
	"testing"
)

func testPolicy(allowHTTP bool) policy {
	ports := []int{443, 8443}
	if allowHTTP {
		ports = append(ports, 80)
	}
	return newPolicy(allowHTTP, ports, []string{"relaylab.example.com", "db.example.net", "metadata.google.internal", "metadata"}, nil)
}

func expectCategory(t *testing.T, p policy, raw string, want Category) {
	t.Helper()
	_, err := p.validateURL(raw)
	got := Succeeded
	if err != nil {
		got = err.Category
	}
	if got != want {
		t.Errorf("%q: %s, want %s", raw, got, want)
	}
}

func TestValidateURLCategories(t *testing.T) {
	p := testPolicy(false)
	cases := map[Category][]string{
		Succeeded: {
			"https://hooks.example.com/receive", "https://hooks.example.com:8443/x?y=1#frag",
			"https://8.8.8.8/", "https://[2606:4700::1111]/", "HTTPS://Hooks.Example.COM./x",
		},
		BlockedScheme: {
			"file:///etc/passwd", "gopher://example.com", "ftp://example.com", "ws://example.com",
			"data:text/plain,x", "//example.com", "example.com/x", "http://example.com", "javascript:alert(1)",
		},
		InvalidURL: {
			"https://admin:secret@example.com/", "https://user@example.com/", "https://exa mple.com/",
			"https://example.com/a\r\nb", "https://example.com/\x00", "https://example.com/\t", "https:///path",
			"https://", "https:example.com", "https://" + strings.Repeat("a", MaxURLBytes) + ".com",
			"https://2130706433/", "https://0x7f000001/", "https://0x7f.0.0.1/", "https://0177.0.0.1/",
			"https://017700000001/", "https://127.1/", "https://0/", "https://2852039166/", "https://[::1/",
			"https://example.com:99999/",
		},
		BlockedPort: {
			"https://example.com:5432", "https://example.com:6379", "https://example.com:22",
			"https://example.com:0", "https://example.com:80",
		},
		BlockedAddress: {
			"https://127.0.0.1/", "https://127.1.2.3/", "https://10.0.0.5/", "https://172.16.4.4/",
			"https://192.168.1.1/", "https://100.64.0.1/", "https://169.254.169.254/computeMetadata/v1/",
			"https://224.0.0.1/", "https://255.255.255.255/", "https://240.0.0.1/", "https://0.0.0.0/",
			"https://127.0.0.1./", "https://[::1]/", "https://[0:0:0:0:0:0:0:1]/", "https://[fd12:3456::1]/",
			"https://[fe80::1%25eth0]/", "https://[fd00:ec2::254]/", "https://[::ffff:127.0.0.1]/",
			"https://[::ffff:7f00:1]/", "https://[64:ff9b::a9fe:a9fe]/", "https://[2002:a9fe:a9fe::]/",
		},
		BlockedHost: {
			"https://metadata.google.internal/computeMetadata/v1/", "https://METADATA.google.internal./",
			"https://localhost/", "https://api.localhost/", "https://postgres/", "https://worker.svc/",
			"https://printer.local/", "https://metadata/", "https://relaylab.example.com/h/x",
			"https://in.relaylab.example.com/", "https://db.example.net/",
		},
	}
	for want, urls := range cases {
		for _, u := range urls {
			expectCategory(t, p, u, want)
		}
	}
}

func TestValidateURLWithHTTPEnabled(t *testing.T) {
	p := testPolicy(true)
	expectCategory(t, p, "http://example.com/", Succeeded)
	expectCategory(t, p, "http://example.com:8080/", BlockedPort)
	expectCategory(t, p, "http://127.0.0.1/", BlockedAddress)
	expectCategory(t, p, "ws://example.com/", BlockedScheme)
}

func TestValidateURLDropsFragment(t *testing.T) {
	d, err := testPolicy(false).validateURL("https://example.com/a?b=1#secret")
	if err != nil || d.url.String() != "https://example.com/a?b=1" || d.port != 443 || d.target.name != "example.com" {
		t.Fatalf("%v %v", d, err)
	}
}
