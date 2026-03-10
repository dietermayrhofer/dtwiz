# dtingest — Agent Context

## Goal

`dtingest` is a CLI tool that makes it effortless to get Dynatrace observability deployed on any system. The core idea: a user should be able to run a single command, and the tool figures out what the best Dynatrace ingestion method is for their environment, then installs it.

## What we want to achieve

- **Zero guesswork for the user.** The tool analyzes the system (OS, container runtime, Kubernetes, cloud provider, existing agents) and recommends the right approach — whether that's the Dynatrace Operator on Kubernetes, OneAgent on a bare-metal host, a Docker-based agent, or an OpenTelemetry Collector.

- **A guided, interactive experience.** `dtingest setup` runs the full flow: analyze → recommend → pick → install. The user doesn't need to know which ingestion method to choose; the tool drives the decision.

- **Reuse dtctl for auth.** Authentication is fully delegated to `dtctl`. The user configures their Dynatrace environment once with `dtctl auth login` or `dtctl config set-context`, and `dtingest` picks it up automatically. No duplicated auth logic.

- **Clear, minimal output.** The CLI is opinionated about not overwhelming the user with information. The system analysis shows what was detected, recommendations are concise and actionable, and the installer guides the remaining steps.

- **Extensible installers.** Each ingestion method (OneAgent, Kubernetes Operator, Docker, OTel Collector, AWS CloudFormation) lives in its own installer module. Adding support for a new method should be straightforward.

## Key design decisions

| Decision | Rationale |
|---|---|
| Auth via dtctl | Avoids reimplementing OAuth PKCE, token refresh, OS keyring, and multi-context config |
| Analyze before recommend | Recommendations are grounded in what's actually on the system, not user input |
| Crisp recommendation output | Details (prerequisites, steps) belong in the installer, not the recommendation list |
| `MethodNotSupported` hidden from recommendations | Platform limitations (e.g. macOS) are noted inline in the analysis, not as a recommendation noise |
| **Prefer `dtctl` shell-out over direct API calls** | See below |

## Prefer dtctl over direct Dynatrace API calls

Whenever `dtingest` needs to query or interact with the Dynatrace platform, **prefer shelling out to `dtctl` over making direct HTTP calls**.

### Why

Direct API calls require managing:
- Which URL variant to use (classic `*.dynatracelabs.com` vs platform `*.apps.dynatracelabs.com`)
- Which token type is valid for the endpoint (OAuth `Bearer` vs classic `Api-Token`)
- Which scopes the token has (e.g. `storage:logs:read` for Grail DQL is only available on platform tokens, not the OAuth tokens `dtctl auth login` issues by default)

`dtctl` already handles all of this. If the user has authenticated and their context is configured correctly, `dtctl` will hit the right URL with the right token automatically.

### Concrete example: Grail log search

Logs ingested via the OTel Collector land in **Grail** storage, not the Classic log store. They are only queryable via DQL on the `.apps.` subdomain — **not** via `/api/v2/logs/search`. Attempting to query them directly requires:

1. Converting the env URL to the apps variant (e.g. `.dynatracelabs.com` → `.apps.dynatracelabs.com`)
2. Posting to `/platform/storage/query/v1/query:execute` with a JSON body
3. A token with `storage:logs:read` scope — which the default OAuth flow does **not** grant

Instead, `dtingest` shells out to `dtctl query`:

```go
out, err := exec.Command("dtctl", "query", "--plain", dqlQuery).Output()
if err == nil && strings.Contains(string(out), searchTerm) {
    // found
}
```

`dtctl query` picks up the active context automatically. The user authenticates once with:

```
dtctl auth login --context myenv-apps --environment https://myenv.apps.dynatracelabs.com
```

and everything works without `dtingest` needing to know about tokens or URL variants.

### Rule of thumb

- **Read/query operations** (logs, metrics, entities): shell out to `dtctl query` or other `dtctl` subcommands.
- **Write/ingest operations** (sending logs, metrics, traces): direct HTTP to the ingest endpoint is fine — those use simple API tokens with narrow ingest-only scopes that are already available.

## Dynatrace URL & API families

Dynatrace exposes **two distinct URL families** that point to the same environment but terminate on different routing layers. Getting this wrong produces 404s or auth errors.

### Environment (classic) APIs — **no** `.apps.`

