package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultAPIBase and DefaultConsoleURL are set at build time via -ldflags for prod.
var DefaultAPIBase = "https://console.dev.outgate.ai/api"
var DefaultConsoleURL = "https://console.dev.outgate.ai"

const (
	configDirName  = ".og"
	credFileName   = "credentials.json"
	configFileName = "config.json"
)

// Credentials holds the stored CLI token.
type Credentials struct {
	Token     string `json:"token"`
	Email     string `json:"email,omitempty"`
	Name      string `json:"name,omitempty"`
	OrgID     string `json:"org_id,omitempty"`
	OrgName   string `json:"org_name,omitempty"`
	ExpiresAt string   `json:"expires_at,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
}

// Config holds user preferences.
type Config struct {
	APIBase    string `json:"api_base,omitempty"`
	ConsoleURL string `json:"console_url,omitempty"`
	RegionID   string `json:"region_id,omitempty"`
	RegionName string `json:"region_name,omitempty"`
}

// SaveConfig writes config to ~/.og/config.json.
func SaveConfig(cfg *Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(applyDefaults(cfg), "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, configFileName)
	return os.WriteFile(path, data, 0600)
}

// ActiveRegion returns the currently selected region ID and name.
func ActiveRegion() (id, name string) {
	cfg := LoadConfig()
	return cfg.RegionID, cfg.RegionName
}

// SetActiveRegion saves the active region to config.
func SetActiveRegion(id, name string) error {
	cfg := LoadConfig()
	cfg.RegionID = id
	cfg.RegionName = name
	return SaveConfig(cfg)
}

// Dir returns the path to ~/.og/, creating it if needed.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("cannot create config directory: %w", err)
	}
	return dir, nil
}

// SaveCredentials writes credentials to ~/.og/credentials.json.
func SaveCredentials(creds *Credentials) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, credFileName)
	return os.WriteFile(path, data, 0600)
}

// LoadCredentials reads credentials from ~/.og/credentials.json.
func LoadCredentials() (*Credentials, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, credFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

// DeleteCredentials removes the credentials file.
func DeleteCredentials() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, credFileName)
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// LoadConfig reads the config file, returning defaults if not found.
func LoadConfig() *Config {
	dir, err := Dir()
	if err != nil {
		return defaultConfig()
	}
	path := filepath.Join(dir, configFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultConfig()
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultConfig()
	}
	return applyDefaults(&cfg)
}

// APIBaseURL returns the effective API base URL, checking env var override.
func APIBaseURL() string {
	if v := os.Getenv("OG_API_BASE"); v != "" {
		return v
	}
	return LoadConfig().APIBase
}

// ConsoleURL returns the effective console URL, checking env var override.
func ConsoleURL() string {
	if v := os.Getenv("OG_CONSOLE_URL"); v != "" {
		return v
	}
	return LoadConfig().ConsoleURL
}

func defaultConfig() *Config {
	return &Config{
		APIBase:    DefaultAPIBase,
		ConsoleURL: DefaultConsoleURL,
	}
}

func applyDefaults(cfg *Config) *Config {
	if cfg.APIBase == "" {
		cfg.APIBase = DefaultAPIBase
	}
	if cfg.ConsoleURL == "" {
		cfg.ConsoleURL = DefaultConsoleURL
	}
	return cfg
}
