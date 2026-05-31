package main

import (
	"reflect"
	"testing"
)

// Smoke tests for the pure functions that still live in package main. Once
// the connection-manager / SSHPool / transport layer gets pulled into its
// own package these will move alongside it. Until then, this file at least
// fences in the most-touched helpers against accidental regressions.

func TestNormalizeMode(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"   ":                 "",
		"direct":              "direct",
		"  Direct  ":          "direct",
		"ssh":                 "direct",
		"ssh+pl":              "direct",
		"http_proxy":          "http_proxy",
		"ssh+http":            "http_proxy",
		"ssh+pl+http":         "http_proxy",
		"tls_sni":             "tls_sni",
		"sni":                 "tls_sni",
		"ssh+sni":             "tls_sni",
		"http_proxy_tls_sni":  "http_proxy_tls_sni",
		"ssh+http+sni":        "http_proxy_tls_sni",
		"ssh+pl+http+sni":     "http_proxy_tls_sni",
		"unknown":             "",
	}
	for in, want := range cases {
		if got := normalizeMode(in); got != want {
			t.Errorf("normalizeMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeDNSMode(t *testing.T) {
	cases := map[string]string{
		"":           "device",
		"system":     "device",
		"DEVICE":     "device",
		"default":    "device",
		"google":     "google",
		"cloudflare": "cloudflare",
		"custom":     "custom",
		"garbage":    "device",
	}
	for in, want := range cases {
		if got := normalizeDNSMode(in); got != want {
			t.Errorf("normalizeDNSMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"   ":               "",
		"My Profile":        "my-profile",
		"---hyphens---":     "hyphens",
		"Special!Chars@123": "special-chars-123",
		"already-slug":      "already-slug",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractHTTPStatuses(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []int
	}{
		{"none", "no http here", nil},
		{"single", "HTTP/1.1 200 OK\r\n", []int{200}},
		{"multiple", "HTTP/1.1 302 Found\r\n\r\nHTTP/1.1 200 OK\r\n", []int{302, 200}},
		{"non-status text ignored", "Server: nginx\r\nHTTP/1.1 101 Switching Protocols\r\n", []int{101}},
		{"malformed status word skipped", "HTTP/1.1 abc OK\r\nHTTP/1.1 200 OK\r\n", []int{200}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractHTTPStatuses(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("extractHTTPStatuses(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestAllowedStatuses(t *testing.T) {
	cases := []struct {
		name    string
		got     []int
		allowed []int
		want    bool
	}{
		{"empty got", nil, []int{200}, true},
		{"empty allowed treated as any", []int{200}, nil, true},
		{"all in allowed", []int{200, 302}, []int{200, 302, 101}, true},
		{"one not allowed fails", []int{200, 500}, []int{200, 302}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := allowedStatuses(c.got, c.allowed); got != c.want {
				t.Errorf("allowedStatuses(%v, %v) = %v, want %v", c.got, c.allowed, got, c.want)
			}
		})
	}
}

func TestExtractSSHBanner(t *testing.T) {
	cases := map[string]string{
		"":                                       "",
		"no banner here":                         "",
		"SSH-2.0-OpenSSH_8.4\r\n":                "SSH-2.0-OpenSSH_8.4",
		"HTTP/1.1 200 OK\r\nSSH-2.0-dropbear\n":  "SSH-2.0-dropbear",
	}
	for in, want := range cases {
		if got := extractSSHBanner(in); got != want {
			t.Errorf("extractSSHBanner(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueProfileID(t *testing.T) {
	pf := &ProfilesFile{
		Profiles: []Profile{{ID: "main"}, {ID: "main-2"}},
	}
	// First-time fresh slug returns as-is.
	if got := uniqueProfileID(pf, "fresh"); got != "fresh" {
		t.Errorf("uniqueProfileID fresh = %q, want %q", got, "fresh")
	}
	// Existing slug gets a suffix beyond what's already taken.
	if got := uniqueProfileID(pf, "main"); got != "main-3" {
		t.Errorf("uniqueProfileID collision = %q, want %q", got, "main-3")
	}
}

func TestBuildTransportSetsTLSPort443When80OnTLSChain(t *testing.T) {
	// Regression test for the SNI-on-port-80 bug: a profile that selected
	// the http_proxy_tls_sni chain with proxy port 80 used to time out the
	// TLS handshake. buildTransport must auto-upgrade port 80 to 443 in
	// that case.
	ssh := SSH{Host: "example.com", Port: 80}
	hp := &HTTPProxyCfg{Host: "example.com", Port: 80, ConnectMethod: "socket"}
	tc := buildTransport("http_proxy_tls_sni", PayloadCfg{}, hp, nil, ssh)
	if tc.HTTPProxy == nil {
		t.Fatal("expected HTTPProxy in tls_sni mode")
	}
	if tc.HTTPProxy.Port != 443 {
		t.Errorf("expected proxy port auto-upgraded to 443, got %d", tc.HTTPProxy.Port)
	}
}