| | |
|---|---|
| **Pattern** | `https://<env-id>.<cluster-domain>/api/...` |
| **Example** | `https://fxz0998d.dev.dynatracelabs.com/api/v2/metrics` |
| **Auth** | API tokens (`Api-Token …`) |

Hosts: Metrics v2, Entities/topology, Problems & events, Configuration APIs, Logs (environment APIs), OneAgent installer download — everything that historically existed before the Dynatrace Platform.

### Platform & Apps APIs — **with** `.apps.`

| | |
|---|---|
| **Pattern** | `https://<env-id>.apps.<cluster-domain>/platform/...` |
| **Example** | `https://fxz0998d.apps.dev.dynatracelabs.com/platform/storage/query` |
| **Auth** | OAuth / platform tokens |

Hosts: DQL / Grail queries, Platform APIs, AppEngine & app functions, Platform OAuth — the new "platform-first" services.

### Which one to use

| Use **without** `.apps.` | Use **with** `.apps.` |
|---|---|
| Path starts with `/api/v1` or `/api/v2` | Path starts with `/platform/...` |
| Metrics, problems, entities, config | DQL or Grail |
| `Api-Token` authentication | OAuth / platform tokens required |
| Scripts, Terraform, CI, exporters | Dynatrace Apps, AppEngine |

### Why two families exist

Dynatrace is mid-transition from an environment-centric model (`/api/v1`, `/api/v2`, API tokens) to a platform-centric model (`/platform/...`, OAuth). Both stacks run in parallel so existing integrations don't break.

### How dtingest handles this

- **`APIURL()` / `ClassicAPIURL()`** — strip `.apps.` to produce the classic API base URL (used for OneAgent download, `/api/v2` calls).
- **`toAppsURL()`** — insert `.apps.` to produce the platform URL (used for Logs UI deep-links, DQL queries).
- **`dtctl` shell-outs** — `dtctl query` auto-selects the correct URL family based on the active context, so dtingest doesn't need to pick.

> **If an endpoint returns 404 or auth errors, the URL family is usually the problem — not the token.**

## Releases

Releases are built and published with **GoReleaser** (`.goreleaser.yaml`). GoReleaser cross-compiles for all supported platforms, creates archives, and uploads them to the GitHub release.

### How to cut a release

```sh
git tag v0.x.y
git push origin v0.x.y
GITHUB_TOKEN=$(gh auth token) goreleaser release --clean
```

### Asset naming convention (dtingest)

Archives follow GoReleaser's default template:

```
dtingest_{version}_{os}_{arch}.tar.gz   # Linux / macOS
dtingest_{version}_{os}_{arch}.zip      # Windows
```

Examples: `dtingest_0.1.3_darwin_arm64.tar.gz`, `dtingest_0.1.3_linux_amd64.tar.gz`.

The install script (`scripts/install.sh`) constructs this name at runtime and downloads the matching asset from the GitHub release.

### Pitfall: tag exists but release has no assets

`git push --tags` (or the GitHub UI "draft release" flow) can create a lightweight GitHub release with an empty assets list. In that state `dtingest install otel-collector` — and the install script itself — will fail with 404 because there are no binaries to download.

**Fix:** run `goreleaser release --clean` against the existing tag. GoReleaser detects the already-created release and uploads the missing archives.

## Current state

The analyzer detects: platform/OS, container runtime (Docker), Kubernetes (with distribution and context), OneAgent, OTel Collector, AWS, Azure, and running services.

Installers are partially implemented. The recommendation and analysis engine is complete.

---

## Requirements

### R1 — CLI Structure & Commands

