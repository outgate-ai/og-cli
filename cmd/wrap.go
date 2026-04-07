package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/outgate-ai/og-cli/api"
	"github.com/outgate-ai/og-cli/internal/config"
)

type toolConfig struct {
	// Provider
	upstreamURL       string
	forwardCallerAuth bool
	providerNameHint  string // e.g. "Anthropic" or "OpenAI"

	// Env vars to set
	baseURLEnv string // e.g. ANTHROPIC_BASE_URL or OPENAI_BASE_URL
	apiKeyEnv  string // e.g. ANTHROPIC_API_KEY or OPENAI_API_KEY

	// Binary name
	binary string
}

var tools = map[string]toolConfig{
	"claude": {
		upstreamURL:       "https://api.anthropic.com",
		forwardCallerAuth: true,
		providerNameHint:  "Anthropic",
		baseURLEnv:        "ANTHROPIC_BASE_URL",
		apiKeyEnv:         "ANTHROPIC_API_KEY",
		binary:            "claude",
	},
	"codex": {
		upstreamURL:       "https://api.openai.com",
		forwardCallerAuth: true,
		providerNameHint:  "OpenAI",
		baseURLEnv:        "OPENAI_BASE_URL",
		apiKeyEnv:         "OPENAI_API_KEY",
		binary:            "codex",
	},
}

func claudeCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "claude [args...]",
		Short:              "Run Claude Code through Outgate",
		Long: `Routes Claude Code traffic through Outgate. All arguments are passed directly to claude.

Use --provider <name-or-id> to select a specific provider (stripped from claude args).`,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
				return cmd.Help()
			}
			return wrapTool(cmd.Context(), "claude", args)
		},
	}
}

func codexCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "codex [args...]",
		Short:              "Run Codex through Outgate",
		Long: `Routes Codex traffic through Outgate. All arguments are passed directly to codex.

Use --provider <name-or-id> to select a specific provider (stripped from codex args).`,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
				return cmd.Help()
			}
			return wrapTool(cmd.Context(), "codex", args)
		},
	}
}

// extractOgFlags parses and removes og-specific flags from args.
// (Previously named extractGwFlags in the old gw CLI.)
func extractOgFlags(args []string) (providerID, projectName string, toolArgs []string) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--provider" && i+1 < len(args) {
			providerID = args[i+1]
			i++
		} else if strings.HasPrefix(args[i], "--provider=") {
			providerID = strings.TrimPrefix(args[i], "--provider=")
		} else if args[i] == "--name" && i+1 < len(args) {
			projectName = args[i+1]
			i++
		} else if strings.HasPrefix(args[i], "--name=") {
			projectName = strings.TrimPrefix(args[i], "--name=")
		} else {
			toolArgs = append(toolArgs, args[i])
		}
	}
	return
}

