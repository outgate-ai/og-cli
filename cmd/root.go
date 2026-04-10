package cmd

import (
	"github.com/spf13/cobra"

	"github.com/outgate-ai/og-cli/internal/auth"
	"github.com/outgate-ai/og-cli/version"
)

func NewCLI() *cobra.Command {
	root := &cobra.Command{
		Use:     "og",
		Short:   "Outgate CLI — route AI traffic through your gateway",
		Long:    "Outgate CLI lets you authenticate, manage providers, and route AI tool traffic through your Outgate gateway with guardrails, logging, and cost tracking.",
		Version: version.Version,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return auth.MaybeRefreshToken(cmd.Context())
		},
	}

	root.AddCommand(
		loginCmd(),
		logoutCmd(),
		statusCmd(),
		regionCmd(),
		claudeCmd(),
		codexCmd(),
		envCmd(),
		scanCmd(),
		vaultCmd(),
	)

	return root
}
