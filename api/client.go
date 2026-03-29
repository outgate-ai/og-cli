// Package api implements the HTTP client for talking to the Outgate BFF API.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/outgate-ai/og-cli/version"
)

// Client communicates with the Outgate API.
type Client struct {
	base     *url.URL
	http     *http.Client
	token    string
	orgID    string
	regionID string
}

// NewClient creates a client with the given base URL, auth token, org ID, and optional region ID.
func NewClient(baseURL, token, orgID string, regionID ...string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid API base URL: %w", err)
	}
	c := &Client{
		base:  u,
		http:  &http.Client{Timeout: 30 * time.Second},
		token: token,
		orgID: orgID,
	}
	if len(regionID) > 0 {
		c.regionID = regionID[0]
	}
	return c, nil
}

// StatusError represents an API error response.
type StatusError struct {
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
}

func (e StatusError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, body, result any) error {
	u := *c.base
	if idx := strings.Index(path, "?"); idx >= 0 {
		u.Path = u.Path + path[:idx]
		u.RawQuery = path[idx+1:]
	} else {
		u.Path = u.Path + path
	}

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "og-cli/"+version.Version)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.orgID != "" {
		req.Header.Set("x-organization-id", c.orgID)
	}
	if c.regionID != "" {
		req.Header.Set("x-region-id", c.regionID)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode >= 400 {
		apiErr := StatusError{StatusCode: resp.StatusCode}
		if json.Unmarshal(respData, &apiErr) != nil {
			apiErr.Message = string(respData)
		}
		return apiErr
	}

	if result != nil && len(respData) > 0 {
		return json.Unmarshal(respData, result)
	}
	return nil
}

// -- CLI Token ---

// RevokeSelfToken calls DELETE /auth/cli-token to revoke the current token.
func (c *Client) RevokeSelfToken(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/auth/cli-token", nil, nil)
}

// CliRefreshResponse holds the response from the CLI token refresh endpoint.
type CliRefreshResponse struct {
	Token     string   `json:"token"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expiresAt"`
	User      struct {
		ID               string `json:"id"`
		Email            string `json:"email"`
		Name             string `json:"name"`
		OrganizationID   string `json:"organizationId"`
		OrganizationName string `json:"organizationName"`
	} `json:"user"`
}

// RefreshCliToken calls the /auth/cli-refresh endpoint to obtain a new token.
func (c *Client) RefreshCliToken(ctx context.Context) (*CliRefreshResponse, error) {
	var resp CliRefreshResponse
	err := c.do(ctx, http.MethodPost, "/auth/cli-refresh", nil, &resp)
	return &resp, err
}

// -- Providers ---

type Provider struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Status     string `json:"status,omitempty"`
	ModelCount int    `json:"modelCount,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
}

type ProvidersResponse struct {
	Providers []Provider `json:"providers"`
}

func (c *Client) ListProviders(ctx context.Context) ([]Provider, error) {
	var resp ProvidersResponse
	err := c.do(ctx, http.MethodGet, "/providers", nil, &resp)
	if err != nil {
		var arr []Provider
		err2 := c.do(ctx, http.MethodGet, "/providers", nil, &arr)
		if err2 == nil {
			return arr, nil
		}
		return nil, err
	}
	return resp.Providers, nil
}

// -- Provider Management ---

type CreateProviderRequest struct {
	Name             string `json:"name"`
	URL              string `json:"url"`
	ForwardCallerAuth bool  `json:"forwardCallerAuth"`
	ProviderType     string `json:"providerType,omitempty"`
}

type CreateProviderResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *Client) CreateProvider(ctx context.Context, req *CreateProviderRequest) (*CreateProviderResponse, error) {
	var resp CreateProviderResponse
	err := c.do(ctx, http.MethodPost, "/providers", req, &resp)
	return &resp, err
}

// -- Share Management ---

type CreateShareRequest struct {
	Name string `json:"name"`
}

type CreateShareResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Endpoint       string `json:"endpoint"`
	AuthForwarding bool   `json:"authForwarding"`
	ApiKey         string `json:"apiKey,omitempty"`
}

func (c *Client) CreateShare(ctx context.Context, providerID string, req *CreateShareRequest) (*CreateShareResponse, error) {
	var resp CreateShareResponse
	err := c.do(ctx, http.MethodPost, "/providers/"+providerID+"/shares", req, &resp)
	return &resp, err
}

// -- Shares ---

type Share struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Endpoint       string `json:"endpoint,omitempty"`
	ProviderID     string `json:"parentProviderId,omitempty"`
	AuthForwarding bool   `json:"authForwarding,omitempty"`
	ApiKey         string `json:"apiKey,omitempty"`
}

type SharesResponse struct {
	Shares []Share `json:"shares"`
}

func (c *Client) ListShares(ctx context.Context, providerID string) ([]Share, error) {
	var resp SharesResponse
	err := c.do(ctx, http.MethodGet, "/providers/"+providerID+"/shares", nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Shares, nil
}

// -- Regions ---

type Region struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status,omitempty"`
}

