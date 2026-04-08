package config

import "os"

// ResolvedConfig is the final merged config from all sources.
// Resolution order (highest to lowest priority):
//   1. CLI flags (passed explicitly)
//   2. .og.yaml (local project config, walk-up search)
//   3. ~/.og/config.json (global user config)
//   4. Environment variables (OG_*)
//   5. Build-time defaults
type ResolvedConfig struct {
	APIBase    string
	Provider   string
	Project    string
	Share      string // share ID — pin to existing share, skip auto-create
	Region     string
	GatewayURL string // direct gateway URL for local/private regions
	Scan       ScanConfig
}

// ResolveInput holds the CLI flag values (may be empty).
type ResolveInput struct {
	APIBase    string
	Provider   string
	Project    string
	Region     string
	GatewayURL string
	StartDir   string // directory to start .og.yaml search from
}

// Resolve merges all config layers and returns the final resolved config.
func Resolve(input ResolveInput) *ResolvedConfig {
	// Start with defaults
	resolved := &ResolvedConfig{
		APIBase: DefaultAPIBase,
		Scan:    defaultScanConfig(),
	}

	// Layer 4: Global config (~/.og/config.json)
	global := LoadConfig()
	if global.APIBase != "" {
		resolved.APIBase = global.APIBase
	}
	if global.RegionID != "" {
		resolved.Region = global.RegionID
	}

	// Layer 3: Environment variables
	if v := os.Getenv("OG_API_BASE"); v != "" {
		resolved.APIBase = v
	}
	if v := os.Getenv("OG_PROVIDER"); v != "" {
		resolved.Provider = v
	}
	if v := os.Getenv("OG_PROJECT"); v != "" {
		resolved.Project = v
	}
	if v := os.Getenv("OG_SHARE"); v != "" {
		resolved.Share = v
	}
	if v := os.Getenv("OG_REGION"); v != "" {
		resolved.Region = v
	}
	if v := os.Getenv("OG_GATEWAY_URL"); v != "" {
		resolved.GatewayURL = v
	}

	// Layer 2: .og.yaml (local project config)
	startDir := input.StartDir
	if startDir == "" {
		startDir, _ = os.Getwd()
	}
	if startDir != "" {
		if projCfg, _ := FindProjectConfig(startDir); projCfg != nil {
			if projCfg.APIBase != "" {
				resolved.APIBase = projCfg.APIBase
			}
			if projCfg.Provider != "" {
				resolved.Provider = projCfg.Provider
			}
			if projCfg.Project != "" {
				resolved.Project = projCfg.Project
			}
			if projCfg.Share != "" {
				resolved.Share = projCfg.Share
			}
			if projCfg.Region != "" {
				resolved.Region = projCfg.Region
			}
			if projCfg.GatewayURL != "" {
				resolved.GatewayURL = projCfg.GatewayURL
			}
			// Merge scan config
			if projCfg.Scan != nil {
				if len(projCfg.Scan.Extensions) > 0 {
					resolved.Scan.Extensions = projCfg.Scan.Extensions
				}
				if len(projCfg.Scan.ExcludeDirs) > 0 {
					resolved.Scan.ExcludeDirs = append(resolved.Scan.ExcludeDirs, projCfg.Scan.ExcludeDirs...)
				}
				if len(projCfg.Scan.ExcludeFiles) > 0 {
					resolved.Scan.ExcludeFiles = projCfg.Scan.ExcludeFiles
				}
				if projCfg.Scan.MaxFileSize > 0 {
					resolved.Scan.MaxFileSize = projCfg.Scan.MaxFileSize
				}
				if projCfg.Scan.MaxContextTokens > 0 {
					resolved.Scan.MaxContextTokens = projCfg.Scan.MaxContextTokens
				}
				if projCfg.Scan.ContextMargin > 0 {
					resolved.Scan.ContextMargin = projCfg.Scan.ContextMargin
				}
				if projCfg.Scan.OverlapLines > 0 {
					resolved.Scan.OverlapLines = projCfg.Scan.OverlapLines
				}
			}
		}
	}

	// Layer 1: CLI flags (highest priority)
	if input.APIBase != "" {
		resolved.APIBase = input.APIBase
	}
	if input.Provider != "" {
		resolved.Provider = input.Provider
	}
	if input.Project != "" {
		resolved.Project = input.Project
	}
	if input.Region != "" {
		resolved.Region = input.Region
	}
	if input.GatewayURL != "" {
		resolved.GatewayURL = input.GatewayURL
	}

	return resolved
}

func defaultScanConfig() ScanConfig {
	return ScanConfig{
		Extensions: []string{
			".ts", ".js", ".py", ".go", ".rs", ".java",
			".yaml", ".yml", ".json", ".toml", ".ini",
			".env", ".cfg", ".conf", ".xml",
			".sh", ".bash", ".zsh", ".sql", ".md", ".txt",
			".rb", ".php", ".cs", ".kt", ".swift",
			".dockerfile", ".tf", ".hcl", ".properties",
		},
		ExcludeDirs: []string{
			"node_modules", ".git", "dist", "build",
			"vendor", "__pycache__", ".next", ".nuxt",
			"target", "out", ".cache", ".venv", "venv",
		},
		MaxFileSize: 1024 * 1024, // 1MB
	}
}