| ID | Requirement | Status |
|---|---|---|
| R1.1 | `dtingest` root command with `--context`, `--environment`, `--access-token`, `--platform-token` persistent flags. | ✅ Done |
| R1.2 | `dtingest analyze` — run all system detectors and print a summary. Support `--json` for machine-readable output. | ✅ Done |
| R1.3 | `dtingest recommend` — analyze the system and print ranked ingestion recommendations. Support `--json`. | ✅ Done |
| R1.4 | `dtingest setup` — interactive guided workflow (analyze → recommend → user picks → install). Support `--dry-run`. | ✅ Done |
| R1.5 | `dtingest install <method>` — parent command for method-specific installers. Support `--dry-run` on all sub-commands. | ✅ Done |
| R1.6 | `dtingest install oneagent` — download and run the OneAgent installer on Linux/Windows hosts. | ✅ Done |
| R1.7 | `dtingest install kubernetes` — deploy the Dynatrace Operator via Helm and apply DynaKube CRs. | ✅ Done |
| R1.8 | `dtingest install docker` — run OneAgent as a privileged Docker container. | ✅ Done |
| R1.9 | `dtingest install otel-collector` — download the Dynatrace OTel Collector binary, write config, start the process, and verify log delivery. | ✅ Done |
| R1.10 | `dtingest install otel-update` — patch an existing OTel Collector YAML config with the Dynatrace OTLP exporter. Support `--config <path>`. | ✅ Done |
| R1.11 | `dtingest install otel-python` — install OTel Python auto-instrumentation packages and print required env vars. Support `--service-name`. | ✅ Done |
| R1.12 | `dtingest install aws` — deploy the Dynatrace AWS Data Acquisition CloudFormation stack with interactive prompts for tokens and parameters. | ✅ Done |
| R1.13 | `dtingest uninstall kubernetes` — remove DynaKube CRs, wait for managed pods, Helm uninstall, delete namespace. | ✅ Done |
| R1.14 | `dtingest status` — verify Dynatrace connectivity (logged-in user, environment URL) and print system analysis. | ✅ Done |
| R1.15 | Every destructive install/uninstall command must show a preview of what will be done and prompt for user confirmation before executing. | ✅ Done |

### R2 — System Analyzer

| ID | Requirement | Status |
|---|---|---|
| R2.1 | Detect platform (Linux, macOS, Windows) and architecture (`amd64`, `arm64`). | ✅ Done |
| R2.2 | Detect Docker: availability, server version, running container count. | ✅ Done |
| R2.3 | Detect Kubernetes: cluster reachability, current context, cluster name, server version, node count, and distribution heuristic (GKE, EKS, AKS, OpenShift, k3s, minikube, kind, generic). | ✅ Done |
| R2.4 | Detect OneAgent: check `/opt/dynatrace/oneagent` path and `oneagentctl` in PATH. | ✅ Done |
| R2.5 | Detect OTel Collector: find running `otelcol`/`otelcol-contrib`/`otel-collector` processes, extract binary path and `--config=` path from the process command line. | ✅ Done |
| R2.6 | Detect AWS: verify `aws sts get-caller-identity`, extract account ID and region. Probe services concurrently (EC2, EKS, ECS, Lambda, RDS, S3) and report counts. | ✅ Done |
| R2.7 | Detect Azure: verify `az account show`, extract subscription ID and tenant ID. Probe services concurrently (VMs, AKS, Functions, App Services, SQL DBs, Storage) with counts. Handle MFA expiration gracefully. | ✅ Done |
| R2.8 | Detect installed application runtimes (`java`, `node`, `python3`, `go` via `which`) and running daemons (`nginx`, `postgres`, `mysql`, `redis`, `mongodb` via `pgrep`). | ✅ Done |
| R2.9 | All detectors run concurrently with a 10-second per-command timeout. Detection failures are non-fatal. | ✅ Done |
| R2.10 | `SystemInfo` struct supports JSON serialization for use with `--json` output and programmatic consumers. | ✅ Done |
| R2.11 | Human-readable `Summary()` output with colored labels, consistent column alignment, and platform-specific notes (e.g. "macOS not supported" for OneAgent). | ✅ Done |
| R2.12 | Detect GCP environment. | ❌ Not started |

### R3 — Recommendation Engine

| ID | Requirement | Status |
|---|---|---|
| R3.1 | If OneAgent is already running, return a single "already installed" recommendation with `Done: true` and stop. | ✅ Done |
| R3.2 | If Kubernetes is detected, recommend Dynatrace Operator deployment (priority 10). | ✅ Done |
| R3.3 | If Docker is detected without Kubernetes, recommend Docker OneAgent container (priority 20). | ✅ Done |
| R3.4 | If bare-metal Linux or Windows (no containers), recommend host OneAgent install (priority 30). | ✅ Done |
| R3.5 | If AWS is detected, recommend CloudFormation integration (priority 40). | ✅ Done |
| R3.6 | If OTel Collector is running, recommend configuring it with the Dynatrace exporter (priority 50). | ✅ Done |
| R3.7 | Recommendations include `Method`, `Priority`, `Title`, `Description`, `Prerequisites`, and `Steps` fields. | ✅ Done |
| R3.8 | `MethodNotSupported` entries (e.g. macOS OneAgent) are hidden from recommendation output and noted inline in analysis instead. | ✅ Done |
| R3.9 | Formatted output uses colored badges: `✓` for done, numbered for actionable, `!` for not-supported. Each actionable recommendation shows the `dtingest install <method>` command. | ✅ Done |
| R3.10 | If Azure is detected, recommend Azure integration. | ❌ Not started |

