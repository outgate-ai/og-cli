package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/outgate-ai/og-cli/api"
	"github.com/outgate-ai/og-cli/internal/config"
)

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show account, providers, and usage",
		RunE:  statusHandler,
	}
}

func statusHandler(cmd *cobra.Command, args []string) error {
	creds, err := config.LoadCredentials()
	if err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}
	if creds == nil || creds.Token == "" {
		fmt.Println("Not logged in. Run 'og login' first.")
		return nil
	}

	// Account section
	fmt.Println("Account")
	fmt.Println(strings.Repeat("-", 50))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  Name:\t%s\n", creds.Name)
	fmt.Fprintf(w, "  Email:\t%s\n", creds.Email)
	w.Flush()

	// Org section
	if creds.OrgName != "" || creds.OrgID != "" {
		fmt.Println()
		fmt.Println("Organization")
		fmt.Println(strings.Repeat("-", 50))
		w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if creds.OrgName != "" {
			fmt.Fprintf(w, "  Name:\t%s\n", creds.OrgName)
		}
		if creds.OrgID != "" {
			client, err := api.NewClient(config.APIBaseURL(), creds.Token, creds.OrgID)
			if err == nil {
				org, err := client.GetOrganization(cmd.Context(), creds.OrgID)
				if err == nil {
					if creds.OrgName == "" {
						fmt.Fprintf(w, "  Name:\t%s\n", org.Name)
					}
					fmt.Fprintf(w, "  Plan:\t%s\n", capitalize(org.Plan))
				}
			}
		}
		w.Flush()
	}

	if creds.OrgID == "" {
		fmt.Println()
		return nil
	}

	client, err := api.NewClient(config.APIBaseURL(), creds.Token, creds.OrgID)
	if err != nil {
		fmt.Println()
		return nil
	}
	ctx := cmd.Context()

	// Regions
	regions, err := client.ListRegions(ctx)
	if err != nil || len(regions) == 0 {
		fmt.Println()
		return nil
	}

	// Build provider name map
	providerNames := map[string]string{}

	for _, region := range regions {
		rc, err := api.NewClient(config.APIBaseURL(), creds.Token, creds.OrgID, region.ID)
		if err != nil {
			continue
		}

		shareNames := map[string]string{}

		providers, _ := rc.ListProviders(ctx)
		for _, p := range providers {
			providerNames[p.ID] = p.Name
			// Fetch shares for this provider to build name map
			shares, err := rc.ListShares(ctx, p.ID)
			if err == nil {
				for _, s := range shares {
					shareNames[s.ID] = s.Name
				}
			}
		}

		// Usage
		dash, err := rc.GetDashboard(ctx, "24h")
		if err != nil {
			continue
		}
		s := dash.Summary
		if s.TotalRequests == 0 {
			continue
		}

		totalTokens := s.TotalPromptTok + s.TotalCompletionTok

		fmt.Println()
		fmt.Printf("Usage — last 24h (%s)\n", region.Name)
		fmt.Println(strings.Repeat("-", 50))
		w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "  Requests:\t%s\n", fmtNum(s.TotalRequests))
		fmt.Fprintf(w, "  Tokens:\t%s total\n", fmtNum(totalTokens))
		fmt.Fprintf(w, "    Input:\t%s\n", fmtNum(s.TotalPromptTok))
		fmt.Fprintf(w, "    Output:\t%s\n", fmtNum(s.TotalCompletionTok))
		if s.TotalCacheReadTok > 0 {
			fmt.Fprintf(w, "    Cache read:\t%s\n", fmtNum(s.TotalCacheReadTok))
		}
		if s.TotalCacheWriteTok > 0 {
			fmt.Fprintf(w, "    Cache write:\t%s\n", fmtNum(s.TotalCacheWriteTok))
		}
		if s.ErrorRate > 0 {
			fmt.Fprintf(w, "  Error Rate:\t%.1f%%\n", s.ErrorRate*100)
		}
		fmt.Fprintf(w, "  Avg Latency:\t%.0fms\n", s.AvgLatency)
		w.Flush()

		// Top Providers
		if len(dash.TopProviders) > 0 {
			fmt.Println()
			fmt.Println("  Top Providers")
			w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "    NAME\tREQUESTS\tINPUT\tOUTPUT\tCACHE\n")
			for i, p := range dash.TopProviders {
				if i >= 5 {
					break
				}
				name := providerNames[p.ID]
				if name == "" {
					name = p.ID
				}
				fmt.Fprintf(w, "    %s\t%s\t%s\t%s\t%s\n",
					name, fmtNum(p.RequestCount),
					fmtNum(p.PromptTokens), fmtNum(p.CompletionTok),
					fmtNum(p.CacheReadTok))
			}
			w.Flush()
		}

		// Top Models
		if len(dash.TopModels) > 0 {
			fmt.Println()
			fmt.Println("  Top Models")
			w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "    MODEL\tREQUESTS\tINPUT\tOUTPUT\tCACHE\n")
			for i, m := range dash.TopModels {
				if i >= 5 {
					break
				}
				fmt.Fprintf(w, "    %s\t%s\t%s\t%s\t%s\n",
					m.ID, fmtNum(m.RequestCount),
					fmtNum(m.PromptTokens), fmtNum(m.CompletionTok),
					fmtNum(m.CacheReadTok))
			}
			w.Flush()
		}

		// Top Shares
		sharesMetrics, err := rc.GetSharesMetrics(ctx)
		if err == nil && len(sharesMetrics.Shares) > 0 {
			fmt.Println()
			fmt.Println("  Top Shares")
			w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "    SHARE\tREQUESTS\tINPUT\tOUTPUT\tCACHE\n")
			for i, sh := range sharesMetrics.Shares {
				if i >= 5 {
					break
				}
				name := shareNames[sh.ID]
				if name == "" {
					name = sh.ID
				}
				fmt.Fprintf(w, "    %s\t%s\t%s\t%s\t%s\n",
					name, fmtNum(sh.RequestCount),
					fmtNum(sh.PromptTokens), fmtNum(sh.CompletionTok),
					fmtNum(sh.CacheReadTok))
			}
			w.Flush()
		}

		// Top Users/Keys
		if len(dash.TopUsers) > 0 {
			fmt.Println()
			fmt.Println("  Top API Keys")
			w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "    KEY\tREQUESTS\tINPUT\tOUTPUT\tCACHE\n")
			for i, u := range dash.TopUsers {
				if i >= 5 {
					break
				}
				// Extract short key ID from the full composite ID
				keyID := u.ID
				if parts := strings.Split(u.ID, "-ak-"); len(parts) == 2 {
					keyID = parts[1][:8] + "..."
				}
				fmt.Fprintf(w, "    %s\t%s\t%s\t%s\t%s\n",
					keyID, fmtNum(u.RequestCount),
					fmtNum(u.PromptTokens), fmtNum(u.CompletionTok),
					fmtNum(u.CacheReadTok))
			}
			w.Flush()
		}
	}

	fmt.Println()
	return nil
}

func fmtNum(n int64) string {
	if n >= 1_000_000_000 {
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
