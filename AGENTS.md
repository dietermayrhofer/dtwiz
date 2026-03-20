# dtwiz — Agent Context

## Goal

`dtwiz` is a CLI tool that makes it effortless to get Dynatrace observability deployed on any system. The core idea: a user should be able to run a single command, and the tool figures out what the best Dynatrace ingestion method is for their environment, then installs it.

## What we want to achieve

- **Zero guesswork for the user.** The tool analyzes the system (OS, container runtime, Kubernetes, cloud provider, existing agents) and recommends the right approach — whether that's the Dynatrace Operator on Kubernetes, OneAgent on a bare-metal host, a Docker-based agent, or an OpenTelemetry Collector.

- **A guided, interactive experience.** `dtwiz setup` runs the full flow: analyze → recommend → pick → install. The user doesn't need to know which ingestion method to choose; the tool drives the decision.

- **Reuse dtctl for auth.** Authentication is fully delegated to `dtctl`. The user configures their Dynatrace environment once with `dtctl auth login` or `dtctl config set-context`, and `dtwiz` picks it up automatically. No duplicated auth logic.

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

Whenever `dtwiz` needs to query or interact with the Dynatrace platform, **prefer shelling out to `dtctl` over making direct HTTP calls**.

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

Instead, `dtwiz` shells out to `dtctl query`:

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

and everything works without `dtwiz` needing to know about tokens or URL variants.

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

### How dtwiz handles this

- **`APIURL()` / `ClassicAPIURL()`** — strip `.apps.` to produce the classic API base URL (used for OneAgent download, `/api/v2` calls).
- **`toAppsURL()`** — insert `.apps.` to produce the platform URL (used for Logs UI deep-links, DQL queries).
- **`dtctl` shell-outs** — `dtctl query` auto-selects the correct URL family based on the active context, so dtwiz doesn't need to pick.

> **If an endpoint returns 404 or auth errors, the URL family is usually the problem — not the token.**

## Releases

Releases are built and published with **GoReleaser** (`.goreleaser.yaml`). GoReleaser cross-compiles for all supported platforms, creates archives, and uploads them to the GitHub release.

### How to cut a release

```sh
git tag v0.x.y
git push origin v0.x.y
GITHUB_TOKEN=$(gh auth token) goreleaser release --clean
```

### Asset naming convention (dtwiz)

Archives follow GoReleaser's default template:

```
dtwiz_{version}_{os}_{arch}.tar.gz   # Linux / macOS
dtwiz_{version}_{os}_{arch}.zip      # Windows
```

Examples: `dtwiz_0.1.3_darwin_arm64.tar.gz`, `dtwiz_0.1.3_linux_amd64.tar.gz`.

The install script (`scripts/install.sh`) constructs this name at runtime and downloads the matching asset from the GitHub release.

### Pitfall: tag exists but release has no assets

`git push --tags` (or the GitHub UI "draft release" flow) can create a lightweight GitHub release with an empty assets list. In that state `dtwiz install otel` — and the install script itself — will fail with 404 because there are no binaries to download.

**Fix:** run `goreleaser release --clean` against the existing tag. GoReleaser detects the already-created release and uploads the missing archives.

## Dynatrace ingestion methods — full landscape

The Dynatrace documentation at `docs.dynatrace.com/docs/ingest-from` organizes all ingestion methods into distinct categories. `dtwiz` should understand this full landscape and enable as many as applicable **by default** — the zero-config philosophy means turning on all relevant defaults automatically, not asking the user to pick.

### Ingestion method categories

#### 1. OneAgent (full-stack, host-level)

OneAgent is Dynatrace's primary data collection agent. A single OneAgent per host collects **all** monitoring data — infrastructure metrics, process monitoring, distributed traces, log ingestion, code-level profiling, and real user monitoring (RUM) injection. OneAgent auto-discovers processes and activates technology-specific instrumentation automatically (Java, .NET, Node.js, Go, PHP, Python, etc.).

