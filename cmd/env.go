package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/outgate-ai/og-cli/api"
	"github.com/outgate-ai/og-cli/internal/config"
)

func envCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env <tool>",
		Short: "Print environment variables for routing a tool through Outgate",
		Long: `Prints export statements that configure a tool to route through Outgate.
Use with eval to set them in your current shell:

  eval $(og env claude)
  eval $(og env codex)
  eval $(og env claude --name my-project)

Add to .zshrc or VS Code terminal profile for persistent routing:

  if command -v og &>/dev/null; then
    eval $(og env claude 2>/dev/null)
  fi

Use --provider <name-or-id> to select a specific provider.
Use --name <project> to set a custom share name (defaults to current directory).

Configuration is resolved in order: CLI flags > .og.yaml > ~/.og/config.json > env vars > defaults.`,
		Args: cobra.ExactArgs(1),
		RunE: envHandler,
	}
	cmd.Flags().String("name", "", "Custom project/share name (defaults to current directory)")
	cmd.Flags().String("provider", "", "Provider name or ID")
	return cmd
}

func envHandler(cmd *cobra.Command, args []string) error {
	toolName := strings.ToLower(args[0])
	tc, ok := tools[toolName]
	if !ok {
		return fmt.Errorf("unknown tool: %s (supported: claude, codex)", toolName)
	}

	providerFlag, _ := cmd.Flags().GetString("provider")
	projectFlag, _ := cmd.Flags().GetString("name")

	// Resolve config: CLI flags > .og.yaml > global config > env vars
	resolved := config.Resolve(config.ResolveInput{
		Provider: providerFlag,
		Project:  projectFlag,
	})
	if resolved.Provider != "" && providerFlag == "" {
		providerFlag = resolved.Provider
	}
	if resolved.Project != "" && projectFlag == "" {
		projectFlag = resolved.Project
	}

	creds, err := config.LoadCredentialsFor(resolved.APIBase)
	if err != nil || creds == nil || creds.Token == "" {
		fmt.Println("# og: not logged in — run 'og login' first")
		return nil
	}

	ctx := context.Background()

	// Region — .og.yaml takes precedence over global config
	regionID, _ := config.ActiveRegion()
	if resolved.Region != "" {
		regionID = resolved.Region
	}
	if regionID == "" {
		client, err := api.NewClient(resolved.APIBase, creds.Token, creds.OrgID)
		if err != nil {
			fmt.Println("# og: failed to create client")
			return nil
		}
		regions, err := client.ListRegions(ctx)
		if err != nil || len(regions) == 0 {
			fmt.Println("# og: no regions available")
			return nil
		}
		regionID = regions[0].ID
		_ = config.SetActiveRegion(regionID, regions[0].Name)
	}

	rc, err := api.NewClient(resolved.APIBase, creds.Token, creds.OrgID, regionID)
	if err != nil {
		fmt.Println("# og: failed to create client")
		return nil
	}

	providers, err := rc.ListProviders(ctx)
	if err != nil {
		fmt.Println("# og: failed to list providers")
		return nil
	}

	// Find provider
	var provider *api.Provider
	if providerFlag != "" {
		for i, p := range providers {
			if p.ID == providerFlag || strings.EqualFold(p.Name, providerFlag) {
				provider = &providers[i]
				break
			}
		}
		if provider == nil {
			// Fuzzy match
			for i, p := range providers {
				if strings.Contains(strings.ToLower(p.Name), strings.ToLower(providerFlag)) {
					provider = &providers[i]
					break
				}
			}
		}
		if provider == nil {
			fmt.Printf("# og: provider '%s' not found\n", providerFlag)
			return nil
		}
	} else {
		for i, p := range providers {
			if strings.Contains(strings.ToLower(p.Name), strings.ToLower(tc.providerNameHint)) {
				provider = &providers[i]
				break
			}
		}
	}

	if provider == nil {
		resp, err := rc.CreateProvider(ctx, &api.CreateProviderRequest{
			Name:              tc.providerNameHint,
			URL:               tc.upstreamURL,
			ForwardCallerAuth: tc.forwardCallerAuth,
		})
		if err != nil {
			fmt.Printf("# og: failed to create %s provider: %v\n", tc.providerNameHint, err)
			return nil
		}
		provider = &api.Provider{ID: resp.ID, Name: resp.Name}
	}

	shares, err := rc.ListShares(ctx, provider.ID)
	if err != nil {
		fmt.Printf("# og: failed to list shares: %v\n", err)
		return nil
	}

	var shareEndpoint string
	var shareApiKey string
	var isAuthForwarding bool
	var shareID string

	// If share is pinned in .og.yaml, look it up by ID
	if resolved.Share != "" {
		for _, s := range shares {
			if s.ID == resolved.Share || s.Name == resolved.Share {
				shareEndpoint = s.Endpoint
				shareApiKey = s.ApiKey
				isAuthForwarding = s.AuthForwarding
				shareID = s.ID
				break
			}
		}
		if shareEndpoint == "" {
			fmt.Printf("# og: share '%s' not found\n", resolved.Share)
			return nil
		}
	} else {
		// Auto-create: share name = [hostname] project
		dirName := projectFlag
		if dirName == "" {
			dirName = func() string { d, _ := os.Getwd(); return currentDirName2(d) }()
		}
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "unknown"
		}
		shareName := fmt.Sprintf("[%s] %s", hostname, dirName)

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
			resp, err := rc.CreateShare(ctx, provider.ID, &api.CreateShareRequest{Name: shareName})
			if err != nil {
				fmt.Printf("# og: failed to create share: %v\n", err)
				return nil
			}
			shareEndpoint = resp.Endpoint
			shareApiKey = resp.ApiKey
			isAuthForwarding = resp.AuthForwarding
			shareID = resp.ID

			if shareApiKey != "" {
				_ = saveShareKey(shareID, shareApiKey)
			}
		}
	}

	// Override share endpoint with gateway_url for local/private regions
	if resolved.GatewayURL != "" {
		if shareEndpoint == "" || strings.HasPrefix(shareEndpoint, "/") {
			shareEndpoint = strings.TrimRight(resolved.GatewayURL, "/") + shareEndpoint
		}
	}

	// Load cached API key if not in list response
	if shareApiKey == "" && shareID != "" {
		shareApiKey = loadShareKey(shareID)
	}

	// Embed OG key in URL path so Authorization header stays free for upstream auth
	baseURL := shareEndpoint
	if shareApiKey != "" {
		baseURL = strings.TrimRight(shareEndpoint, "/") + "/_k/" + shareApiKey
	}

	// Print export statements
	fmt.Printf("export %s=%s\n", tc.baseURLEnv, baseURL)
	if !isAuthForwarding && shareApiKey != "" {
		fmt.Printf("export %s=%s\n", tc.apiKeyEnv, shareApiKey)
	}

	return nil
}

func currentDirName2(dir string) string {
	if dir == "" {
		return "default"
	}
	parts := strings.Split(dir, string(os.PathSeparator))
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "default"
}
