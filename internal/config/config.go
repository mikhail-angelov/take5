// Package config is one JSON file (~/.config/take5/config.json) for every user-level
// setting this project can't reliably get any other way: a process Chrome spawns (native
// messaging -> take5 host -> render, detached) doesn't inherit the launching shell's
// environment (OPENROUTER_API_KEY, HTTPS_PROXY) or necessarily its PATH (a pipx-installed
// edge-tts lands in ~/.local/bin, not anywhere a Chrome-spawned process's PATH search or the
// usual Homebrew-prefix fallbacks already cover).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Dir is this project's config directory. Exported so callers that need to know where it is
// without loading it (e.g. a doctor check reporting "no config file yet") don't have to
// duplicate Dir's own home-directory resolution.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "take5"), nil
}

// Path is config.json's full path.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Config holds every field config.json can carry. All optional — a zero value means "not
// configured", handled by whichever fallback already existed before this file did (no proxy,
// PATH search, "you have to run `voice` by hand").
type Config struct {
	// OpenRouterAPIKey is the transcript-rewrite LLM's API key (cmd/take5's
	// -openrouter-key flag defaults to this, or OPENROUTER_API_KEY).
	OpenRouterAPIKey string `json:"openrouterApiKey,omitempty"`
	// Proxy is an http:// or https:// proxy URL for reaching OpenRouter (and anything else
	// this project calls out to). net/http only reads HTTP_PROXY/HTTPS_PROXY from the
	// environment, never ALL_PROXY, and has no built-in SOCKS5 support — so this needs to be
	// the scheme-specific http(s):// form of whatever proxy an interactive shell might
	// otherwise reach it through.
	Proxy string `json:"proxy,omitempty"`
	// EdgeTTSPath is the edge-tts executable's resolved absolute path, recorded by
	// `take5 setup-voice` after a pipx install.
	EdgeTTSPath string `json:"edgeTTSPath,omitempty"`
}

// Load reads config.json. A missing file is not an error — it means nothing has been
// configured yet, the normal state for most installs.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	// path is derived from os.UserHomeDir(), not attacker-controlled input.
	buf, readErr := os.ReadFile(path) //nolint:gosec // G304
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("read %s: %w", path, readErr)
	}
	var cfg Config
	if err := json.Unmarshal(buf, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// Update reads the current config, applies fn, and writes the result back — read-modify-write
// so, for example, `setup-voice` recording EdgeTTSPath can never clobber an OpenRouterAPIKey a
// human configured by hand, or vice versa.
func Update(fn func(*Config)) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	fn(&cfg)
	return save(cfg)
}

func save(cfg Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if mkdirErr := os.MkdirAll(dir, 0o750); mkdirErr != nil {
		return fmt.Errorf("create %s: %w", dir, mkdirErr)
	}
	buf, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(buf, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
