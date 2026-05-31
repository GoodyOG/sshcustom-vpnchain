package dnsx

import (
	"reflect"
	"testing"
)

func TestExtractIPv4s(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "host has address 1.2.3.4", []string{"1.2.3.4"}},
		{"multiple", "1.2.3.4 5.6.7.8", []string{"1.2.3.4", "5.6.7.8"}},
		{"deduplicates", "1.2.3.4\n1.2.3.4\n", []string{"1.2.3.4"}},
		{"ignores ipv6", "2001:db8::1 1.2.3.4", []string{"1.2.3.4"}},
		{"ignores invalid", "999.999.999.999 1.2.3.4", []string{"1.2.3.4"}},
		{"ping output", "PING example.com (93.184.216.34): 56 data bytes", []string{"93.184.216.34"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractIPv4s(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ExtractIPv4s(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestSanitizeIPv4List(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, nil},
		{"trims and dedupes", []string{" 1.2.3.4 ", "1.2.3.4"}, []string{"1.2.3.4"}},
		{"rejects ipv6", []string{"2001:db8::1", "1.2.3.4"}, []string{"1.2.3.4"}},
		{"rejects garbage", []string{"not-an-ip", "1.2.3.4"}, []string{"1.2.3.4"}},
		{"preserves order", []string{"5.6.7.8", "1.2.3.4"}, []string{"5.6.7.8", "1.2.3.4"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SanitizeIPv4List(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("SanitizeIPv4List(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeServers(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{}},
		{"adds port", []string{"1.1.1.1"}, []string{"1.1.1.1:53"}},
		{"keeps port", []string{"1.1.1.1:5353"}, []string{"1.1.1.1:5353"}},
		{"trims whitespace", []string{" 8.8.8.8 "}, []string{"8.8.8.8:53"}},
		{"dedupes", []string{"1.1.1.1", "1.1.1.1:53"}, []string{"1.1.1.1:53"}},
		{"skips empty", []string{"", "1.1.1.1"}, []string{"1.1.1.1:53"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeServers(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("NormalizeServers(%v) = %v, want %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("NormalizeServers(%v)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestRotateIPs(t *testing.T) {
	// Deterministic check: every rotation must be a permutation of input.
	in := []string{"a", "b", "c", "d"}
	got := RotateIPs(in)
	if len(got) != len(in) {
		t.Fatalf("RotateIPs changed length: %v -> %v", in, got)
	}
	seen := map[string]int{}
	for _, s := range got {
		seen[s]++
	}
	for _, s := range in {
		if seen[s] != 1 {
			t.Fatalf("RotateIPs(%v) = %v, %q appeared %d times", in, got, s, seen[s])
		}
	}
	// Single-element input is returned unchanged.
	one := []string{"only"}
	if got := RotateIPs(one); !reflect.DeepEqual(got, one) {
		t.Fatalf("RotateIPs(%v) = %v, want %v", one, got, one)
	}
}

func TestEvictHostFlushesAllVariants(t *testing.T) {
	// Seed the cache with two entries for the same host but different modes.
	cache(cacheKey(Config{Mode: ModeGoogle}, "example.com"), []string{"1.2.3.4"}, "google_dns")
	cache(cacheKey(Config{Mode: ModeCloudflare}, "example.com"), []string{"5.6.7.8"}, "cloudflare_dns")
	cache(cacheKey(Config{Mode: ModeGoogle}, "other.com"), []string{"9.9.9.9"}, "google_dns")

	EvictHost("example.com")

	hostCache.Lock()
	defer hostCache.Unlock()
	for k := range hostCache.entries {
		if k == cacheKey(Config{Mode: ModeGoogle}, "example.com") ||
			k == cacheKey(Config{Mode: ModeCloudflare}, "example.com") {
			t.Fatalf("EvictHost left stale entry: %s", k)
		}
	}
	// other.com must still be there.
	if _, ok := hostCache.entries[cacheKey(Config{Mode: ModeGoogle}, "other.com")]; !ok {
		t.Fatalf("EvictHost wrongly removed entry for unrelated host")
	}
}
