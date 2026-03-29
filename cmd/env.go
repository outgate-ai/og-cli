package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
Use --name <project> to set a custom share name (defaults to current directory).`,
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

	creds, err := config.LoadCredentials()
	if err != nil || creds == nil || creds.Token == "" {
		// Silent fail for shell eval — just print comments
		fmt.Println("# og: not logged in — run 'og login' first")
		return nil
	}

	ctx := context.Background()

	regionID, _ := config.ActiveRegion()
	if regionID == "" {
		client, err := api.NewClient(config.APIBaseURL(), creds.Token, creds.OrgID)
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

	rc, err := api.NewClient(config.APIBaseURL(), creds.Token, creds.OrgID, regionID)
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
	providerOverride, _ := cmd.Flags().GetString("provider")
	var provider *api.Provider
	if providerOverride != "" {
		for i, p := range providers {
			if p.ID == providerOverride || strings.EqualFold(p.Name, providerOverride) {
				provider = &providers[i]
				break
			}
		}
		if provider == nil {
			fmt.Printf("# og: provider '%s' not found\n", providerOverride)
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
		// Create provider silently
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

	// Find or create share
	dirName, _ := cmd.Flags().GetString("name")
	if dirName == "" {
		dirName = filepath.Base(func() string { d, _ := os.Getwd(); return d }())
	}
	shares, err := rc.ListShares(ctx, provider.ID)
	if err != nil {
		fmt.Printf("# og: failed to list shares: %v\n", err)
		return nil
	}

	var shareEndpoint string
	var shareApiKey string
	var isAuthForwarding bool
	for _, s := range shares {
		if s.Name == dirName {
			shareEndpoint = s.Endpoint
			shareApiKey = s.ApiKey
			isAuthForwarding = s.AuthForwarding
			break
		}
	}

	if shareEndpoint == "" {
		resp, err := rc.CreateShare(ctx, provider.ID, &api.CreateShareRequest{Name: dirName})
		if err != nil {
			fmt.Printf("# og: failed to create share: %v\n", err)
			return nil
		}
		shareEndpoint = resp.Endpoint
		shareApiKey = resp.ApiKey
		isAuthForwarding = resp.AuthForwarding
	}

	// Print export statements to stdout (for eval)
	fmt.Printf("export %s=%s\n", tc.baseURLEnv, shareEndpoint)
	if !isAuthForwarding && shareApiKey != "" {
		fmt.Printf("export %s=%s\n", tc.apiKeyEnv, shareApiKey)
	}

	return nil
}
