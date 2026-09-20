// Package install merges Jev into client configuration without changing other
// servers. No shell commands are used and tokens are never printed.
package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const Placeholder = "REPLACE_WITH_YOUR_TYPESAFE_TOKEN"

type Config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid MCP URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return errors.New("remote MCP URLs require HTTPS; HTTP is allowed only for loopback")
	}
	return nil
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &cfg); err != nil {
		return cfg, errors.New("invalid client JSON configuration")
	}
	if err := ValidateURL(cfg.URL); err != nil {
		return cfg, err
	}
	if cfg.Token == "" || cfg.Token == Placeholder || len(cfg.Token) > 4096 {
		return cfg, errors.New("replace token in the client JSON file with your TypeSafe API key")
	}
	for _, c := range cfg.Token {
		if c < 33 || c > 126 {
			return cfg, errors.New("token must contain printable ASCII without spaces")
		}
	}
	return cfg, nil
}

func Init(path, endpoint string) error {
	if err := ValidateURL(endpoint); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(Config{URL: endpoint, Token: Placeholder}, "", "  ")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	return errors.Join(writeErr, closeErr)
}

type Result struct{ Path, Backup string }
type target struct {
	path      string
	old, data []byte
	exists    bool
}

func Merge(client string, original []byte, cfg Config) ([]byte, error) {
	doc := map[string]any{}
	original = bytes.TrimPrefix(original, []byte{0xef, 0xbb, 0xbf})
	if len(bytes.TrimSpace(original)) > 0 {
		var err error
		if client == "codex" {
			err = toml.Unmarshal(original, &doc)
		} else {
			err = json.Unmarshal(original, &doc)
		}
		if err != nil || doc == nil {
			return nil, errors.New("existing configuration is invalid; no changes made")
		}
	}
	key := "mcpServers"
	entry := map[string]any{"type": "http", "url": cfg.URL, "headers": map[string]any{"Authorization": "Bearer " + cfg.Token}}
	if client == "codex" {
		key = "mcp_servers"
		entry = map[string]any{"url": cfg.URL, "http_headers": map[string]any{"Authorization": "Bearer " + cfg.Token}}
	} else if client != "claude" {
		return nil, errors.New("client must be codex or claude")
	}
	servers := map[string]any{}
	if current, exists := doc[key]; exists {
		var ok bool
		servers, ok = current.(map[string]any)
		if !ok || servers == nil {
			return nil, errors.New("existing MCP server configuration is not an object")
		}
	}
	servers["jev"] = entry
	doc[key] = servers
	if client == "codex" {
		return toml.Marshal(doc)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	return append(data, '\n'), err
}

// Install preflights every target before writing, backs up changed originals,
// then uses atomic replacement. homeOverride makes isolated installs testable.
func Install(client string, cfg Config, homeOverride string) ([]Result, error) {
	if client != "codex" && client != "claude" && client != "all" {
		return nil, errors.New("client must be codex, claude or all")
	}
	userDir := homeOverride
	if userDir == "" {
		var err error
		userDir, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	codexDir := filepath.Join(userDir, ".codex")
	claudePath := filepath.Join(userDir, ".claude.json")
	if homeOverride == "" {
		if v := os.Getenv("CODEX_HOME"); v != "" {
			codexDir = v
		}
		if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
			claudePath = filepath.Join(v, ".claude.json")
		}
	}
	clients := []string{client}
	if client == "all" {
		clients = []string{"codex", "claude"}
	}
	var targets []target
	for _, name := range clients {
		path := claudePath
		if name == "codex" {
			path = filepath.Join(codexDir, "config.toml")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		lockPath := path + ".jev-lock"
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, fmt.Errorf("cannot lock %s; another installer may be running", path)
		}
		lock.Close()
		defer os.Remove(lockPath)
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("refusing non-regular config file: %s", path)
		}
		original, err := os.ReadFile(path)
		exists := err == nil
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		data, err := Merge(name, original, cfg)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		targets = append(targets, target{path, original, data, exists})
	}
	var results []Result
	for _, t := range targets {
		result := Result{Path: t.path}
		if bytes.Equal(t.old, t.data) {
			results = append(results, result)
			continue
		}
		if t.exists {
			result.Backup = t.path + ".backup-" + time.Now().UTC().Format("20060102T150405.000000000")
			if err := os.WriteFile(result.Backup, t.old, 0600); err != nil {
				return results, err
			}
		}
		// Detect edits from a client after preflight instead of overwriting them.
		latest, readErr := os.ReadFile(t.path)
		if (t.exists && readErr != nil) || (!t.exists && !os.IsNotExist(readErr)) || !bytes.Equal(latest, t.old) {
			return results, errors.New("configuration changed during installation; retry with clients closed")
		}
		if err := atomicWrite(t.path, t.data); err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".jev-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func Summary(results []Result) string {
	var out strings.Builder
	for _, r := range results {
		fmt.Fprintf(&out, "Configured: %s\n", r.Path)
		if r.Backup != "" {
			fmt.Fprintf(&out, "Backup: %s\n", r.Backup)
		}
	}
	return out.String()
}
