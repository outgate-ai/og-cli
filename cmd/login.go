package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/outgate-ai/og-cli/api"
	"github.com/outgate-ai/og-cli/internal/config"
)

func loginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with Outgate",
		Long:  "Opens a browser to sign in to your Outgate account. After authentication, a CLI token is stored locally.",
		RunE:  loginHandler,
	}

	cmd.Flags().Bool("no-browser", false, "Print the login URL instead of opening a browser")

	return cmd
}

func logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials",
		RunE:  logoutHandler,
	}
}

// callbackResult is sent from the HTTP callback handler to the main goroutine.
type callbackResult struct {
	Token   string
	Email   string
	Name    string
	OrgID   string
	OrgName string
	Scopes  string
	Error   string
}

func loginHandler(cmd *cobra.Command, args []string) error {
	// Check if already logged in
	creds, _ := config.LoadCredentials()
	if creds != nil && creds.Token != "" {
		fmt.Printf("Already logged in as %s (%s)\n", creds.Name, creds.Email)
		fmt.Println("Run 'og logout' first to sign in as a different user.")
		return nil
	}

	noBrowser, _ := cmd.Flags().GetBool("no-browser")

	// Start local HTTP server on a random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("failed to start local server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	resultCh := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		handleCallback(w, r, resultCh)
	})

	server := &http.Server{Handler: mux}
	go func() {
		_ = server.Serve(listener)
	}()

	// Build the auth URL
	consoleURL := config.ConsoleURL()
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	authURL := fmt.Sprintf("%s/cli-auth?callback=%s&scopes=%s", consoleURL, url.QueryEscape(callbackURL), url.QueryEscape("read:account,read:providers,read:regions,read:organizations,read:usage,write:providers,write:shares"))

	if noBrowser {
		fmt.Println("Open this URL in your browser to sign in:")
		fmt.Println()
		fmt.Printf("  %s\n", authURL)
		fmt.Println()
	} else {
		fmt.Println("Opening browser to sign in...")
		if err := browser.OpenURL(authURL); err != nil {
			fmt.Println("Could not open browser. Open this URL manually:")
			fmt.Println()
			fmt.Printf("  %s\n", authURL)
			fmt.Println()
		}
	}

	fmt.Println("Waiting for authentication...")

	// Wait for callback with timeout
	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()

	select {
	case result := <-resultCh:
		_ = server.Shutdown(context.Background())

		if result.Error != "" {
			return fmt.Errorf("authentication failed: %s", result.Error)
		}

		// Parse scopes from comma-separated string
		var scopes []string
		if result.Scopes != "" {
			for _, s := range strings.Split(result.Scopes, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					scopes = append(scopes, s)
				}
			}
		}

		// Save credentials
		creds := &config.Credentials{
			Token:   result.Token,
			Email:   result.Email,
			Name:    result.Name,
			OrgID:   result.OrgID,
			OrgName: result.OrgName,
			Scopes:  scopes,
		}
		if err := config.SaveCredentials(creds); err != nil {
			return fmt.Errorf("failed to save credentials: %w", err)
		}

		fmt.Println()
		fmt.Printf("Logged in as %s (%s)\n", result.Name, result.Email)
		if result.OrgName != "" {
			fmt.Printf("Organization: %s\n", result.OrgName)
		}
		fmt.Println("Credentials saved to ~/.og/credentials.json")
		return nil

	case <-ctx.Done():
		_ = server.Shutdown(context.Background())
		return fmt.Errorf("authentication timed out (5 minutes)")
	}
}

