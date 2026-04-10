package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/outgate-ai/og-cli/api"
	"github.com/outgate-ai/og-cli/internal/config"
)

// normalizeLower normalizes text the same way the fingerprint store does:
// lowercase, trim, collapse whitespace.
func normalizeLower(text string) string {
	return strings.ToLower(strings.TrimSpace(strings.Join(strings.Fields(text), " ")))
}

func vaultCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Manage the Detection Vault",
		Long: `Add, remove, and list fingerprinted detections in the Detection Vault.

The vault stores hashed tokens of sensitive values so they can be
detected in future requests without calling the LLM again.

Subcommands:
  add    — Manually add a value to the vault
  rm     — Remove a value by its content or by tag
  list   — List vault entries`,
	}

	cmd.AddCommand(vaultAddCmd())
	cmd.AddCommand(vaultRmCmd())
	cmd.AddCommand(vaultListCmd())

	return cmd
}

func vaultAddCmd() *cobra.Command {
	var categoryFlag string
	var tagFlag string

	cmd := &cobra.Command{
		Use:   "add <value>",
		Short: "Add a value to the Detection Vault",
		Long: `Tokenizes and hashes the given value, then stores it in the vault
with source='manual'. Future requests containing this value will
be caught by the KV fingerprint scan without needing an LLM call.

Examples:
  og vault add "sk-prod-abc123" --tag "production API key"
  og vault add "john@example.com" --category personal_information --tag "test PII"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVaultAdd(cmd.Context(), args[0], categoryFlag, tagFlag)
		},
	}

	cmd.Flags().StringVar(&categoryFlag, "category", "credentials", "Detection category (credentials, personal_information, sensitive_data)")
	cmd.Flags().StringVar(&tagFlag, "tag", "", "Optional label for this entry (max 200 chars)")

	return cmd
}

func vaultRmCmd() *cobra.Command {
	var tagFlag string

	cmd := &cobra.Command{
		Use:   "rm <value>",
		Short: "Remove a value from the Detection Vault",
		Long: `Removes a fingerprint entry. By default, removes by hashing the
given value. Use --tag to remove all entries whose tag contains
the given substring.

Examples:
  og vault rm "sk-prod-abc123"
  og vault rm --tag "prod"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVaultRm(cmd.Context(), args, tagFlag)
		},
	}

	cmd.Flags().StringVar(&tagFlag, "tag", "", "Remove all entries whose tag contains this substring")

	return cmd
}

func vaultListCmd() *cobra.Command {
	var categoryFlag string
	var sourceFlag string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Detection Vault entries",
		Long: `Lists fingerprint entries in the vault with optional filtering.

Examples:
  og vault list
  og vault list --category credentials
  og vault list --source manual`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVaultList(cmd.Context(), categoryFlag, sourceFlag)
		},
	}

	cmd.Flags().StringVar(&categoryFlag, "category", "", "Filter by category (credentials, personal_information, sensitive_data)")
	cmd.Flags().StringVar(&sourceFlag, "source", "", "Filter by source (auto-detect, kv-scan, manual)")

	return cmd
}

func resolveVaultClient(ctx context.Context) (*api.Client, error) {
	creds, err := config.LoadCredentials()
	if err != nil || creds == nil || creds.Token == "" {
		return nil, fmt.Errorf("not logged in — run 'og login' first")
	}

	resolved := config.Resolve(config.ResolveInput{})
	regionID := resolved.Region
	if regionID == "" {
		regionID, _ = config.ActiveRegion()
	}
	if regionID == "" {
		return nil, fmt.Errorf("no region selected — run 'og region change' or set 'region' in .og.yaml")
	}

	return api.NewClient(resolved.APIBase, creds.Token, creds.OrgID, regionID)
}

func runVaultAdd(ctx context.Context, value, category, tag string) error {
	if category == "" {
		category = "credentials"
	}
	if len(tag) > 200 {
		return fmt.Errorf("tag must be 200 characters or less")
	}

	client, err := resolveVaultClient(ctx)
	if err != nil {
		return err
	}

	resp, err := client.VaultAdd(ctx, value, category, tag)
	if err != nil {
		return fmt.Errorf("failed to add detection: %w", err)
	}

	if resp.Stored {
		fmt.Printf("Added to vault: hash=%s\n", resp.Hash[:16]+"...")
	} else {
		fmt.Printf("Already in vault (updated): hash=%s\n", resp.Hash[:16]+"...")
	}
	return nil
}

func runVaultRm(ctx context.Context, args []string, tag string) error {
	client, err := resolveVaultClient(ctx)
	if err != nil {
		return err
	}

	if tag != "" {
		// Delete by tag substring
		resp, err := client.VaultDeleteByTag(ctx, tag)
		if err != nil {
			return fmt.Errorf("failed to delete by tag: %w", err)
		}
		if resp.Deleted == 0 {
			fmt.Println("No entries found matching that tag.")
		} else {
			fmt.Printf("Deleted %d entr%s matching tag '%s'\n", resp.Deleted, pluralY(resp.Deleted), tag)
		}
		return nil
	}

	if len(args) == 0 {
		return fmt.Errorf("provide a value to remove, or use --tag to remove by tag")
	}

	// Hash the value locally to match the server's hash
	hash := sha256.Sum256([]byte(normalizeLower(args[0])))
	hashStr := hex.EncodeToString(hash[:])

	err = client.VaultDelete(ctx, hashStr)
	if err != nil {
		return fmt.Errorf("failed to delete detection: %w", err)
	}

	fmt.Printf("Removed: hash=%s\n", hashStr[:16]+"...")
	return nil
}

func runVaultList(ctx context.Context, category, source string) error {
	client, err := resolveVaultClient(ctx)
	if err != nil {
		return err
	}

	resp, err := client.VaultList(ctx, 1, 50, category, source)
	if err != nil {
		return fmt.Errorf("failed to list detections: %w", err)
	}

	if len(resp.Detections) == 0 {
		fmt.Println("No fingerprints found.")
		return nil
	}

	fmt.Printf("%-16s %-12s %-8s %-8s %s\n", "HASH", "TYPE", "SOURCE", "TOKENS", "TAG")
	fmt.Printf("%-16s %-12s %-8s %-8s %s\n", "----", "----", "------", "------", "---")
	for _, d := range resp.Detections {
		hashShort := d.Hash
		if len(hashShort) > 16 {
			hashShort = hashShort[:16]
		}
		src := d.Source
		if src == "" {
			src = "auto"
		}
		tagDisplay := d.Tag
		if len(tagDisplay) > 40 {
			tagDisplay = tagDisplay[:37] + "..."
		}
		fmt.Printf("%-16s %-12s %-8s %-8d %s\n", hashShort+"...", d.Type, src, d.TokenCount, tagDisplay)
	}

	fmt.Printf("\nShowing %d of %d entries (page %d)\n", len(resp.Detections), resp.Total, resp.Page)
	return nil
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}