| Deployment target | How dtwiz handles it |
|---|---|
| **Bare-metal Linux/Windows** | `dtwiz install oneagent` — downloads and runs the installer |
| **Docker hosts** | `dtwiz install docker` — runs `dynatrace/oneagent` as a privileged container |
| **Kubernetes** | `dtwiz install kubernetes` — deploys the Dynatrace Operator + DynaKube CR |
| **macOS** | Not supported — noted in analysis output |

**Zero-config defaults for OneAgent:**
- `--set-app-log-content-access=true` — enables log content access immediately
- Full-stack monitoring mode (not infrastructure-only) — captures traces, metrics, logs, RUM
- Auto-instrumentation of all detected technologies — no per-technology opt-in required

#### 2. Dynatrace Operator (Kubernetes)

The Dynatrace Operator is the recommended way to deploy Dynatrace on Kubernetes. It manages OneAgent pods, ActiveGate instances, and metadata enrichment via the DynaKube custom resource. Supports all major distributions: GKE, EKS, AKS, OpenShift, k3s, minikube, kind.

**What the Operator enables by default (via DynaKube `cloudNativeFullStack`):**
- OneAgent injection into all pods (via init containers / CSI driver)
- ActiveGate for routing and Kubernetes API monitoring
- Kubernetes cluster metrics, events, and workload monitoring
- Automatic log collection from all pod stdout/stderr
- Extension Execution Controller (EEC) for Dynatrace Extensions Framework 2.0

**Zero-config principle:** `dtwiz install kubernetes` deploys with `cloudNativeFullStack` mode — the most comprehensive option — rather than asking users to choose between infrastructure-only, application-only, or full-stack modes.

#### 3. ActiveGate

ActiveGate acts as a secure proxy between OneAgents and the Dynatrace cluster, and also performs monitoring of cloud environments and remote technologies. In Kubernetes, ActiveGate is deployed automatically by the Operator. For standalone use:

- **Routing purpose:** proxy OneAgent traffic through a local gateway (useful for restricted networks)
- **Monitoring purpose:** cloud platform monitoring (AWS, Azure, GCP), VMware, SNMP, WMI, Prometheus scraping
- **Synthetic purpose:** run synthetic monitors from private locations

**dtwiz does not directly install standalone ActiveGate** — it is deployed automatically as part of the Kubernetes Operator installation. Standalone ActiveGate installation may be added as a future installer.

#### 4. OpenTelemetry (OTLP native ingest)

Dynatrace natively ingests OpenTelemetry data via OTLP/HTTP. This is the primary open-standards-based ingestion path.

**OTLP endpoint:** `https://<tenant>.live.dynatrace.com/api/v2/otlp`
- Accepts traces, metrics, and logs
- Uses `Api-Token` authentication with scopes: `openTelemetryTrace.ingest`, `metrics.ingest`, `logs.ingest`
- Only HTTP is supported (not gRPC)
- Metrics must use **delta temporality** (use `cumulativetodelta` processor in the Collector)

**Three ingestion paths for OTel data:**

| Path | When to use | dtwiz support |
|---|---|---|
| **Dynatrace OTel Collector** | Standalone host — collects and forwards all signals | `dtwiz install otel` |
| **Existing OTel Collector** | Already running collector — add Dynatrace exporter | `dtwiz install otel-update` |
| **OTel SDK direct export** | Application sends OTLP directly (no collector) | Supported by runtime; dtwiz sets env vars |

**Dynatrace OTel Collector distribution** (from `github.com/Dynatrace/dynatrace-otel-collector`):
- Curated set of receivers, processors, and exporters verified by Dynatrace
- Independent security patches from upstream OpenTelemetry releases
- Covered by Dynatrace support
- Supports Linux, macOS, Windows × amd64, arm64