func handleCallback(w http.ResponseWriter, r *http.Request, resultCh chan<- callbackResult) {
	q := r.URL.Query()

	// Check for error
	if errMsg := q.Get("error"); errMsg != "" {
		resultCh <- callbackResult{Error: errMsg}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, successPage("Authentication Failed", "You can close this window and try again.", true))
		return
	}

	// Check for POST body (console posts JSON)
	if r.Method == http.MethodPost {
		var body callbackResult
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.Token != "" {
			resultCh <- body
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"ok": true}`)
			return
		}
	}

	// Query parameter flow
	token := q.Get("token")
	if token == "" {
		resultCh <- callbackResult{Error: "no token received"}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, successPage("Authentication Failed", "No token received. Please try again.", true))
		return
	}

	resultCh <- callbackResult{
		Token:   token,
		Email:   q.Get("email"),
		Name:    q.Get("name"),
		OrgID:   q.Get("org_id"),
		OrgName: q.Get("org_name"),
		Scopes:  q.Get("scopes"),
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, successPage("Authentication Successful", "You can close this window and return to the terminal.", false))
}

func successPage(title, message string, isError bool) string {
	iconHTML := `<div class="icon icon-ok">&#10003;</div>`
	if isError {
		iconHTML = `<div class="icon icon-err">!</div>`
	}

	// Use raw string to avoid fmt.Sprintf %% escaping issues with CSS
	const pageTpl = `<!DOCTYPE html>
<html>
<head><title>{{TITLE}}</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  html, body {
    height: 100%%;
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  }
  .bg {
    position: fixed; top: 0; left: 0; right: 0; bottom: 0;
    background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
    z-index: 0;
  }
  .dots {
    position: fixed; top: 0; left: 0; right: 0; bottom: 0;
    opacity: 0.1; z-index: 1;
    background-image:
      radial-gradient(circle at 25% 25%, #fff 2px, transparent 2px),
      radial-gradient(circle at 75% 75%, #fff 2px, transparent 2px);
    background-size: 50px 50px;
  }
  .wrap {
    position: relative; z-index: 2;
    display: flex; justify-content: center; align-items: center;
    min-height: 100vh; padding: 2rem;
  }
  .card {
    text-align: center; padding: 3rem; border-radius: 12px;
    background: white; box-shadow: 0 20px 60px rgba(0,0,0,0.15);
    max-width: 440px; width: 100%;
  }
  .logo { margin-bottom: 1.25rem; }
  .logo img { height: 28px; }
  .icon {
    width: 48px; height: 48px; border-radius: 50%;
    color: white; font-size: 1.5rem; font-weight: bold;
    display: flex; align-items: center; justify-content: center;
    margin: 0 auto 1rem;
  }
  .icon-ok { background: #48bb78; }
  .icon-err { background: #e53e3e; }
  h1 { font-size: 1.5rem; font-weight: 700; color: #1a202c; margin-bottom: 0.5rem; }
  p { color: #718096; font-size: 0.925rem; line-height: 1.5; }
  .hint { margin-top: 1.25rem; font-size: 0.8rem; color: #a0aec0; }
  .console-link { display: inline-block; margin-top: 1rem; color: #667eea; font-size: 0.875rem; font-weight: 500; text-decoration: none; }
  .console-link:hover { text-decoration: underline; }
</style>
</head>
<body>
  <div class="bg"></div>
  <div class="dots"></div>
  <div class="wrap">
    <div class="card">
      <div class="logo"><img src="https://console.outgate.ai/logos/outgate-dark.png" alt="Outgate" /></div>
      {{ICON}}
      <h1>{{TITLE}}</h1>
      <p>{{MESSAGE}}</p>
      <p class="hint" id="hint">Closing automatically...</p>
      <a href="https://console.dev.outgate.ai" class="console-link">Open Outgate Console</a>
    </div>
  </div>
  <script>
    setTimeout(function(){
      window.close();
      setTimeout(function(){
        document.getElementById('hint').textContent = 'You can close this tab now.';
      }, 500);
    }, 1500);
  </script>
</body>
</html>`

	r := strings.NewReplacer(
		"{{TITLE}}", title,
		"{{MESSAGE}}", message,
		"{{ICON}}", iconHTML,
	)
	return r.Replace(pageTpl)
}

func logoutHandler(cmd *cobra.Command, args []string) error {
	creds, _ := config.LoadCredentials()
	if creds == nil || creds.Token == "" {
		fmt.Println("Not currently logged in.")
		return nil
	}

	// Revoke the token on the server
	client, err := api.NewClient(config.APIBaseURL(), creds.Token, creds.OrgID)
	if err == nil {
		if err := client.RevokeSelfToken(cmd.Context()); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not revoke token on server: %v\n", err)
		}
	}

	if err := config.DeleteCredentials(); err != nil {
		return fmt.Errorf("failed to remove credentials: %w", err)
	}

	fmt.Printf("Logged out (%s)\n", creds.Email)
	return nil
}
