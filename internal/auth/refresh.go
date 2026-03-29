package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/outgate-ai/og-cli/api"
	"github.com/outgate-ai/og-cli/internal/config"
)

// MaybeRefreshToken checks if the token expires within 30 days and refreshes it.
func MaybeRefreshToken(ctx context.Context) error {
	creds, err := config.LoadCredentials()
	if err != nil || creds == nil || creds.Token == "" {
		return nil // not logged in, nothing to refresh
	}

	exp, err := getTokenExpiry(creds.Token)
	if err != nil {
		return nil // can't parse, skip refresh
	}

	daysUntilExpiry := time.Until(exp).Hours() / 24
	if daysUntilExpiry > 30 {
		return nil // not close to expiry
	}

	client, err := api.NewClient(config.APIBaseURL(), creds.Token, creds.OrgID)
	if err != nil {
		return nil
	}

	resp, err := client.RefreshCliToken(ctx)
	if err != nil {
		fmt.Printf("Warning: token refresh failed: %v\n", err)
		return nil // don't block commands
	}

	// Update stored credentials
	creds.Token = resp.Token
	creds.ExpiresAt = resp.ExpiresAt
	creds.Email = resp.User.Email
	creds.Name = resp.User.Name
	creds.OrgID = resp.User.OrganizationID
	creds.OrgName = resp.User.OrganizationName
	creds.Scopes = resp.Scopes

	if err := config.SaveCredentials(creds); err != nil {
		fmt.Printf("Warning: failed to save refreshed token: %v\n", err)
	} else {
		fmt.Println("Token refreshed successfully.")
	}

	return nil
}

// getTokenExpiry extracts the exp claim from a JWT without signature verification.
func getTokenExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("invalid JWT format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, err
	}

	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, err
	}

	return time.Unix(claims.Exp, 0), nil
}
