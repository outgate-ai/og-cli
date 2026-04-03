# OG CLI Configuration

The OG CLI uses a layered configuration system. Settings are resolved in priority order — higher layers override lower ones.

## Resolution Order

```
1. CLI flags          (highest priority)
2. .og.yaml           project-level config (walk-up search)
3. ~/.og/config.json  global user config
4. Environment vars   OG_* prefixed
5. Build-time defaults (lowest priority)
```

## Configuration Files

### `~/.og/config.json` — Global Config

Created automatically on login. Stores user-level settings and credentials reference.

```json
{
  "api_base": "https://console.outgate.ai/api",
  "console_url": "https://console.outgate.ai",
  "region_id": "reg-abc123",
  "region_name": "eu-central-1"
}
```

### `~/.og/credentials.json` — Auth Token

Managed by `og login` / `og logout`. Do not edit manually.

```json
{
  "token": "og_cli_...",
  "email": "you@example.com",
  "org_id": "o-abc123",
  "org_name": "My Org"
}
```

### `.og.yaml` — Project Config

Place in your project root (or any parent directory). The CLI walks up from the current directory to find the nearest `.og.yaml`, similar to `.gitignore`.

```yaml
# .og.yaml — Project-level Outgate configuration

# Provider to use (name or ID)
provider: "Anthropic"

# Project name (used for share naming in og claude / og codex)
project: "my-app"

# Region override (optional — defaults to active region from global config)
region: "reg-abc123"

# API base URL override (optional)
api_base: "https://console.outgate.ai/api"

# Scan settings (for og scan)
scan:
  # File extensions to include (overrides defaults)
  extensions:
    - ".py"
    - ".ts"
    - ".js"
    - ".yaml"
    - ".yml"
    - ".json"
    - ".env"
    - ".toml"
    - ".sh"

  # Additional directories to exclude (appended to defaults)
  exclude_dirs:
    - "test_data"
    - ".terraform"

  # File patterns to skip
  exclude_files:
    - "*.min.js"
    - "*.map"
    - "package-lock.json"

  # Max file size in bytes (default: 1048576 = 1MB)
  max_file_size: 2097152
```

## Environment Variables

| Variable | Description | Example |
|---|---|---|
| `OG_API_BASE` | API base URL | `https://console.outgate.ai/api` |
| `OG_PROVIDER` | Default provider name or ID | `Anthropic` |
| `OG_PROJECT` | Default project name | `my-app` |
| `OG_REGION` | Default region ID | `reg-abc123` |
| `OG_TOKEN` | Auth token (alternative to login) | `og_cli_...` |
| `OG_CONSOLE_URL` | Console URL | `https://console.outgate.ai` |

## CLI Flags

Flags always take highest priority. Available on relevant commands:

| Flag | Commands | Description |
|---|---|---|
| `--provider` | `scan`, `claude`, `codex` | Provider name or ID |
| `--project` | `scan` | Project directory path |
| `--name` | `claude`, `codex` | Project name for share |

## Commands

### `og login`

Authenticate with your Outgate account. Opens the browser for OAuth flow.

```bash
og login
```

### `og status`

Show current authentication and configuration status.

```bash
og status
```

### `og region select`

Select the active region for all commands.

```bash
og region select
```

### `og scan`

Scan project files for sensitive data using the guardrail service. Detections are stored in the Detection Vault for fast matching on future requests.

```bash
# Scan current directory using provider from .og.yaml
og scan

# Scan with explicit provider
og scan --provider "Local Ollama"

# Scan a specific directory
og scan --provider my-provider --project /path/to/project
```

**Requirements:**
- Provider must have guardrail enabled
- Provider must have a guardrail policy attached
- Region must be selected (via `og region select` or `.og.yaml`)

**What it scans:**
- Text files matching configured extensions (`.ts`, `.py`, `.env`, etc.)
- Skips binary files, `node_modules/`, `.git/`, and other excluded directories
- Files larger than `max_file_size` (default 1MB) are skipped

**How it works:**
1. Sends each file's content through the provider with the `X-Outgate-Guardrail: dry-run` header
2. The guardrail LLM evaluates the content for PII, credentials, and sensitive data
3. Detections are stored in the Detection Vault (Redis KV fingerprint store)
4. The request never reaches the upstream provider
5. Future real requests can match against stored fingerprints without calling the LLM

### `og claude [args...]`

Route Claude Code traffic through Outgate. All arguments are passed directly to the `claude` binary.

```bash
# Use provider from .og.yaml
og claude

# Override provider
og claude --provider "Anthropic"

# Pass arguments to claude
og claude --model claude-opus-4-6
```

### `og codex [args...]`

Route Codex traffic through Outgate. Same behavior as `og claude` but for OpenAI Codex.

```bash
og codex --provider "OpenAI"
```

### `og env`

Print environment variables that would be set for tool wrapping.

```bash
og env claude
og env codex
```

## Example `.og.yaml` for Common Setups

### Python Project

```yaml
provider: "Anthropic"
project: "ml-pipeline"
scan:
  extensions: [".py", ".yaml", ".json", ".env", ".toml", ".cfg"]
  exclude_dirs: [".venv", "__pycache__", ".mypy_cache"]
  exclude_files: ["*.pyc"]
```

### Node.js Project

```yaml
provider: "OpenAI"
project: "web-app"
scan:
  extensions: [".ts", ".js", ".json", ".yaml", ".env"]
  exclude_dirs: ["node_modules", "dist", ".next"]
  exclude_files: ["*.min.js", "*.map", "package-lock.json"]
```

### Monorepo

```yaml
provider: "Local Ollama"
project: "platform"
scan:
  extensions: [".ts", ".js", ".py", ".go", ".yaml", ".json", ".env", ".tf"]
  exclude_dirs: ["node_modules", "vendor", "dist", "build", ".terraform"]
  max_file_size: 2097152  # 2MB
```