**OTel Collector config template** (what dtwiz generates):
```yaml
receivers:
  otlp:
    protocols:
      http:
        endpoint: "0.0.0.0:4318"
exporters:
  otlphttp/dynatrace:
    endpoint: "https://<tenant>.live.dynatrace.com/api/v2/otlp"
    headers:
      Authorization: "Api-Token <token>"
processors:
  cumulativetodelta: {}
  batch: {}
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlphttp/dynatrace]
    metrics:
      receivers: [otlp]
      processors: [cumulativetodelta, batch]
      exporters: [otlphttp/dynatrace]
    logs:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlphttp/dynatrace]
```

**Zero-config defaults:**
- All three signal pipelines (traces, metrics, logs) enabled by default
- `cumulativetodelta` processor included automatically for metrics
- Batch processor for efficient delivery
- OTLP HTTP receiver on port 4318 for receiving data from instrumented apps

**OTel auto-instrumentation for application runtimes:**

| Runtime | dtwiz support | Packages installed |
|---|---|---|
| Python | `dtwiz install otel-python` | `opentelemetry-api`, `opentelemetry-sdk`, `opentelemetry-exporter-otlp`, `opentelemetry-instrumentation` |
| Java | Future: `dtwiz install otel-java` | OpenTelemetry Java agent JAR |
| Node.js | Future: `dtwiz install otel-node` | `@opentelemetry/auto-instrumentations-node` |
| Go | Future: `dtwiz install otel-go` | Manual instrumentation guidance |
| .NET | Future: `dtwiz install otel-dotnet` | OpenTelemetry .NET auto-instrumentation |
| Ruby | Future | OTel Ruby SDK packages |
| PHP | Future | OTel PHP auto-instrumentation |

#### 5. Cloud platform integrations

Dynatrace integrates with all major cloud platforms to ingest cloud service metrics, logs, and topology data. These integrations run alongside (not instead of) OneAgent for full-stack visibility.

##### AWS

**What gets ingested:**
- CloudWatch metrics for all AWS services (EC2, EKS, ECS, Lambda, RDS, S3, DynamoDB, API Gateway, SQS, SNS, Kinesis, etc.)
- AWS CloudTrail logs and events
- Cloud topology and tag-based enrichment
- Cost and resource utilization data

**How dtwiz sets it up:**
- Deploys a CloudFormation stack (`dtwiz install aws`) that creates IAM roles for Dynatrace to read CloudWatch metrics
- Auto-creates the `com.dynatrace.extension.da-aws` monitoring configuration via the Dynatrace API
- Enables log ingestion for all AWS regions by default
- Uses `QUICK_START` mode with all standard feature sets enabled

**Zero-config defaults:**
- All detected AWS services are monitored automatically
- Log forwarding enabled across all regions
- No per-service opt-in required — everything is on by default

##### Azure

**What gets ingested:**
- Azure Monitor metrics for all Azure services (VMs, AKS, Functions, App Services, SQL, Storage, Cosmos DB, Event Hubs, etc.)
- Azure Activity Log events
- Cloud topology with resource group and subscription context
- Tag-based enrichment for ownership and cost allocation

**How dtwiz should set it up (planned — R3.10):**
- Register a Dynatrace Azure integration via the Dynatrace API
- Create an Azure AD app registration with Reader role for metric collection
- Enable log forwarding via Azure Event Hub or direct ingestion

**Zero-config defaults (planned):**
- All detected Azure services monitored automatically
- Activity Log forwarding enabled
- Out-of-the-box dashboards and alert templates activated

##### Google Cloud Platform (GCP)

**What gets ingested:**
- Google Cloud Operations API (formerly Stackdriver) metrics for GCE, GKE, Cloud Functions, Cloud SQL, Cloud Storage, Pub/Sub, Datastore, Memorystore, Load Balancers
- GCP topology and label-based enrichment
- Cloud Functions monitoring via OpenTelemetry (GCP client SDKs are pre-instrumented with OTel)

**Deployment model:**
- Uses the open-source `dynatrace-gcp-function` (Google Cloud Functions that forward metrics to Dynatrace)
- OneAgent for full-stack monitoring on GCE instances and GKE nodes
- OpenTelemetry for serverless (Cloud Functions, Cloud Run)

