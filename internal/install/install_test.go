package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergePreservesExistingServers(t *testing.T) {
	cfg := Config{URL: "https://mcp.example/mcp", Token: "secret"}
	codex, err := Merge("codex", []byte("model = \"gpt\"\n[mcp_servers.other]\nurl = \"https://other\"\n"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(codex)
	for _, want := range []string{"model =", "gpt", "mcp_servers.other", "mcp_servers.jev", "Bearer secret"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	claude, err := Merge("claude", []byte(`{"mcpServers":{"other":{"type":"http","url":"https://other"}}}`), cfg)
	if err != nil || !strings.Contains(string(claude), `"other"`) || !strings.Contains(string(claude), `"jev"`) {
		t.Fatalf("claude merge: %v %s", err, claude)
	}
}

func TestInstallAllCreatesFilesAndBacksUp(t *testing.T) {
	home := t.TempDir()
	cfg := Config{URL: "https://mcp.example/mcp", Token: "secret"}
	// Simulate an existing user config so the installer must create a backup.
	codexPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte("model = 'changed'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("all", cfg, home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("all", cfg, home); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(home, ".codex"))
	if err != nil {
		t.Fatal(err)
	}
	foundBackup := false
	for _, e := range entries {
		if strings.Contains(e.Name(), ".backup-") {
			foundBackup = true
		}
	}
	if !foundBackup {
		t.Fatal("install should create a backup")
	}
}

func TestValidateURL(t *testing.T) {
	for _, tc := range []struct {
		url string
		ok  bool
	}{
		{"https://example.com/mcp", true}, {"http://localhost:8080/mcp", true}, {"http://example.com/mcp", false}, {"https://example.com/mcp?token=x", false},
	} {
		if got := ValidateURL(tc.url) == nil; got != tc.ok {
			t.Errorf("ValidateURL(%q) = %v", tc.url, got)
		}
	}
}
