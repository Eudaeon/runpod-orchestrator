package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds the credentials needed to authenticate against RunPod.
//
// The orchestrator does not perform an interactive login. Instead, you grab two
// values once from a logged-in browser session on https://console.runpod.io:
//
//   - SessionID: the Clerk session id (looks like "sess_..."). It appears in the
//     URL of the token-minting request:
//     POST https://clerk.runpod.io/v1/client/sessions/<SessionID>/tokens
//   - ClientCookie: the value of the "__client" cookie sent to clerk.runpod.io.
//
// With these, the orchestrator can mint short-lived JWTs on demand.
type Config struct {
	SessionID    string `json:"session_id"`
	ClientCookie string `json:"client_cookie"`
}

// Environment variables that override the values from the config file.
const (
	EnvSessionID    = "RUNPOD_SESSION_ID"
	EnvClientCookie = "RUNPOD_CLIENT_COOKIE"
)

// DefaultPath returns the default config file location,
// e.g. ~/.config/runpod-orchestrator/config.json on Linux.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runpod-orchestrator", "config.json"), nil
}

// Load reads the config from path (or the default path when path is empty),
// then applies any environment-variable overrides. A missing config file is not
// an error as long as the required values are supplied via the environment.
func Load(path string) (*Config, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}

	cfg := &Config{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		// Fine: rely on environment variables.
	default:
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	if v := os.Getenv(EnvSessionID); v != "" {
		cfg.SessionID = v
	}
	if v := os.Getenv(EnvClientCookie); v != "" {
		cfg.ClientCookie = v
	}

	return cfg, nil
}

// Validate ensures the credentials required for authentication are present.
func (c *Config) Validate() error {
	var missing []string
	if c.SessionID == "" {
		missing = append(missing, "session_id ("+EnvSessionID+")")
	}
	if c.ClientCookie == "" {
		missing = append(missing, "client_cookie ("+EnvClientCookie+")")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing credentials: %v", missing)
	}
	return nil
}

// Save writes the config to path (or the default path when path is empty),
// creating parent directories as needed, with owner-only permissions.
func (c *Config) Save(path string) error {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
