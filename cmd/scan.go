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

// DryRunResponse is the response from a guardrail dry-run request
type DryRunResponse struct {
	DryRun             bool                `json:"dryRun"`
	Decision           string              `json:"decision"`
	Severity           string              `json:"severity"`
	Reason             string              `json:"reason"`
	GuardrailLatencyMs int                 `json:"guardrailLatencyMs"`
	RawDetections      json.RawMessage     `json:"detections"`
	Detections         []DryRunDetection   `json:"-"` // populated after unmarshal
	AnonymizationCount int                 `json:"anonymizationCount"`
}

type DryRunDetection struct {
	Text        string `json:"text"`
	Category    string `json:"category"`
}

// parseDetections handles Lua cjson returning {} for empty arrays
func (r *DryRunResponse) parseDetections() {
	if len(r.RawDetections) == 0 {
		return
	}
	// Try as array first
	if err := json.Unmarshal(r.RawDetections, &r.Detections); err != nil {
		// Lua cjson encodes empty array as {} — ignore
		r.Detections = nil
	}
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
requests are evaluated but never forwarded to the upstream.

Configuration is resolved in order: CLI flags > .og.yaml > ~/.og/config.json > env vars > defaults.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScan(cmd.Context(), providerFlag, projectFlag)
		},
	}

	cmd.Flags().StringVar(&providerFlag, "provider", "", "Provider ID or name (must have guardrail enabled)")
	cmd.Flags().StringVar(&projectFlag, "project", "", "Project directory to scan (default: current directory)")

	return cmd
}

func runScan(ctx context.Context, providerFlag, projectFlag string) error {
	projectDir := projectFlag
	if projectDir == "" {
		var err error
		projectDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
	}
	projectDir, _ = filepath.Abs(projectDir)

	// Resolve config: CLI flags > .og.yaml > global config > env vars > defaults
	resolved := config.Resolve(config.ResolveInput{
		Provider: providerFlag,
		Project:  projectFlag,
		StartDir: projectDir,
	})

	if resolved.Provider == "" {
		return fmt.Errorf("provider is required — use --provider flag or set 'provider' in .og.yaml")
	}

	creds, err := config.LoadCredentials()
	if err != nil || creds == nil || creds.Token == "" {
		return fmt.Errorf("not logged in — run 'og login' first")
	}

	regionID := resolved.Region
	if regionID == "" {
		regionID, _ = config.ActiveRegion()
	}
	if regionID == "" {
		return fmt.Errorf("no region selected — run 'og region select' or set 'region' in .og.yaml")
	}

	client, err := api.NewClient(resolved.APIBase, creds.Token, creds.OrgID, regionID)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Resolve provider by name or ID
	provider, err := resolveProvider(ctx, client, resolved.Provider)
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

	// Determine the gateway endpoint for this provider
	// Priority: gateway_url from config (for local/private regions) > provider endpoint (public regions)
	endpoint := full.Endpoint
	if resolved.GatewayURL != "" {
		// Local/private region: use gateway_url + provider path
		endpoint = strings.TrimRight(resolved.GatewayURL, "/") + "/" + full.ID
	}
	if endpoint == "" {
		return fmt.Errorf("provider '%s' has no endpoint.\nFor local/private regions, set 'gateway_url' in .og.yaml:\n\n  gateway_url: \"http://localhost:8000\"\n", full.Name)
	}

	fmt.Printf("Scanning %s\n", projectDir)
	fmt.Printf("Provider: %s (%s)\n", full.Name, full.ID)
	fmt.Printf("Endpoint: %s\n\n", endpoint)

	// Walk directory and collect text files
	files, err := collectFiles(projectDir, &resolved.Scan)
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

		result, err := scanFile(ctx, endpoint, creds.Token, string(content))
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

	for i, p := range providers {
		if strings.Contains(strings.ToLower(p.Name), strings.ToLower(ref)) {
			return &providers[i], nil
		}
	}

	return nil, fmt.Errorf("provider '%s' not found", ref)
}

func collectFiles(root string, scanCfg *config.ScanConfig) ([]string, error) {
	extSet := make(map[string]bool, len(scanCfg.Extensions))
	for _, ext := range scanCfg.Extensions {
		extSet[ext] = true
	}

	excludeDirSet := make(map[string]bool, len(scanCfg.ExcludeDirs))
	for _, dir := range scanCfg.ExcludeDirs {
		excludeDirSet[dir] = true
	}

	maxSize := scanCfg.MaxFileSize
	if maxSize <= 0 {
		maxSize = 1024 * 1024
	}

	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if excludeDirSet[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Size() > maxSize {
			return nil
		}

		// Check excluded file patterns
		for _, pattern := range scanCfg.ExcludeFiles {
			if matched, _ := filepath.Match(pattern, info.Name()); matched {
				return nil
			}
		}

		ext := strings.ToLower(filepath.Ext(path))
		name := strings.ToLower(info.Name())

		if extSet[ext] {
			files = append(files, path)
			return nil
		}
		if strings.HasPrefix(name, ".env") {
			files = append(files, path)
			return nil
		}
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
		if resp.StatusCode == 403 || resp.StatusCode == 422 {
			var result DryRunResponse
			result.Decision = "BLOCK"
			_ = json.Unmarshal(respBody, &result)
			result.parseDetections()
			return &result, nil
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody[:min(200, len(respBody))]))
	}

	var result DryRunResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	result.parseDetections()
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