**How dtwiz should set it up (planned — R2.12):**
- Detect GCP environment via metadata service or `gcloud` CLI
- Deploy the `dynatrace-gcp-function` integration
- Configure GCP service account with monitoring read permissions

#### 6. Log ingestion paths

Dynatrace supports multiple log ingestion paths, all landing in Grail for unified DQL-based querying:

| Path | Data flow | dtwiz relevance |
|---|---|---|
| **OneAgent log monitoring** | OneAgent reads local log files and container stdout | Enabled by default with `--set-app-log-content-access=true` |
| **OTel Collector log pipeline** | OTLP logs → Collector → Dynatrace OTLP endpoint | `dtwiz install otel` (logs pipeline enabled) |
| **Log ingest API** | Direct HTTP POST to `/api/v2/logs/ingest` | Used for verification in `dtwiz install otel` |
| **Cloud log forwarding** | AWS CloudTrail / Azure Activity Log / GCP ops logs | Enabled as part of cloud integrations |
| **Fluentd / Fluent Bit** | Third-party log shippers → Dynatrace API | Not directly managed by dtwiz |
| **Generic log ingestion API** | REST API for custom log sources | Not directly managed by dtwiz |

#### 7. Metrics ingestion paths

| Path | Protocol | dtwiz relevance |
|---|---|---|
| **OneAgent** | Built-in (process, host, container metrics) | Automatic with OneAgent |
| **OTLP metrics** | OTLP/HTTP to `/api/v2/otlp` | OTel Collector pipelines |
| **Metrics ingest API** | POST to `/api/v2/metrics/ingest` (line protocol) | Not directly managed |
| **Prometheus scraping** | Dynatrace scrapes Prometheus `/metrics` endpoints | Via Operator annotations or ActiveGate |
| **StatsD** | UDP StatsD protocol | Via ActiveGate extension |
| **Cloud metrics** | CloudWatch / Azure Monitor / GCP Operations API | Cloud integrations |

#### 8. Traces / distributed tracing

| Path | Protocol | dtwiz relevance |
|---|---|---|
| **OneAgent** | PurePath (proprietary) + OTel trace enrichment | Automatic with OneAgent |
| **OTLP traces** | OTLP/HTTP to `/api/v2/otlp` | OTel Collector or direct SDK export |
| **Zipkin** | Zipkin v2 JSON | Via OTel Collector zipkin receiver |

#### 9. Extensions Framework 2.0 (EF2)

Dynatrace Extensions 2.0 provide monitoring for technologies without native OneAgent support — databases (Oracle, MSSQL, PostgreSQL), network devices (SNMP), custom metrics, and third-party APIs. Extensions run on the Extension Execution Controller (EEC), which is deployed as part of the Kubernetes Operator or as part of an ActiveGate.

**dtwiz's role:** The Kubernetes installer deploys the EEC automatically. Standalone EEC/ActiveGate deployment for bare-metal monitoring is a future consideration.

### Signal coverage matrix

This table shows which signals each ingestion method provides:

| Method | Metrics | Traces | Logs | Topology | RUM | Security |
|---|---|---|---|---|---|---|
| **OneAgent (full-stack)** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Dynatrace Operator (K8s)** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| **OTel Collector** | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ |
| **AWS CloudFormation** | ✅ | ❌ | ✅ | ✅ | ❌ | ❌ |
| **Azure integration** | ✅ | ❌ | ✅ | ✅ | ❌ | ❌ |
| **GCP integration** | ✅ | ❌ | ✅ | ✅ | ❌ | ❌ |
| **Log ingest API** | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ |
| **Metrics ingest API** | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |

### Zero-config philosophy for dtwiz

The core principle: **if we detect it, we enable monitoring for it — no questions asked.**