### R4 — Authentication & dtctl Integration

| ID | Requirement | Status |
|---|---|---|
| R4.1 | `dtctl` must be installed and on PATH; if missing, prompt the user to auto-download the latest release binary from GitHub. | ✅ Done |
| R4.2 | Auto-download resolves OS/arch from `runtime.GOOS`/`runtime.GOARCH`, picks the matching release asset, installs to a writable directory (`/usr/local/bin`, `~/.local/bin`, or `~/bin`), and warns if not in PATH. | ✅ Done |
| R4.3 | Load dtctl configuration via `dtconfig.Load()`. Support `--context` flag to override the active context. | ✅ Done |
| R4.4 | Create a `dtclient.Client` from the dtctl config, supporting both OAuth PKCE tokens and plain API tokens. | ✅ Done |
| R4.5 | `getDtEnvironment()` resolves the environment URL and token. Resolution order: `--environment` flag → `DT_ENVIRONMENT` env var → dtctl context. | ✅ Done |
| R4.6 | Auto-recovery: if credentials are expired or missing, run `dtctl auth login` interactively and retry once. Derive the context name from the environment URL's first DNS label. | ✅ Done |
| R4.7 | `accessToken()`: resolve from `--access-token` flag or `DT_ACCESS_TOKEN` env var. Used for classic API operations. | ✅ Done |
| R4.8 | `platformToken()`: resolve from `--platform-token` flag or `DT_PLATFORM_TOKEN` env var. Used for the AWS installer. | ✅ Done |
| R4.9 | `AppsContext()`: inspect `dtctl config get-contexts` to find a context with an `.apps.` URL for DQL queries. Auto-detect and override if the current context uses a classic URL. | ✅ Done |
| R4.10 | `Reauth()`: run `dtctl auth login` interactively for a specific context when a token refresh fails. | ✅ Done |
| R4.11 | `IsAuthError()` / `IsTokenRefreshError()`: pattern-match error messages to detect recoverable auth failures (expired tokens, invalid grants, 401s). | ✅ Done |

### R5 — Installer: OneAgent (Host)

| ID | Requirement | Status |
|---|---|---|
| R5.1 | Support Linux (amd64, arm64) and Windows. Reject macOS with a clear error message. | ✅ Done |
| R5.2 | Map environment URLs: `.apps.dynatrace.com` → `.live.dynatrace.com`, `.apps.dynatracelabs.com` → `.dynatracelabs.com`. | ✅ Done |
| R5.3 | Connectivity check: `GET /api/v1/time` with the auth token before downloading. Report 401 clearly. | ✅ Done |
| R5.4 | Download the installer from `/api/v1/deployment/installer/agent/{type}/latest/default?arch={arch}`. Save to a temp file, make executable. | ✅ Done |
| R5.5 | Run the installer with `--set-server` and `--set-app-log-content-access=true`. Prepend `sudo` on Linux if current user is not root. | ✅ Done |
| R5.6 | Dry-run mode prints installer type, arch, API URL, and install mode without executing. | ✅ Done |

### R6 — Installer: Kubernetes (Dynatrace Operator)

| ID | Requirement | Status |
|---|---|---|
| R6.1 | Install or auto-install Helm if not present (via `get-helm-3` script). | ✅ Done |
| R6.2 | Detect Helm major version (v3 vs v4+) and use `--atomic` or `--rollback-on-failure` accordingly. | ✅ Done |
| R6.3 | Install or upgrade the `dynatrace-operator` Helm chart from `oci://public.ecr.aws/dynatrace/dynatrace-operator` into the `dynatrace` namespace. | ✅ Done |
| R6.4 | Auto-derive the DynaKube name from the kubectl cluster name, sanitized to a valid RFC 1123 DNS label (max 63 chars). | ✅ Done |
| R6.5 | Render the DynaKube YAML manifest from an embedded Go template (`dynakube.tmpl`) with cluster name, API URL, tokens, and EEC repository. | ✅ Done |
| R6.6 | Show a full preview: rendered YAML manifest, Helm command, and kubectl apply command. Require explicit user confirmation. | ✅ Done |
| R6.7 | Apply the manifest (Secret + DynaKube CRs) via `kubectl apply -f`. | ✅ Done |
| R6.8 | Wait for all pods in the `dynatrace` namespace to become ready, with ActiveGate readiness as a gate. Poll every 5s up to 10 minutes, with a live-updating progress line. | ✅ Done |
| R6.9 | Uninstall: delete all DynaKube + EdgeConnect CRs → wait for managed pods to terminate (5 min timeout) → Helm uninstall → delete namespace. | ✅ Done |

