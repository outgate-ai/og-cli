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

// DryRunResponse is the response from a guardrail dry-run request.
type DryRunResponse struct {
	DryRun             bool              `json:"dryRun"`
	Decision           string            `json:"decision"`
	Severity           string            `json:"severity"`
	Reason             string            `json:"reason"`
	GuardrailLatencyMs int               `json:"guardrailLatencyMs"`
	RawDetections      json.RawMessage   `json:"detections"`
	Detections         []DryRunDetection `json:"-"`
	AnonymizationCount int               `json:"anonymizationCount"`
}

type DryRunDetection struct {
	Text     string `json:"text"`
	Category string `json:"category"`
}

// parseDetections handles Lua cjson returning {} for empty arrays.
func (r *DryRunResponse) parseDetections() {
	if len(r.RawDetections) == 0 {
		return
	}
	if err := json.Unmarshal(r.RawDetections, &r.Detections); err != nil {
		r.Detections = nil
	}
}

// Token estimation: ~4 chars per token (conservative for English + code).
const charsPerToken = 4

func scanCmd() *cobra.Command {
	var providerFlag string
	var projectFlag string

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan project files for sensitive data using guardrail",
		Long: `Scans text files in a project directory through the guardrail service
to detect PII, credentials, and other sensitive data. Detections are
stored in the Detection Vault for fast matching on future requests.

Large files are automatically chunked to fit the guardrail model's context
window (default 128K tokens, configurable via .og.yaml).

Requires a provider with guardrail enabled. Uses the dry-run mode —
requests are evaluated but never forwarded to the upstream.

Configuration is resolved in order:
  CLI flags > .og.yaml > ~/.og/config.json > env vars > defaults

Example .og.yaml:

  provider: "My Provider"
  region: "reg-abc123"
  gateway_url: "http://localhost:8000"   # for local/private regions
  scan:
    max_context_tokens: 128000           # guardrail model context limit
    context_margin: 0.2                  # 20% safety margin
    overlap_lines: 50                    # overlap between chunks
    extensions: [".py", ".ts", ".env"]   # file types to scan
    exclude_dirs: ["vendor", "dist"]     # dirs to skip
    exclude_files: ["*.min.js"]          # file patterns to skip
    max_file_size: 2097152               # max file size (bytes)`,
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

	provider, err := resolveProvider(ctx, client, resolved.Provider)
	if err != nil {
		return err
	}

	full, err := client.GetProvider(ctx, provider.ID)
	if err != nil {
		return fmt.Errorf("failed to get provider details: %w", err)
	}
	if !full.GuardrailEnabled {
		return fmt.Errorf("provider '%s' does not have guardrail enabled.\nEnable a guardrail policy first at the console.", full.Name)
	}

	endpoint := full.Endpoint
	if resolved.GatewayURL != "" {
		endpoint = strings.TrimRight(resolved.GatewayURL, "/") + "/" + full.ID
	}
	if endpoint == "" {
		return fmt.Errorf("provider '%s' has no endpoint.\nFor local/private regions, set 'gateway_url' in .og.yaml:\n\n  gateway_url: \"http://localhost:8000\"\n", full.Name)
	}

	// Compute chunk size from scan config
	maxTokens := resolved.Scan.MaxContextTokens
	if maxTokens <= 0 {
		maxTokens = 128000
	}
	margin := resolved.Scan.ContextMargin
	if margin <= 0 || margin >= 1 {
		margin = 0.2
	}
	overlapLines := resolved.Scan.OverlapLines
	if overlapLines <= 0 {
		overlapLines = 50
	}
	maxCharsPerChunk := int(float64(maxTokens) * (1 - margin) * float64(charsPerToken))

	fmt.Printf("Scanning %s\n", projectDir)
	fmt.Printf("Provider: %s (%s)\n", full.Name, full.ID)
	fmt.Printf("Endpoint: %s\n", endpoint)
	fmt.Printf("Context:  %dK tokens (%.0f%% margin, %d char chunks)\n\n",
		maxTokens/1000, margin*100, maxCharsPerChunk)

	files, err := collectFiles(projectDir, &resolved.Scan)
	if err != nil {
		return fmt.Errorf("failed to scan directory: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("No text files found to scan.")
		return nil
	}

	fmt.Printf("Found %d text files\n\n", len(files))

	totalDetections := 0
	totalFiles := 0
	totalChunks := 0
	detectionsByType := map[string]int{}
	var failedFiles []string

	for i, file := range files {
		relPath, _ := filepath.Rel(projectDir, file)
		content, err := os.ReadFile(file)
		if err != nil {
			failedFiles = append(failedFiles, relPath)
			continue
		}

		// Chunk if file exceeds context limit
		chunks := chunkContent(string(content), maxCharsPerChunk, overlapLines)
		fileDetections := 0
		fileTotalLatency := 0

		for ci, chunk := range chunks {
			result, err := scanFile(ctx, endpoint, creds.Token, chunk)
			if err != nil {
				suffix := ""
				if len(chunks) > 1 {
					suffix = fmt.Sprintf(" (chunk %d/%d)", ci+1, len(chunks))
				}
				fmt.Printf("  %-50s  error%s: %s\n", relPath, suffix, err.Error())
				if ci == 0 {
					failedFiles = append(failedFiles, relPath)
				}
				continue
			}

			fileDetections += len(result.Detections)
			fileTotalLatency += result.GuardrailLatencyMs
			for _, d := range result.Detections {
				detectionsByType[d.Category]++
			}
			totalChunks++
		}

		totalDetections += fileDetections
		if fileDetections > 0 {
			totalFiles++
		}

		progress := fmt.Sprintf("[%d/%d]", i+1, len(files))
		chunkInfo := ""
		if len(chunks) > 1 {
			chunkInfo = fmt.Sprintf(" (%d chunks)", len(chunks))
		}
		if fileDetections > 0 {
			fmt.Printf("  %s %-45s  %d detections  %dms%s\n", progress, relPath, fileDetections, fileTotalLatency, chunkInfo)
		} else {
			fmt.Printf("  %s %-45s  clean%s\n", progress, relPath, chunkInfo)
		}
	}

	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("Scan complete: %d detections across %d/%d files", totalDetections, totalFiles, len(files))
	if totalChunks > len(files) {
		fmt.Printf(" (%d chunks)", totalChunks)
	}
	fmt.Println()
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

// chunkContent splits content into chunks that fit within maxChars,
// breaking on line boundaries with overlap for context continuity.
func chunkContent(content string, maxChars, overlapLines int) []string {
	if len(content) <= maxChars {
		return []string{content}
	}

	lines := strings.Split(content, "\n")
	var chunks []string
	start := 0

	for start < len(lines) {
		// Find how many lines fit in maxChars
		charCount := 0
		end := start
		for end < len(lines) {
			lineLen := len(lines[end]) + 1 // +1 for newline
			if charCount+lineLen > maxChars && end > start {
				break
			}
			charCount += lineLen
			end++
		}

		// Build chunk from lines[start:end]
		chunk := strings.Join(lines[start:end], "\n")
		chunks = append(chunks, chunk)

		// Move start forward, keeping overlap
		if end >= len(lines) {
			break
		}
		start = end - overlapLines
		if start < 0 {
			start = 0
		}
		// Prevent infinite loop if overlap pushes start back to same position
		if start <= (end - (end - start)) {
			start = end
		}
	}

	return chunks
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