| What we detect | What we auto-enable |
|---|---|
| Kubernetes cluster | Dynatrace Operator in `cloudNativeFullStack` mode (full pod injection + infrastructure monitoring + log collection) |
| Docker without K8s | OneAgent container with full-stack monitoring |
| Bare-metal Linux/Windows | OneAgent with app log content access |
| AWS account | CloudFormation stack with all services + log forwarding for all regions |
| Azure subscription | Azure Monitor integration with all services (planned) |
| GCP project | GCP integration with Operations API metrics (planned) |
| Running OTel Collector | Add Dynatrace exporter to all existing pipelines |
| Python/Java/Node runtime | OTel auto-instrumentation with all signals enabled |
| No agent possible (macOS dev) | OTel Collector + runtime auto-instrumentation |

**What "turn all defaults on" means concretely:**
- OneAgent: full-stack mode, not infrastructure-only
- Kubernetes: `cloudNativeFullStack`, not `classicFullStack` or `applicationMonitoring`
- OTel Collector: all three pipelines (traces + metrics + logs), not just one
- AWS: all service feature sets in `QUICK_START` mode, logs enabled for all regions
- Cloud integrations: monitor all detected services, not a hand-picked subset
- Log content access: always enabled (not just metadata)

### Token scopes required per ingestion method

| Method | Token type | Required scopes |
|---|---|---|
| **OneAgent install** | API token (`dt0c01.*`) | `InstallerDownload` |
| **OTel Collector** | API token (`dt0c01.*`) | `openTelemetryTrace.ingest`, `metrics.ingest`, `logs.ingest` |
| **OTel direct SDK** | API token (`dt0c01.*`) | `openTelemetryTrace.ingest`, `metrics.ingest`, `logs.ingest` |
| **AWS CloudFormation** | API token (`dt0c01.*`) | `settings:objects:write`, `extensions:configurations:write`, `extensions:configurations:read` (settings token) + `data-acquisition:logs:ingest`, `data-acquisition:events:ingest` (ingest token) |
| **Kubernetes Operator** | API token (`dt0c01.*`) | Uses `Kubernetes Data Ingest` template scopes |
| **DQL queries** | Any token with Bearer auth | `storage:logs:read`, `storage:metrics:read` |

### Token types and authentication headers

Dynatrace uses several token prefixes, each with different capabilities:

| Prefix | Token type | Typical use |
|---|---|---|
| `dt0c01.*` | API token (classic) | Environment API v1/v2 (`/api/v1`, `/api/v2`) |
| `dt0s16.*` | Settings token | Settings API, extension configuration |
| `dt0e*.` | Environment token | Agent communication |
| OAuth tokens | Platform / PKCE tokens | Platform APIs, DQL, AppEngine |

#### Auth header rules by endpoint family

| Endpoint | Auth header format | Notes |
|---|---|---|
| Classic API (`/api/v1/*`, `/api/v2/*`) | `Api-Token dt0c01.*` | Must use `Api-Token` scheme |
| Platform DQL API (`/platform/storage/query/v1/query:execute`) | `Bearer <token>` | **Always** `Bearer` — regardless of token prefix |
| OTLP ingest (`/api/v2/otlp/*`) | `Api-Token dt0c01.*` | Classic API path, uses `Api-Token` |

#### Critical finding: DQL endpoint auth

The Grail DQL query endpoint at `.apps.` **always requires `Bearer` auth** — even for `dt0c01.*` tokens.  Sending `Api-Token dt0c01.*` (as `AuthHeader()` would produce) returns **403 "Insufficient permission to access the tenant"**.

This was discovered empirically:

1. `Api-Token dt0c01.*` → 403 (wrong auth scheme for platform endpoint)
2. `Bearer dt0s16.*` → 403 ("Insufficient permission to access the tenant" — settings tokens lack Grail scopes)
3. `Bearer dt0c01.*` → **Works** (if the token has `storage:logs:read` scope)

The working Python implementation confirms this — it always uses `Bearer {token}` for DQL, never `Api-Token`:

```python
headers = {
    "Authorization": f"Bearer {token}",  # Always Bearer, even for dt0c01.* tokens
    "Content-Type": "application/json",
}
```

#### dtwiz token resolution for DQL verification

`verifyOtelInstall` resolves the DQL token with fallback:

```
dqlToken = platformToken || apiToken
```

