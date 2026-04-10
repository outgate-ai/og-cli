package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

// credPathForEndpoint returns the credential file path for a given API base URL.
// Uses ~/.og/credentials/{hostname}.json for multi-endpoint support.
// Falls back to ~/.og/credentials.json for the default endpoint.
func credPathForEndpoint(apiBase string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}

	host := endpointHost(apiBase)
	defaultHost := endpointHost(DefaultAPIBase)

	// Other endpoints use ~/.og/credentials/{hostname}.json
	// Check hostname-specific file first, even for the default endpoint,
	// so that dev builds (where DefaultAPIBase == console.dev.outgate.ai)
	// can find credentials stored per-hostname by prior og login runs.
	if host != "" {
		credDir := filepath.Join(dir, "credentials")
		perHostPath := filepath.Join(credDir, host+".json")
		if _, statErr := os.Stat(perHostPath); statErr == nil {
			return perHostPath, nil
		}
	}

	// Default endpoint falls back to legacy path for backward compatibility
	if host == "" || host == defaultHost {
		return filepath.Join(dir, credFileName), nil
	}

	// Non-default endpoint: create the credentials dir and return the per-host path
	credDir := filepath.Join(dir, "credentials")
	if err := os.MkdirAll(credDir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(credDir, host+".json"), nil
}

// endpointHost extracts the hostname from an API base URL.
func endpointHost(apiBase string) string {
	if apiBase == "" {
		return ""
	}
	u, err := url.Parse(apiBase)
	if err != nil {
		// Fall back to simple extraction
		apiBase = strings.TrimPrefix(apiBase, "https://")
		apiBase = strings.TrimPrefix(apiBase, "http://")
		if idx := strings.Index(apiBase, "/"); idx >= 0 {
			apiBase = apiBase[:idx]
		}
		return apiBase
	}
	return u.Hostname()
}

// EffectiveAPIBase returns the current effective API base, considering .og.yaml.
func EffectiveAPIBase() string {
	resolved := Resolve(ResolveInput{})
	return resolved.APIBase
}

// SaveCredentials writes credentials for the effective API endpoint.
func SaveCredentials(creds *Credentials) error {
	return SaveCredentialsFor(EffectiveAPIBase(), creds)
}

// SaveCredentialsFor writes credentials for a specific API endpoint.
func SaveCredentialsFor(apiBase string, creds *Credentials) error {
	path, err := credPathForEndpoint(apiBase)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// LoadCredentials reads credentials for the effective API endpoint.
func LoadCredentials() (*Credentials, error) {
	return LoadCredentialsFor(EffectiveAPIBase())
}

// LoadCredentialsFor reads credentials for a specific API endpoint.
func LoadCredentialsFor(apiBase string) (*Credentials, error) {
	path, err := credPathForEndpoint(apiBase)
	if err != nil {
		return nil, err
	}
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

// DeleteCredentials removes credentials for the effective API endpoint.
func DeleteCredentials() error {
	return DeleteCredentialsFor(EffectiveAPIBase())
}

// DeleteCredentialsFor removes credentials for a specific API endpoint.
func DeleteCredentialsFor(apiBase string) error {
	path, err := credPathForEndpoint(apiBase)
	if err != nil {
		return err
	}
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

// APIBaseURL returns the effective API base URL.
// Resolution: env var > .og.yaml > global config > default.
func APIBaseURL() string {
	return EffectiveAPIBase()
}

// ConsoleURL returns the effective console URL, checking env var override.
func ConsoleURL() string {
	if v := os.Getenv("OG_CONSOLE_URL"); v != "" {
		return v
	}
	// Derive from effective API base — strip /api suffix
	apiBase := EffectiveAPIBase()
	if strings.HasSuffix(apiBase, "/api") {
		return strings.TrimSuffix(apiBase, "/api")
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