### R7 — Installer: Docker

| ID | Requirement | Status |
|---|---|---|
| R7.1 | Pre-flight check: `docker info` must succeed. | ✅ Done |
| R7.2 | Run the `dynatrace/oneagent` image as a detached privileged container with `--pid=host`, `--net=host`, and `/:/mnt/root` volume mount. | ✅ Done |
| R7.3 | Pass `DT_SERVER`, `DT_TENANT`, `DT_TENANT_TOKEN` env vars derived from the environment URL and token. | ✅ Done |
| R7.4 | Remove any existing container with the same name (`dynatrace-oneagent`) before starting. | ✅ Done |

### R8 — Installer: OpenTelemetry Collector

| ID | Requirement | Status |
|---|---|---|
| R8.1 | Resolve the latest Dynatrace OTel Collector release version by following the GitHub `/releases/latest` redirect (avoid API rate limits). | ✅ Done |
| R8.2 | Construct the correct platform asset name: `dynatrace-otel-collector_{version}_{OS}_{arch}.tar.gz` (or `.zip` on Windows). Support Linux, macOS, Windows × amd64, arm64. | ✅ Done |
| R8.3 | Download the release archive, extract the collector binary from `.tar.gz` or `.zip`. | ✅ Done |
| R8.4 | macOS preparation: strip quarantine extended attributes (`xattr -cr`) and apply an ad-hoc code signature (`codesign --force --deep --sign -`). | ✅ Done |
| R8.5 | Generate the collector config from an embedded template (`otel.tmpl`) with the Dynatrace endpoint and auth header. Show a token-redacted preview to the user. | ✅ Done |
| R8.6 | Detect and offer to kill any already-running `dynatrace-otel-collector` processes before starting the new one. | ✅ Done |
| R8.7 | Start the collector as a background process. Filter stderr: suppress `info`-level structured log lines, forward `warn`/`error`/`fatal`. Detect immediate startup crashes (within 3 seconds). Then release/detach the process. | ✅ Done |
| R8.8 | Verification: wait for port 4318 (TCP, IPv4 loopback), send an OTLP log record containing a unique install ID, then poll `dtctl query` until the log appears in Dynatrace Grail. | ✅ Done |
| R8.9 | Print a clickable terminal hyperlink (OSC 8) to the Dynatrace Logs UI filtered to the verification log. | ✅ Done |
| R8.10 | Auto-detect an `.apps.` dtctl context for DQL queries. Auto-re-authenticate (once) if the token has expired. Abort after 3 consecutive dtctl query failures with a diagnostic message. | ✅ Done |
| R8.11 | `otel-update` sub-command: read an existing YAML config, create a timestamped `.bak` backup, deep-merge the `otlphttp/dynatrace` exporter into `exporters`, and append it to every `service.pipelines.*.exporters` list. | ✅ Done |
| R8.12 | `otel-python` sub-command: detect Python 3 and pip, install `opentelemetry-api`, `opentelemetry-sdk`, `opentelemetry-exporter-otlp`, `opentelemetry-instrumentation`. Print `OTEL_*` env var export script. Warn if no virtualenv is active. | ✅ Done |

### R9 — Installer: AWS CloudFormation

