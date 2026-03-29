package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/outgate-ai/og-cli/api"
	"github.com/outgate-ai/og-cli/internal/config"
)

func regionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "region",
		Short: "Manage regions",
	}

	cmd.AddCommand(regionListCmd())
	cmd.AddCommand(regionChangeCmd())

	return cmd
}

func regionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available regions",
		RunE:  regionListHandler,
	}
}

func regionChangeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "change [region-name]",
		Short: "Switch active region",
		Long:  "Switch the active region. Pass the region name as an argument, or omit it to select interactively.",
		Args:  cobra.MaximumNArgs(1),
		RunE:  regionChangeHandler,
	}
}

func regionListHandler(cmd *cobra.Command, args []string) error {
	creds, err := config.LoadCredentials()
	if err != nil || creds == nil || creds.Token == "" {
		fmt.Println("Not logged in. Run 'og login' first.")
		return nil
	}

	client, err := api.NewClient(config.APIBaseURL(), creds.Token, creds.OrgID)
	if err != nil {
		return err
	}

	regions, err := client.ListRegions(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to list regions: %w", err)
	}

	if len(regions) == 0 {
		fmt.Println("No regions found.")
		return nil
	}

	activeID, _ := config.ActiveRegion()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  \tNAME\tID\tSTATUS\n")
	for _, r := range regions {
		marker := " "
		if r.ID == activeID {
			marker = "*"
		}
		status := r.Status
		if status == "" {
			status = "-"
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", marker, r.Name, r.ID, status)
	}
	w.Flush()

	if activeID != "" {
		fmt.Println()
		_, activeName := config.ActiveRegion()
		fmt.Printf("Active region: %s\n", activeName)
	}

	return nil
}

func regionChangeHandler(cmd *cobra.Command, args []string) error {
	creds, err := config.LoadCredentials()
	if err != nil || creds == nil || creds.Token == "" {
		fmt.Println("Not logged in. Run 'og login' first.")
		return nil
	}

	client, err := api.NewClient(config.APIBaseURL(), creds.Token, creds.OrgID)
	if err != nil {
		return err
	}

	regions, err := client.ListRegions(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to list regions: %w", err)
	}

	if len(regions) == 0 {
		fmt.Println("No regions found.")
		return nil
	}

	var selected api.Region

	if len(args) == 1 {
		// Match by name (case-insensitive)
		target := strings.ToLower(args[0])
		for _, r := range regions {
			if strings.ToLower(r.Name) == target || r.ID == target {
				selected = r
				break
			}
		}
		if selected.ID == "" {
			fmt.Printf("Region '%s' not found. Available regions:\n\n", args[0])
			for _, r := range regions {
				fmt.Printf("  %s\n", r.Name)
			}
			return nil
		}
	} else {
		// Interactive selection
		activeID, _ := config.ActiveRegion()

		fmt.Println("Select a region:")
		fmt.Println()
		for i, r := range regions {
			marker := "  "
			if r.ID == activeID {
				marker = "* "
			}
			fmt.Printf("  %s%d) %s\n", marker, i+1, r.Name)
		}
		fmt.Println()
		fmt.Printf("Enter number [1-%d]: ", len(regions))

		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		num, err := strconv.Atoi(input)
		if err != nil || num < 1 || num > len(regions) {
			fmt.Println("Invalid selection.")
			return nil
		}

		selected = regions[num-1]
	}

	if err := config.SetActiveRegion(selected.ID, selected.Name); err != nil {
		return fmt.Errorf("failed to save region: %w", err)
	}

	fmt.Printf("Switched to region: %s\n", selected.Name)
	return nil
}
