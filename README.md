# dtwiz

**Dynatrace Ingest CLI** — analyzes your system and deploys the best Dynatrace observability method.

`dtwiz` is a Go CLI that ports the Python `ingest-agent` to Go. It reuses **dtctl's entire authentication stack** (config loading, multi-context support, OAuth PKCE token refresh, OS keyring, and API token fallback) by importing `github.com/dynatrace-oss/dtctl` as a module dependency.

## Prerequisites

Configure your Dynatrace environment with [dtctl](https://github.com/dynatrace-oss/dtctl):

```bash
# Option 1 – OAuth login (recommended)
dtctl auth login

# Option 2 – API token
dtctl config set-context my-env \
  --environment https://abc12345.apps.dynatrace.com \
  --token dt0c01.XXXX...
```

## Installation

```bash
source <(curl -sSL https://raw.githubusercontent.com/dietermayrhofer/dtwiz/main/scripts/install_dtwiz_linux_mac.sh)
```

> Requires bash or zsh. Using `source <(...)` makes `dtwiz` available in your current terminal immediately — no need to open a new one.

```bash
# From source
git clone https://github.com/dietermayrhofer/dt-clis.git
cd dt-clis/dtwiz
make install
```

## Available commands

| Command | Description |
|---------|-------------|
| `dtwiz analyze` | Detect platform, containers, K8s, existing agents, cloud, and services |
| `dtwiz recommend` | Generate ranked ingestion recommendations |
| `dtwiz setup` | Interactive analyze → recommend → install workflow |
| `dtwiz install oneagent` | Install Dynatrace OneAgent on this host |
| `dtwiz install kubernetes` | Deploy Dynatrace Operator on Kubernetes |
| `dtwiz install docker` | Install OneAgent for Docker |
| `dtwiz install otel-collector` | Install/configure OpenTelemetry Collector |
| `dtwiz install aws` | Set up Dynatrace AWS CloudFormation integration |
| `dtwiz status` | Show Dynatrace connection status and system state |

Use `--context <name>` on any command to override the active dtctl context.

## Example workflow

```bash
# 1. Authenticate via dtctl
dtctl auth login

# 2. Analyze the current system
dtwiz analyze

# 3. Get ranked recommendations
dtwiz recommend

# 4. Install the recommended method (e.g., Kubernetes)
dtwiz install kubernetes

# 5. Check status
dtwiz status
```

## JSON output

`analyze` and `recommend` support `--json` for structured output:

```bash
dtwiz analyze --json | jq .platform
dtwiz recommend --json | jq '.[0].method'
```

## Building

```bash
cd dtwiz
make build        # builds ./dtwiz binary
make test         # runs go test ./...
make install      # installs to $GOPATH/bin
make clean        # removes build artifacts
```

## Architecture

```
dtwiz/
├── main.go
├── cmd/
│   ├── root.go       # Cobra root + --context flag
│   ├── auth.go       # dtctl auth bridge (loadDtctlConfig, newDtClient, getDtEnvironment)
│   ├── analyze.go
│   ├── recommend.go
│   ├── setup.go
│   ├── install.go
│   └── status.go
└── pkg/
    ├── analyzer/     # System detection (platform, Docker, K8s, agents, cloud, services)
    ├── recommender/  # Recommendation engine
    └── installer/    # Shared utilities + per-method stubs
```

Authentication is fully delegated to dtctl — `dtwiz` never stores credentials itself.