| ID | Requirement | Status |
|---|---|---|
| R9.1 | Check `aws` CLI is installed. Verify credentials via `aws sts get-caller-identity`. Handle `ExpiredToken` with a clear message. | ✅ Done |
| R9.2 | Interactive prompts for: stack name, Dynatrace URL, settings token (with scopes `settings:objects:write`, `extensions:configurations:write/read`), ingest token (with scopes `data-acquisition:logs:ingest`, `data-acquisition:events:ingest`). Secret prompts mask existing values as `[set]`. | ✅ Done |
| R9.3 | Require `DT_ACCESS_TOKEN` (classic `dt0c01.*` token) for the monitoring configuration API, since platform tokens are rejected by `/api/v2`. | ✅ Done |
| R9.4 | Auto-create (or reuse existing) Dynatrace `com.dynatrace.extension.da-aws` monitoring configuration for the detected AWS account+region. The configuration uses `QUICK_START` mode with standard feature sets. | ✅ Done |
| R9.5 | Render the CloudFormation parameters from an embedded template (`aws.tmpl`). Download the pinned Dynatrace CloudFormation template from S3. | ✅ Done |
| R9.6 | Show a full preview of stack parameters and the `aws cloudformation deploy` command. Require user confirmation. | ✅ Done |
| R9.7 | Deploy with `aws cloudformation deploy` using `CAPABILITY_NAMED_IAM`. Logs ingestion is enabled by default for all AWS regions; events ingestion is disabled by default. | ✅ Done |

### R10 — URL & Token Handling

| ID | Requirement | Status |
|---|---|---|
| R10.1 | `APIURL()` / `ClassicAPIURL()`: convert `.apps.dynatrace.com` → `.live.dynatrace.com` and `.apps.dynatracelabs.com` → `.dynatracelabs.com`. | ✅ Done |
| R10.2 | `toAppsURL()`: convert classic URLs to `.apps.` variant by inserting `.apps.` before the domain suffix. No-op if already contains `.apps.`. | ✅ Done |
| R10.3 | `ExtractTenantID()`: extract the first DNS label from the environment URL. | ✅ Done |
| R10.4 | `AuthHeader()`: use `Api-Token <token>` for `dt0c01.*` tokens, `Bearer <token>` for everything else. | ✅ Done |

### R11 — Cross-Cutting Concerns

| ID | Requirement | Status |
|---|---|---|
| R11.1 | All installers support `--dry-run` mode that shows what would be executed without making changes. | ✅ Done |
| R11.2 | Colored terminal output throughout: cyan for headers, white-bold for labels, dim gray for muted/separator text, green for success, red for errors. (Uses `fatih/color`.) | ✅ Done |
| R11.3 | `sudo` detection on Unix (via `needsSudo`); platform-specific implementations for Unix and Windows (`sudo_unix.go` / `sudo_windows.go`). | ✅ Done |
| R11.4 | `RunCommand()` streams stdout/stderr to the terminal. `RunCommandQuiet()` suppresses stdout but captures stderr for error reporting. | ✅ Done |
| R11.5 | Build with Go 1.24+. Dependencies: `spf13/cobra` (CLI framework), `fatih/color` (terminal colors), `dynatrace-oss/dtctl` (auth/config), `gopkg.in/yaml.v3` (YAML parsing). | ✅ Done |
| R11.6 | Build via `Makefile` targets: `build`, `install`, `test`, `lint`, `clean`. | ✅ Done |
| R11.7 | Cross-platform releases via GoReleaser with archives for Linux, macOS, Windows (amd64+arm64). Install scripts for shell (`install.sh`) and PowerShell (`install.ps1`). | ✅ Done |

### R12 — Testing

| ID | Requirement | Status |
|---|---|---|
| R12.1 | `analyzer_test.go`: verify `AnalyzeSystem()` returns the correct platform and arch for the current OS, and that `Summary()` is non-empty. | ✅ Done |
| R12.2 | `analyzer_test.go`: table-driven tests for `DetectK8sDistribution()` covering GKE, EKS, AKS, OpenShift, k3s, minikube, kind, and generic. | ✅ Done |
| R12.3 | `recommender_test.go`: verify recommendation output for OneAgent-already-running, Kubernetes, Docker-only, bare-metal Linux, and macOS (no `MethodNotSupported` entry). | ✅ Done |
| R12.4 | `recommender_test.go`: verify `FormatRecommendations(nil)` does not return an empty string. | ✅ Done |
| R12.5 | Add unit tests for URL conversion helpers (`APIURL`, `ClassicAPIURL`, `toAppsURL`, `ExtractTenantID`). | ❌ Not started |
| R12.6 | Add unit tests for `AuthHeader()` and token type detection. | ❌ Not started |
| R12.7 | Add unit tests for the OTel config merge logic (`mergeDynatraceExporter`). | ❌ Not started |
| R12.8 | Add integration/E2E test for the `setup` flow. | ❌ Not started |