Matching the Python: `token = config.platform_token or config.api_token`

Both are sent as `Bearer <token>` to the DQL endpoint. The `dt0c01.*` API token works as long as it has the `storage:logs:read` scope — no separate platform token is strictly required.

### Dynatrace Hub & ecosystem context

The Dynatrace Hub (`dynatrace.com/hub`) lists 875+ integrations. Key categories relevant to dtwiz:

- **Cloud platforms:** AWS, Azure, GCP — native integrations with CloudWatch, Azure Monitor, GCP Operations API
- **Kubernetes:** All-in-one K8s observability app, EKS/AKS/GKE specific integrations
- **OpenTelemetry:** Native OTLP support, OTel Collector distribution, per-language walkthroughs
- **Databases:** Oracle, PostgreSQL, MySQL, MongoDB, Redis, Cosmos DB, DynamoDB — via Extensions 2.0 or OneAgent
- **Message queues:** Kafka (Confluent Cloud), RabbitMQ, SQS, SNS — via Extensions or OTel
- **AI/ML:** NVIDIA GPU monitoring, Ollama, LangGraph, LlamaIndex, TensorFlow Keras, OpenAI
- **Log shippers:** Fluentd, Fluent Bit, generic log ingest API
- **CI/CD:** GitHub, GitLab, Azure DevOps pipeline observability
- **Prometheus:** Native scraping in Kubernetes via Dynatrace Operator annotations

## Current state

The analyzer detects: platform/OS, container runtime (Docker), Kubernetes (with distribution and context), OneAgent, OTel Collector, AWS, Azure, and running services.

Installers are partially implemented. The recommendation and analysis engine is complete.

---

## Requirements

### R1 — CLI Structure & Commands

| ID | Requirement | Status |
|---|---|---|
| R1.1 | `dtwiz` root command with `--context`, `--environment`, `--access-token`, `--platform-token` persistent flags. | ✅ Done |
| R1.2 | `dtwiz analyze` — run all system detectors and print a summary. Support `--json` for machine-readable output. | ✅ Done |
| R1.3 | `dtwiz recommend` — analyze the system and print ranked ingestion recommendations. Support `--json`. | ✅ Done |
| R1.4 | `dtwiz setup` — interactive guided workflow (analyze → recommend → user picks → install). Support `--dry-run`. | ✅ Done |
| R1.5 | `dtwiz install <method>` — parent command for method-specific installers. Support `--dry-run` on all sub-commands. | ✅ Done |
| R1.6 | `dtwiz install oneagent` — download and run the OneAgent installer on Linux/Windows hosts. | ✅ Done |
| R1.7 | `dtwiz install kubernetes` — deploy the Dynatrace Operator via Helm and apply DynaKube CRs. | ✅ Done |
| R1.8 | `dtwiz install docker` — run OneAgent as a privileged Docker container. | ✅ Done |
| R1.9 | `dtwiz install otel` — download the Dynatrace OTel Collector binary, write config, start the process, and verify log delivery. | ✅ Done |
| R1.10 | `dtwiz install otel-update` — patch an existing OTel Collector YAML config with the Dynatrace OTLP exporter. Support `--config <path>`. | ✅ Done |
| R1.11 | `dtwiz install otel-python` — install OTel Python auto-instrumentation packages and print required env vars. Support `--service-name`. | ✅ Done |
| R1.12 | `dtwiz install aws` — deploy the Dynatrace AWS Data Acquisition CloudFormation stack with interactive prompts for tokens and parameters. | ✅ Done |
| R1.13 | `dtwiz uninstall kubernetes` — remove DynaKube CRs, wait for managed pods, Helm uninstall, delete namespace. | ✅ Done |
| R1.14 | `dtwiz status` — verify Dynatrace connectivity (logged-in user, environment URL) and print system analysis. | ✅ Done |
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
| R3.9 | Formatted output uses colored badges: `✓` for done, numbered for actionable, `!` for not-supported. Each actionable recommendation shows the `dtwiz install <method>` command. | ✅ Done |
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