func wrapTool(ctx context.Context, toolName string, args []string) error {
	tc, ok := tools[toolName]
	if !ok {
		return fmt.Errorf("unknown tool: %s", toolName)
	}

	// Extract og-specific flags before passing rest to tool
	providerOverride, projectName, args := extractOgFlags(args)

	// Resolve config: CLI flags > .og.yaml > global config > env vars
	resolved := config.Resolve(config.ResolveInput{
		Provider: providerOverride,
		Project:  projectName,
	})
	if resolved.Provider != "" && providerOverride == "" {
		providerOverride = resolved.Provider
	}
	if resolved.Project != "" && projectName == "" {
		projectName = resolved.Project
	}

	creds, err := config.LoadCredentials()
	if err != nil || creds == nil || creds.Token == "" {
		return fmt.Errorf("not logged in — run 'og login' first")
	}

	// Need active region — .og.yaml takes precedence over global config
	regionID, regionName := config.ActiveRegion()
	if resolved.Region != "" {
		regionID = resolved.Region
		regionName = ""
	}
	if regionID == "" {
		client, err := api.NewClient(resolved.APIBase, creds.Token, creds.OrgID)
		if err != nil {
			return fmt.Errorf("failed to create client: %w", err)
		}
		regions, err := client.ListRegions(ctx)
		if err != nil || len(regions) == 0 {
			return fmt.Errorf("no regions available — create one at console.dev.outgate.ai")
		}
		regionID = regions[0].ID
		regionName = regions[0].Name
		_ = config.SetActiveRegion(regionID, regionName)
	}

	rc, err := api.NewClient(config.APIBaseURL(), creds.Token, creds.OrgID, regionID)
	if err != nil {
		return err
	}

	// Step 1: Find or create provider
	providers, err := rc.ListProviders(ctx)
	if err != nil {
		return fmt.Errorf("failed to list providers: %w", err)
	}

	var provider *api.Provider
	if providerOverride != "" {
		// Match by ID or name
		for i, p := range providers {
			if p.ID == providerOverride || strings.EqualFold(p.Name, providerOverride) {
				provider = &providers[i]
				break
			}
		}
		if provider == nil {
			return fmt.Errorf("provider '%s' not found — available providers:\n%s", providerOverride, listProviderNames(providers))
		}
	} else {
		// Auto-detect by name hint
		for i, p := range providers {
			if strings.Contains(strings.ToLower(p.Name), strings.ToLower(tc.providerNameHint)) {
				provider = &providers[i]
				break
			}
		}
		if provider == nil {
			resp, err := rc.CreateProvider(ctx, &api.CreateProviderRequest{
				Name:              tc.providerNameHint,
				URL:               tc.upstreamURL,
				ForwardCallerAuth: tc.forwardCallerAuth,
			})
			if err != nil {
				return fmt.Errorf("failed to create provider: %w", err)
			}
			provider = &api.Provider{ID: resp.ID, Name: resp.Name}
		}
	}

	// Step 2: Find or create share
	// Share name = [hostname] projectName — unique per machine
	dirName := projectName
	if dirName == "" {
		dirName = currentDirName()
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	shareName := fmt.Sprintf("[%s] %s", hostname, dirName)
	shares, err := rc.ListShares(ctx, provider.ID)
	if err != nil {
		return fmt.Errorf("failed to list shares: %w", err)
	}

	var shareEndpoint string
	var shareApiKey string
	var isAuthForwarding bool
	var shareID string
	for _, s := range shares {
		if s.Name == shareName {
			shareEndpoint = s.Endpoint
			shareApiKey = s.ApiKey
			isAuthForwarding = s.AuthForwarding
			shareID = s.ID
			break
		}
	}

	if shareEndpoint == "" {
		resp, err := rc.CreateShare(ctx, provider.ID, &api.CreateShareRequest{
			Name: shareName,
		})
		if err != nil {
			return fmt.Errorf("failed to create share: %w", err)
		}
		shareEndpoint = resp.Endpoint
		shareApiKey = resp.ApiKey
		isAuthForwarding = resp.AuthForwarding
		shareID = resp.ID

		// Cache the API key locally — it's only returned once at creation
		if shareApiKey != "" {
			_ = saveShareKey(shareID, shareApiKey)
		}
	}

	// Load cached API key if not in the list response (one-time reveal)
	if shareApiKey == "" && !isAuthForwarding && shareID != "" {
		shareApiKey = loadShareKey(shareID)
		if shareApiKey == "" {
			return fmt.Errorf("share '%s' exists but its API key is not cached on this machine.\n\nThe key was only shown when the share was first created.\nTo fix this, either:\n  1. Delete the share in the console and re-run (a new key will be generated)\n  2. Set %s manually in your environment", shareName, tc.apiKeyEnv)
		}
	}

	// Step 3: exec the tool with env vars
	binary, err := exec.LookPath(tc.binary)
	if err != nil {
		return fmt.Errorf("'%s' not found in PATH — install it first", tc.binary)
	}

	env := os.Environ()
	env = setEnv(env, tc.baseURLEnv, shareEndpoint)

	// For non-auth-forwarding shares, set the share's API key
	if !isAuthForwarding && shareApiKey != "" {
		env = setEnv(env, tc.apiKeyEnv, shareApiKey)
	}

	// exec replaces the current process
	argv := append([]string{tc.binary}, args...)
	return syscall.Exec(binary, argv, env)
}

func currentDirName() string {
	dir, err := os.Getwd()
	if err != nil {
		return "default"
	}
	return filepath.Base(dir)
}

func listProviderNames(providers []api.Provider) string {
	var lines []string
	for _, p := range providers {
		lines = append(lines, fmt.Sprintf("  %s (%s)", p.Name, p.ID))
	}
	return strings.Join(lines, "\n")
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// saveShareKey caches a share's API key to ~/.og/shares/{shareID}.key
func saveShareKey(shareID, apiKey string) error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	sharesDir := filepath.Join(dir, "shares")
	if err := os.MkdirAll(sharesDir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(sharesDir, shareID+".key"), []byte(apiKey), 0600)
}

// loadShareKey reads a cached share API key from ~/.og/shares/{shareID}.key
func loadShareKey(shareID string) string {
	dir, err := config.Dir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "shares", shareID+".key"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