type RegionsResponse struct {
	Regions []Region `json:"regions"`
}

func (c *Client) ListRegions(ctx context.Context) ([]Region, error) {
	var resp RegionsResponse
	err := c.do(ctx, http.MethodGet, "/regions", nil, &resp)
	if err != nil {
		var arr []Region
		err2 := c.do(ctx, http.MethodGet, "/regions", nil, &arr)
		if err2 == nil {
			return arr, nil
		}
		return nil, err
	}
	return resp.Regions, nil
}

// -- Organization ---

type Organization struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Plan      string `json:"plan"`
	CreatedAt string `json:"createdAt,omitempty"`
}

func (c *Client) GetOrganization(ctx context.Context, id string) (*Organization, error) {
	var resp Organization
	err := c.do(ctx, http.MethodGet, "/organizations/"+id, nil, &resp)
	return &resp, err
}

// -- Dashboard / Usage ---

type MetricsEntity struct {
	ID              string  `json:"id"`
	RequestCount    int64   `json:"request_count"`
	AvgLatency      float64 `json:"avg_latency"`
	ErrorRate       float64 `json:"error_rate"`
	PromptTokens    int64   `json:"prompt_tokens"`
	CompletionTok   int64   `json:"completion_tokens"`
	CacheReadTok    int64   `json:"cache_read_tokens"`
	CacheWriteTok   int64   `json:"cache_write_tokens"`
}

type DashboardMetrics struct {
	Summary struct {
		TotalRequests      int64   `json:"total_requests"`
		AvgLatency         float64 `json:"avg_latency"`
		AvgGwLatency       float64 `json:"avg_gateway_latency"`
		AvgProvLatency     float64 `json:"avg_provider_latency"`
		ErrorRate          float64 `json:"error_rate"`
		TotalPromptTok     int64   `json:"total_prompt_tokens"`
		TotalCompletionTok int64   `json:"total_completion_tokens"`
		TotalCacheReadTok  int64   `json:"total_cache_read_tokens"`
		TotalCacheWriteTok int64   `json:"total_cache_write_tokens"`
		ActiveModels       int     `json:"active_models"`
		ActiveUsers        int     `json:"active_users"`
		ActiveProviders    int     `json:"active_providers"`
	} `json:"summary"`
	TopModels    []MetricsEntity `json:"top_models"`
	TopProviders []MetricsEntity `json:"top_providers"`
	TopUsers     []MetricsEntity `json:"top_users"`
}

type SharesMetrics struct {
	Shares []MetricsEntity `json:"shares"`
}

func (c *Client) GetSharesMetrics(ctx context.Context) (*SharesMetrics, error) {
	var resp SharesMetrics
	err := c.do(ctx, http.MethodGet, "/metrics/shares", nil, &resp)
	return &resp, err
}

func (c *Client) GetDashboard(ctx context.Context, period string) (*DashboardMetrics, error) {
	var resp DashboardMetrics
	path := "/metrics/dashboard"
	if period != "" {
		path += "?period=" + period
	}
	err := c.do(ctx, http.MethodGet, path, nil, &resp)
	return &resp, err
}
