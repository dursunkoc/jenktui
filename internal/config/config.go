// Package config loads jenktui's hand-edited settings and persists Collections.
// No credentials are ever read from or written to disk.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/dursunkoc/jenktui/internal/branchfilter"
	toml "github.com/pelletier/go-toml/v2"
)

// ErrNotFound is returned by Load when the config file does not exist.
var ErrNotFound = errors.New("config file not found")

// Config holds settings from config.toml.
type Config struct {
	JenkinsURL         string `toml:"jenkins_url"`
	ParentJob          string `toml:"parent_job,omitempty"`
	Username           string `toml:"username,omitempty"`
	CACert             string `toml:"ca_cert,omitempty"`
	InsecureSkipVerify bool   `toml:"insecure_skip_verify"`
	// BranchFilter holds Branch Filter glob patterns (see package branchfilter).
	BranchFilter []string `toml:"branch_filter,omitempty"`
}

// Dir returns the jenktui config directory (~/.config/jenktui, or $JENKTUI_CONFIG_DIR).
func Dir() (string, error) {
	if d := os.Getenv("JENKTUI_CONFIG_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "jenktui"), nil
}

// Validate checks the config and normalises it (trims trailing slashes, slashes around ParentJob).
func (c *Config) Validate() error {
	c.JenkinsURL = strings.TrimRight(strings.TrimSpace(c.JenkinsURL), "/")
	if c.JenkinsURL == "" {
		return errors.New("jenkins_url is required")
	}
	u, err := url.Parse(c.JenkinsURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("jenkins_url %q must be an http(s) URL", c.JenkinsURL)
	}
	c.ParentJob = strings.Trim(strings.TrimSpace(c.ParentJob), "/")
	for i, p := range c.BranchFilter {
		if err := branchfilter.Validate(p); err != nil {
			return fmt.Errorf("branch_filter[%d]: %w", i, err)
		}
	}
	return nil
}

// Load reads and validates the config at path. Returns ErrNotFound if absent.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := toml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Save validates and writes the config to path, creating directories as needed.
func Save(path string, c *Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	return writeFile(path, b)
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
