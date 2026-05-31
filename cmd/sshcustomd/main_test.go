package main

import (
	"os"
	"testing"
)

// Smoke test — verify the daemon package compiles correctly.
func TestGetDefaultRoute(t *testing.T) {
	// Should not panic; may return empty strings on non-Android CI hosts.
	srcIP, iface, gw := getDefaultRoute()
	t.Logf("default route: srcIP=%q iface=%q gw=%q", srcIP, iface, gw)
}

func TestPatchSettingsINI(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/settings.ini"
	content := "ssh_host=\"\"\nssh_port=\"22\"\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := patchSettingsINI(path, map[string]string{"ssh_host": "test.example.com"}); err != nil {
		t.Fatalf("patchSettingsINI: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !contains(string(got), `ssh_host="test.example.com"`) {
		t.Errorf("expected patched value, got: %s", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
