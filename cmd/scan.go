package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/outgate-ai/og-cli/api"
	"github.com/outgate-ai/og-cli/internal/config"
)

// File extensions to scan
var scanExtensions = map[string]bool{
	".ts": true, ".js": true, ".py": true, ".go": true, ".rs": true, ".java": true,
	".yaml": true, ".yml": true, ".json": true, ".toml": true, ".ini": true,
	".env": true, ".cfg": true, ".conf": true, ".xml": true,
	".sh": true, ".bash": true, ".zsh": true, ".sql": true, ".md": true, ".txt": true,
	".rb": true, ".php": true, ".cs": true, ".kt": true, ".swift": true,
	".dockerfile": true, ".tf": true, ".hcl": true, ".properties": true,
}

// Directories to skip
var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, "dist": true, "build": true,
	"vendor": true, "__pycache__": true, ".next": true, ".nuxt": true,
	"target": true, "out": true, ".cache": true, ".venv": true, "venv": true,
}

const maxFileSize = 1024 * 1024 // 1MB

// DryRunResponse is the response from a guardrail dry-run request
type DryRunResponse struct {
	DryRun            bool   `json:"dryRun"`
	Decision          string `json:"decision"`
	Severity          string `json:"severity"`
	Reason            string `json:"reason"`
	GuardrailLatencyMs int   `json:"guardrailLatencyMs"`
	Detections        []struct {
		Text     string `json:"text"`
		Category string `json:"category"`
	} `json:"detections"`
	AnonymizationCount int `json:"anonymizationCount"`
}

func scanCmd() *cobra.Command {
	var providerFlag string
	var projectFlag string

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan project files for sensitive data using guardrail",
		Long: `Scans text files in a project directory through the guardrail service
to detect PII, credentials, and other sensitive data. Detections are
stored in the Detection Vault for fast matching on future requests.

Requires a provider with guardrail enabled. Uses the dry-run mode —
requests are evaluated but never forwarded to the upstream.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScan(cmd.Context(), providerFlag, projectFlag)
		},
	}

	cmd.Flags().StringVar(&providerFlag, "provider", "", "Provider ID or name (required — must have guardrail enabled)")
	cmd.Flags().StringVar(&projectFlag, "project", "", "Project directory to scan (default: current directory)")
	_ = cmd.MarkFlagRequired("provider")

	return cmd
}

func runScan(ctx context.Context, providerRef, projectDir string) error {
	if projectDir == "" {
		var err error
		projectDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
	}

	// Resolve absolute path
	projectDir, _ = filepath.Abs(projectDir)

	creds, err := config.LoadCredentials()
	if err != nil || creds == nil || creds.Token == "" {
		return fmt.Errorf("not logged in — run 'og login' first")
	}

	regionID, _ := config.ActiveRegion()
	if regionID == "" {
		return fmt.Errorf("no region selected — run 'og region select' first")
	}

	client, err := api.NewClient(config.APIBaseURL(), creds.Token, creds.OrgID, regionID)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Resolve provider
	provider, err := resolveProvider(ctx, client, providerRef)
	if err != nil {
		return err
	}

	// Verify guardrail is enabled
	full, err := client.GetProvider(ctx, provider.ID)
	if err != nil {
		return fmt.Errorf("failed to get provider details: %w", err)
	}
	if !full.GuardrailEnabled {
		return fmt.Errorf("provider '%s' does not have guardrail enabled.\nEnable a guardrail policy first at the console.", full.Name)
	}
	if full.Endpoint == "" {
		return fmt.Errorf("provider '%s' has no endpoint — wait for gateway sync to complete", full.Name)
	}

	fmt.Printf("Scanning %s\n", projectDir)
	fmt.Printf("Provider: %s (%s)\n", full.Name, full.ID)
	fmt.Printf("Endpoint: %s\n\n", full.Endpoint)

	// Walk directory and collect text files
	files, err := collectFiles(projectDir)
	if err != nil {
		return fmt.Errorf("failed to scan directory: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("No text files found to scan.")
		return nil
	}

	fmt.Printf("Found %d text files\n\n", len(files))

	// Scan each file
	totalDetections := 0
	totalFiles := 0
	detectionsByType := map[string]int{}
	var failedFiles []string

	for i, file := range files {
		relPath, _ := filepath.Rel(projectDir, file)
		content, err := os.ReadFile(file)
		if err != nil {
			failedFiles = append(failedFiles, relPath)
			continue
		}

		result, err := scanFile(ctx, full.Endpoint, creds.Token, string(content))
		if err != nil {
			fmt.Printf("  %-50s  error: %s\n", relPath, err.Error())
			failedFiles = append(failedFiles, relPath)
			continue
		}

		count := len(result.Detections)
		totalDetections += count
		if count > 0 {
			totalFiles++
			for _, d := range result.Detections {
				detectionsByType[d.Category]++
			}
		}

		// Display progress
		bar := progressBar(count)
		if count > 0 {
			fmt.Printf("  %-50s  %d detections  %s  %dms\n", relPath, count, bar, result.GuardrailLatencyMs)
		} else if (i+1)%20 == 0 || i == len(files)-1 {
			// Print progress periodically for clean files
			fmt.Printf("  %-50s  clean\n", relPath)
		}
	}

	// Summary
	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("Scan complete: %d detections across %d/%d files\n", totalDetections, totalFiles, len(files))
	if len(detectionsByType) > 0 {
		for cat, count := range detectionsByType {
			fmt.Printf("  %-25s %d\n", cat, count)
		}
	}
	if len(failedFiles) > 0 {
		fmt.Printf("\nFailed to scan %d files\n", len(failedFiles))
	}
	fmt.Println("\nDetections stored in Detection Vault.")

	return nil
}

func resolveProvider(ctx context.Context, client *api.Client, ref string) (*api.Provider, error) {
	providers, err := client.ListProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list providers: %w", err)
	}

	for i, p := range providers {
		if p.ID == ref || strings.EqualFold(p.Name, ref) {
			return &providers[i], nil
		}
	}

	// Fuzzy match
	for i, p := range providers {
		if strings.Contains(strings.ToLower(p.Name), strings.ToLower(ref)) {
			return &providers[i], nil
		}
	}

	return nil, fmt.Errorf("provider '%s' not found", ref)
}

func collectFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Size() > maxFileSize {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		name := strings.ToLower(info.Name())

		// Match by extension
		if scanExtensions[ext] {
			files = append(files, path)
			return nil
		}
		// Match .env.* files (e.g., .env.local, .env.production)
		if strings.HasPrefix(name, ".env") {
			files = append(files, path)
			return nil
		}
		// Match Dockerfile
		if name == "dockerfile" || strings.HasPrefix(name, "dockerfile.") {
			files = append(files, path)
			return nil
		}

		return nil
	})
	return files, err
}

func scanFile(ctx context.Context, endpoint, token, content string) (*DryRunResponse, error) {
	body := map[string]any{
		"model": "scan",
		"messages": []map[string]string{
			{"role": "user", "content": content},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Outgate-Guardrail", "dry-run")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		// Check if it's a guardrail block response
		if resp.StatusCode == 403 || resp.StatusCode == 422 {
			var result DryRunResponse
			result.Decision = "BLOCK"
			_ = json.Unmarshal(respBody, &result)
			return &result, nil
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody[:min(200, len(respBody))]))
	}

	var result DryRunResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return &result, nil
}

func progressBar(detections int) string {
	if detections == 0 {
		return ""
	}
	filled := detections
	if filled > 10 {
		filled = 10
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", 10-filled) + "]"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
