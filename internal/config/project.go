package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const projectConfigFile = ".og.yaml"

// ProjectConfig holds per-project settings from .og.yaml
type ProjectConfig struct {
	// Core
	Provider   string `yaml:"provider,omitempty"`    // provider name or ID
	Project    string `yaml:"project,omitempty"`     // project name (for share naming)
	Share      string `yaml:"share,omitempty"`       // share ID — pin to existing share, skip auto-create
	Region     string `yaml:"region,omitempty"`      // region ID override
	APIBase    string `yaml:"api_base,omitempty"`    // API base URL override
	GatewayURL string `yaml:"gateway_url,omitempty"` // direct gateway URL (e.g. http://localhost:8000 for local regions)

	// Scan settings
	Scan *ScanConfig `yaml:"scan,omitempty"`
}

// ScanConfig holds settings for the og scan command.
type ScanConfig struct {
	Extensions       []string `yaml:"extensions,omitempty"`         // file extensions to include
	ExcludeDirs      []string `yaml:"exclude_dirs,omitempty"`       // directories to skip
	ExcludeFiles     []string `yaml:"exclude_files,omitempty"`      // file patterns to skip
	MaxFileSize      int64    `yaml:"max_file_size,omitempty"`      // max file size in bytes (default 1MB)
	MaxContextTokens int      `yaml:"max_context_tokens,omitempty"` // guardrail model context limit (default 128000)
	ContextMargin    float64  `yaml:"context_margin,omitempty"`     // safety margin 0-1 (default 0.2 = 20%)
	OverlapLines     int      `yaml:"overlap_lines,omitempty"`      // line overlap between chunks (default 50)
}

// FindProjectConfig walks up from dir looking for .og.yaml.
// Returns the parsed config and the directory it was found in, or nil if not found.
func FindProjectConfig(startDir string) (*ProjectConfig, string) {
	dir, _ := filepath.Abs(startDir)

	for {
		path := filepath.Join(dir, projectConfigFile)
		if _, err := os.Stat(path); err == nil {
			cfg, err := loadProjectConfig(path)
			if err == nil {
				return cfg, dir
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached root
		}
		dir = parent
	}

	return nil, ""
}

func loadProjectConfig(path string) (*ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg ProjectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
