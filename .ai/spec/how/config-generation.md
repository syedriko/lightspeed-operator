# Config Generation

## Module Map

| File | Key Functions | Responsibility |
|---|---|---|
| `internal/controller/appserver/assets.go` | `GenerateOLSConfigMap()`, `buildProviderConfigs()`, `buildOLSConfig()`, `generateMCPServerConfigs()`, `buildToolFilteringConfig()` | OLS config YAML (olsconfig.yaml) |
| `internal/controller/postgres/assets.go` | `GeneratePostgresConfigMap()`, `GeneratePostgresBootstrapSecret()`, `GeneratePostgresSecret()` | PostgreSQL config + bootstrap script + credentials |
| `internal/controller/console/assets.go` | `GenerateConsoleUIConfigMap()` | Nginx config for console plugin |
| `internal/controller/ocpmcp/assets.go` | `GenerateConfigMap()` | Standalone MCP runtime config (TOML): TLS, toolsets, denied resources, metrics |

## Data Flow

Tool-result inspection generation conforms to `openshift/ols/.ai/spec/what/tool-result-inspection.md`. This document defines only the operator mappings.

### OLS Config (olsconfig.yaml)
```
CR spec -> GenerateOLSConfigMap() -> ConfigMap "olsconfig"
```

Generated YAML structure (marshaled from `utils.AppSrvConfigFile`):
```yaml
llm_providers:
  - name: <provider.Name>
    type: <provider.Type>  # direct from CRD enum: openai, azure_openai, etc.
    url: <provider.URL>                     # non-Azure providers
    credentials_path: /etc/apikeys/<secretName>  # mount path to secret dir
    models:
      - name: <model.Name>
        url: <model.URL>
        context_window_size: <model.ContextWindowSize>
        parameters:
          max_tokens_for_response: <model.Parameters.MaxTokensForResponse>
          tool_budget_ratio: <default 0.25 if zero>
          temperature: <model.Parameters.Temperature>  # omitted when unset; explicit 0 preserved
          reasoning_config: <model.Parameters.ReasoningConfig>  # [PLANNED: OLS-3442] omitted when nil
    # Azure-specific:
    azure_openai_config:
      url: <provider.URL>
      credentials_path: /etc/apikeys/<secretName>
      azure_deployment_name: <deploymentName>
    api_version: <apiVersion>
    # Watsonx-specific:
    project_id: <projectID>
    # Fake provider:
    fake_provider_config:
      url: "http://example.com"
      response: "This is a preconfigured fake response."
      chunks: 30
      sleep: 0.1
      stream: false
      mcp_tool_call: <fakeProviderMCPToolCall>

ols_config:
  default_model: <spec.ols.defaultModel>
  default_provider: <spec.ols.defaultProvider>
  max_iterations: <spec.ols.maxIterations>
  logging:
    app_log_level: <spec.ols.logLevel>
    lib_log_level: <spec.ols.logLevel>
    uvicorn_log_level: <spec.ols.logLevel>
  conversation_cache:
    type: postgres
    postgres:
      host: lightspeed-postgres-server.<namespace>.svc
      port: 5432
      user: postgres
      db: postgres
      password_path: /etc/credentials/lightspeed-postgres-secret/password
      ssl_mode: require
      ca_cert_path: /etc/certs/postgres-ca/service-ca.crt
  tls_config:
    tls_certificate_path: /etc/certs/lightspeed-tls/tls.crt
    tls_key_path: /etc/certs/lightspeed-tls/tls.key
  reference_content:
    indexes:                                  # one per spec.ols.rag entry; empty when no BYOK RAG
      - path: /app-root/rag/rag-0
        index_id: <rag.IndexID>
        origin: <rag.Image>
    embeddings_model_path: /app-root/embeddings_model

  solr_hybrid:                                # unless byokRAGOnly
    solr_http_base: "https://lightspeed-rhokp.<ns>.svc:8443"
    max_results: 10
    hybrid_vector_boost: 8.0
    hybrid_pool_docs: 100
    hybrid_score_threshold: 0.0
    hybrid_solr_timeout_s: 60
  user_data_collection:
    feedback_disabled: <computed: CRvalue || !dataCollectorEnabled>
    feedback_storage: /app-root/ols-user-data/feedback
    transcripts_disabled: <computed: CRvalue || !dataCollectorEnabled>
    transcripts_storage: /app-root/ols-user-data/transcripts
  extra_cas: [<list of cert file paths from kube-root-ca.crt + additional CA CM>]
  certificate_directory: /etc/certs/cert-bundle
  proxy_config:
    proxy_url: <proxyConfig.proxyURL>
    proxy_ca_cert_path: /etc/certs/cm-proxycacert/<certKey>
  query_filters: [{name, pattern, replace_with}]   # if spec.ols.queryFilters set
  system_prompt_path: /etc/ols/system_prompt        # if spec.ols.querySystemPrompt set
  quota_handlers_config:                             # if spec.ols.quotaHandlersConfig set
    storage: <postgres cache config>
    scheduler: {period: 300}
    limiters_config: [{name, type, initial_quota, quota_increase, period}]
    enable_token_history: <bool>
  tool_filtering:                                    # if ToolFiltering gate + MCP servers exist
    alpha: <default 0.8>
    top_k: <default 10>
    threshold: <default 0.01>
  credential_hot_reload: <credentialHotReload>         # OLS-3450; true when CR flag enabled
  guardrails:                                        # [PLANNED: OLS-3928]
    tool_result_inspection:
      enabled: <spec.ols.guardrails.toolResultInspection.enabled, default true>
  tools_approval:                                    # always present
    approval_type: <default "tool_annotations">
    approval_timeout: <default 600>

mcp_servers:                                         # if any MCP servers configured
  - name: openshift                                  # if introspectionEnabled
    url: https://openshift-mcp-server.<ns>.svc:8443/mcp
    timeout: <mcpKubeServerConfig.timeout or default 60>
    headers:
      Authorization: kubernetes                     # service resolves caller credentials
  - name: <user server>                              # if MCPServer feature gate
    url: <url>
    timeout: <timeout>
    headers:
      <name>: <resolved value>                       # kubernetes -> "kubernetes"
                                                     # client -> "client"
                                                     # secret -> /etc/mcp/headers/<secretName>/header

user_data_collector_config:                          # if dataCollectorEnabled
  data_storage: /app-root/ols-user-data
  log_level: <spec.olsDataCollector.logLevel>
```

### PostgreSQL Bootstrap Script
Content is in `utils.PostgresBootStrapScriptContent` constant. Deployed as a Secret (not ConfigMap) named `lightspeed-postgres-bootstrap`.

```bash
#!/bin/bash
cat /var/lib/pgsql/data/userdata/postgresql.conf

_psql () { psql --set ON_ERROR_STOP=1 "$@" ; }

# Create pg_trgm extension in default database (for OLS conversation cache)
echo "CREATE EXTENSION IF NOT EXISTS pg_trgm;" | _psql -d $POSTGRESQL_DATABASE

# Create schemas for isolating different components' data
echo "CREATE SCHEMA IF NOT EXISTS quota;" | _psql -d $POSTGRESQL_DATABASE
echo "CREATE SCHEMA IF NOT EXISTS conversation_cache;" | _psql -d $POSTGRESQL_DATABASE
```

The `templogs` schema is not created here; the OTEL Collector always creates it via `postgres_admin` at collector startup. See `what/templog.md`.

### PostgreSQL Config (postgresql.conf.sample)
Content is in `utils.PostgresConfigMapContent` constant. Deployed as ConfigMap.
```
huge_pages = off
ssl = on
ssl_cert_file = '/etc/certs/tls.crt'
ssl_key_file = '/etc/certs/tls.key'
ssl_ca_file = '/etc/certs/cm-olspostgresca/service-ca.crt'
```

### PostgreSQL Password Secret
Generated via `GeneratePostgresSecret()`: 12 random bytes, base64 encoded, stored in secret key `password` (`utils.PostgresSecretKeyName`).

### Nginx Config (Console UI)
Inline in `GenerateConsoleUIConfigMap()`:
- PID file: `/tmp/nginx/nginx.pid`
- Temp paths: `/tmp/nginx/{client_body,proxy,fastcgi,uwsgi,scgi}` (for read-only root filesystem)
- Serves static files from `/usr/share/nginx/html` on port 9443 with SSL
- TLS cert/key from `/var/cert/tls.crt` and `/var/cert/tls.key`

### MCP Server Config (TOML)
`ocpmcp.GenerateConfigMap()` writes typed `GenerateConfigTOML()` output to `openshift-mcp-server-config`, key `config.toml`. The standalone Deployment mounts it read-only with `subPath` at `/etc/mcp-server/config.toml` and starts `/openshift-mcp-server --config /etc/mcp-server/config.toml`.
```toml
port = "8443"
tls_cert = "/etc/tls/tls.crt"
tls_key = "/etc/tls/tls.key"
read_only = false
disable_destructive = true
toolsets = ["core", "config", "helm", "observability/metrics", "kubevirt"]
experimental_enable_target_compatibility_tool_filters = true

[[denied_resources]]
group = ""
version = "v1"
kind = "Secret"

[[denied_resources]]
group = "rbac.authorization.k8s.io"
version = "v1"

[toolset_configs."observability/metrics"]
prometheus_url = "https://thanos-querier.openshift-monitoring.svc.cluster.local:9091"
alertmanager_url = "https://alertmanager-main.openshift-monitoring.svc.cluster.local:9094"
guardrails = "!tsdb"
```

The RBAC entry omits `kind`, denying the entire v1 API group. `read_only = false` removes the read-only annotation filter without granting caller permissions; explicit destructive filtering retains the image's prior effective policy and hides destructive-annotated writes such as `resources_create_or_update`; `!tsdb` controls PromQL query safety, not authorization. The example is the omitted-selection baseline; typed `spec.ols.mcpKubeServerConfig.toolsets` replaces selection and generates only selected supported tables as specified below. `spec.ols.mcpKubeServerConfig.timeout` affects only the app-server client. ConfigMap ResourceVersion changes roll the standalone MCP Deployment, not the app-server; see [deployment-generation.md](deployment-generation.md) and [ocpmcp.md](../what/ocpmcp.md).

Header values `kubernetes` and `client` are service-interpreted placeholders, not credentials or token-template strings. The built-in endpoint uses `Authorization: kubernetes`.

## OCP MCP Toolset Inventory — OLS-2715 Design Input

This source-verified inventory describes the shipped server, not arbitrary TOML/global settings exposed by the CR API. It covers all compiled toolsets; implemented operator support excludes `openshift/mustgather` and its offline archive workflow; see [ocpmcp.md](../what/ocpmcp.md#toolset-selection). Inventory fields are upstream TOML names, distinct from the implemented typed singleton-key CR API in [crd-api.md](../what/crd-api.md#typed-toolset-selection). The exact pinned image has executed locally with actual operator serializer output, and a separate full production `bin/manager` run exercised live cluster CA/lifecycle and bounded caller-RBAC behavior; [runtime verification](../what/ocpmcp.md#runtime-verification) records scoped passes and remaining [PLANNED: OLS-2715] integration proofs. Passing operator/envtest tests alone is not runtime evidence.

### Version and Evidence

| Item | Verified baseline |
|---|---|
| Image | `registry.redhat.io/openshift-mcp/openshift-mcp-server-rhel9` |
| Manifest-list digest | `sha256:a551fd58f7b2a7505a76ba2109ae5a8f031606605490c9c68d60f91f502f6fe5` |
| Version label | `1.0.0` |
| OpenShift source revision | `495310209578a754f3383016486fa8f611de7452` |
| Observability implementation | Vendored `github.com/rhobs/obs-mcp v0.7.1` |
| Evidence | [Red Hat Catalog metadata for this digest](https://catalog.redhat.com/api/containers/v1/images?filter=repositories.manifest_list_digest==sha256:a551fd58f7b2a7505a76ba2109ae5a8f031606605490c9c68d60f91f502f6fe5), production imports, registrations, parsers, validators, and runtime code |

Catalog labels for all four architectures map to this revision. Local x86_64 image execution confirmed digest and labels (release `1789004305`), not the entire inventory's functionality. CLI `--version` prints a blank line and MCP `serverInfo.version` is empty; label version `1.0.0` is provenance only, not independent source/binary attestation. All source links below are pinned to that revision. Recheck the catalog, fields, defaults, filtering, authentication, trust, and dependencies when changing the image; a startup image override does not establish compatibility with this inventory.

### Catalog and Configuration Registration

The server has **18 compiled selectable toolsets and nine registered configuration tables**. OLS-2715 exposes 17 typed selections, excluding `openshift/mustgather`; catalog presence alone is not operator support. Eight tables have user-facing fields; `observability/otelcol` uses embedded schemas without a supported user-facing setting. “None” means no registered per-toolset config parser, not no global settings or runtime prerequisites.

| Selection name | Config key under `toolset_configs` | Purpose / external prerequisites |
|---|---|---|
| `core` | None | Kubernetes resources, pods, events, namespaces, nodes; operation-specific APIs and caller permissions |
| `config` | None | Kubernetes contexts/kubeconfig; `configuration_view` is disabled by the image default |
| `helm` | `helm` | Helm charts/releases; chart sources, release storage and Kubernetes permissions |
| `kcp` | None | kcp workspaces; `tenancy.kcp.io/v1alpha1` API. Selection does not select the kcp cluster provider |
| `ossm` | **`kiali`** | Kiali/service-mesh APIs; configured endpoint, mesh and backend integrations |
| `kubevirt` | None | Virtualization APIs/workloads; CDI, cloning, guest-agent and storage prerequisites depend on operation |
| `netobserv` | `netobserv` | NetObserv console-plugin API and its backing flows/metrics services |
| `tekton` | None | `tekton.dev/v1` APIs, pipeline/task definitions and execution prerequisites |
| `cluster-diagnostics` | None | Node debug execution; temporary privileged host-access pods and caller RBAC/SCC permissions |
| `cni-diagnostics` | `cni-diagnostics` | Kernel/network diagnostics; debug images, privileged node pods or exec into existing pods |
| `openshift/mustgather` | `openshift/mustgather` | **Excluded from OLS-2715 operator support.** Offline archive analysis needs extracted archive mounts; separate bundled planning prompt uses live-cluster access |
| `netedge` | None | DNS/router/network probes and monitoring queries; discovers Thanos Route and existing router workloads at invocation |
| `oadp` | None | **Prompt-only** OADP/Velero troubleshooting; no tools registered in this pin |
| `ovn-kubernetes` | None | Exec into existing OVN/OVS pods; expected containers, binaries and DB/socket access |
| `observability/metrics` | `observability/metrics` | Prometheus queries/discovery and Alertmanager reads |
| `observability/logs` | `observability/logs` | Loki queries and LokiStack endpoint discovery |
| `observability/traces` | `observability/traces` | Tempo queries and TempoStack/TempoMonolithic discovery |
| `observability/otelcol` | `observability/otelcol` | Local embedded Collector component schemas, not a Collector endpoint client |

Sources: [base imports](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/mcp/modules.go), [OpenShift imports](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/mcp/openshift_modules.go), [observability registrations](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/vendor/github.com/rhobs/obs-mcp/pkg/toolset/toolsets.go), [OSSM name override](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/toolsets/kiali/internal/defaults/defaults_override.go).

Configuration tables do not enable toolsets. Missing tables do not invoke their extension validators; supplied tables are parsed and validated even for unselected toolsets. Unknown global keys, table names, fields, and wrong field types fail loading. A table for a toolset with no parser fails even if empty. `kiali` is not a selection alias, and `ossm` is not a config-key alias. Slash-containing table names must be quoted, e.g. `[toolset_configs."observability/logs"]`. See [extension parsing](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/config/extended.go) and [toolset registry](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/toolsets/toolsets.go).

There is no supported catalog/config-schema discovery endpoint or export command. MCP `tools/list` exposes selected, filtered tools and invocation schemas, not disabled toolsets or server configuration requirements.

### Helm Configuration

Entire table optional. Source: [Helm config](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/helm/config.go), [runtime client](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/helm/helm.go).

| Field | Type | Effective default | Validation / behavior |
|---|---|---|---|
| `allowed_registries` | string array | Empty: no allowlist | Entries require scheme/host; only `oci://` or `https://`. Normalized scheme/host and path-prefix matching |
| `storage_driver` | string | `secret` | Empty uses default; nonempty lowercased and restricted to `secret` or `configmap` |

Chart URL references permit OCI/HTTPS; without an allowlist local/repository references are also usable. Registry credentials/CA/client certificates have no toolset fields; Helm uses its own credential/config facilities, not Kiali/NetObserv trust settings. Default Secret-backed release storage conflicts with OLS Secret denial; `configmap` is available. Private registries, local charts and credential helpers can require additional mounts/executables. Embedded Helm libraries do not require a separate Helm binary.

### OSSM / Kiali Configuration

Source: [Kiali config](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/kiali/config.go), [runtime client](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/kiali/kiali.go).

| Field | Type | Default | Validation / behavior |
|---|---|---|---|
| `url` | string | Empty; no discovery/default | Required when table is supplied; nonempty scheme/host. Ordinary validation does not restrict scheme to HTTP/HTTPS |
| `insecure` | boolean | `false` | Skips TLS verification; rejected with global `require_tls=true` |
| `certificate_authority` | string path | Empty | HTTPS requires a CA path unless insecure; supplied paths must exist. Relative paths resolve against config directory |

Missing table is not rejected at startup: enabled OSSM then fails API calls with an uninitialized client. An empty table fails validation. With `require_tls`, HTTPS is enforced. CA existence checking does not prove valid/readable PEM; runtime CA loading failures are logged and custom CA is ignored. Uses system roots plus custom CA and shared TLS minimum/ciphers. Requires a mounted CA file for verified HTTPS. Authentication copies derived Kubernetes `BearerToken`, not `BearerTokenFile`, client certificates or exec auth. No independent credential fields. One configured endpoint is shared; tool `meshCluster` is an invocation argument, not server endpoint discovery.

### NetObserv Configuration

Entire table optional. Source: [NetObserv config](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/netobserv/config.go), [defaults](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/netobserv/defaults.go), [runtime client](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/netobserv/netobserv.go).

| Field | Type | Effective default | Validation / behavior |
|---|---|---|---|
| `url` | string | Synthesized from fields below | Explicit nonblank URL overrides namespace/service/port; trimmed, requires scheme/host |
| `namespace` | string | `netobserv` | No DNS-name validation |
| `service` | string | `netobserv-plugin` | No DNS-name validation |
| `port` | integer | `9001` when zero | No explicit range validation |
| `insecure` | boolean | `false` | Skips TLS verification; rejected with global `require_tls=true` |
| `certificate_authority` | string path | Conditional service-CA fallback | Explicit HTTPS requires CA unless insecure; supplied path must exist, relative to config directory |

Runtime OpenShift detection checks Project API availability, not NetObserv installation. Synthesized OCP URL: `https://netobserv-plugin.netobserv.svc.cluster.local:9001`; other clusters use HTTP. For synthesized OCP URLs only, missing explicit CA/insecure tries `/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt`. The file must be projected into MCP; missing CA does not automatically enable insecure. Explicit HTTPS does not use that automatic fallback. Custom CA replaces system roots; runtime invalid/unreadable explicit CA fails the request. BearerToken is used, falling back to BearerTokenFile; file-read failure logs and proceeds unauthenticated. No separate credentials. HTTP timeout is 120 seconds and redirects are prohibited.

Config-time validation assumes non-OpenShift: a supplied table without URL passes normally but fails with `require_tls=true` due to synthesized HTTP. Omitted table bypasses that validation; runtime TLS enforcement still applies. Shared TLS minimum/ciphers apply. Endpoint synthesis is not service/FlowCollector discovery or readiness validation.

### CNI Diagnostics Configuration

Entire table optional. Source: [CNI config](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/toolsets/cni-diagnostics/config/config.go), [runtime helpers](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/toolsets/cni-diagnostics/utils/utils.go).

| Field | Type | Effective default | Validation / behavior |
|---|---|---|---|
| `kernel_debug_image` | string | `nicolaka/netshoot:v0.16` | Empty/omitted uses default; no image-reference validation |
| `tcpdump_image` | string | `nicolaka/netshoot:v0.16` | Empty/omitted uses default; no image-reference validation |
| `pwru_image` | string | `docker.io/cilium/pwru:v1.0.10` | Empty/omitted uses default; no image-reference validation |

Node diagnostics create privileged host-access debug pods; caller RBAC/SCC and image availability matter. pwru needs kernel/eBPF/debugfs support. Pod-target tcpdump execs into an existing container: setting `tcpdump_image` does not install a binary there. No independent auth/TLS settings. These runtime workload images need disconnected/mirroring consideration independently of the MCP image. `cluster-diagnostics` has no corresponding config fields; its debug image can be supplied as a tool argument, with default `registry.access.redhat.com/ubi9/toolbox:latest`.

### Must-gather Configuration — Inventory Only, Excluded from OLS-2715

This source contract is retained to explain the deliberate exclusion. Do not generate its configuration, archive mounts or a PVC/reference API under OLS-2715. Offline analysis is a separate workflow from the live-cluster Lightspeed integration.

Source: [must-gather config](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/toolsets/mustgather/config.go), [archive listing](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/toolsets/mustgather/list.go).

| Field | Type | Effective default | Validation / behavior |
|---|---|---|---|
| `mustgather_dirs` | string array | Empty; no fallback | Operationally required for archive tools; no directory existence/readability validation |
| `tail_limit` | integer | `1000` lines | Positive overrides; zero/negative falls back; no upper-bound validation |
| `max_output_size` | integer | `1048576` bytes | Positive overrides; zero/negative falls back; no upper-bound validation |

Missing/empty directories yield tool errors, not startup rejection. Nonexistent/unreadable roots are skipped; configured roots with no recognized archives return an empty listing. Mount extracted local archives into MCP; compressed archives are not automatically unpacked. Scans configured roots and immediate children, not arbitrary recursion. Archive tools do not need live-cluster data, but the host still derives a Kubernetes client for every tool call. `plan_mustgather` is separately a live-cluster planning prompt. Any future support would need archive volume sourcing, multi-replica accessibility and a separate archive-data access/lifecycle design; Kubernetes denied-resource rules are not an archive-content filter.

### NetEdge Integration Without Configuration Fields

Sources: [NetEdge monitoring queries](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/toolsets/netedge/query_prometheus.go), [monitoring transport](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/prometheus/options.go), [DNS debug pod](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/toolsets/netedge/exec_dns_in_pod.go).

- Monitoring discovers Route `openshift-monitoring/thanos-querier` and uses a separate `pkg/prometheus` client, **not** `observability/metrics` configuration or the vendored observability transport. It copies REST BearerToken with BearerTokenFile fallback; trust uses REST CAData, then CAFile, then system roots. If no usable CA/system pool is available it falls back to insecure TLS with a warning; REST `Insecure=true` explicitly skips verification.
- Router operations exec into existing ingress workloads. Local DNS/HTTP probes originate from MCP's network environment; HTTP probes use ordinary system trust, not the monitoring transport.
- `exec_dns_in_pod` creates/deletes temporary pods using hardcoded `registry.redhat.io/openshift4/network-tools-rhel9`; there is no config field or tool argument to override that image. Requires image availability/pull authorization and caller pod create/get/delete/log permissions. Disconnected image handling needs separate consideration.

### Observability Configuration

All three backend clients accept `auth_mode` values `""`, `"header"`, `"kubeconfig"` (case-sensitive); empty means header. All default `insecure=false`. No per-toolset token, username/password, client-cert/key or CA-file fields exist.

#### Metrics

Source: [metrics config](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/vendor/github.com/rhobs/obs-mcp/pkg/metrics/config.go).

| Field | Type | Effective default | Validation / behavior |
|---|---|---|---|
| `auth_mode` | string | `header` | Values above |
| `prometheus_url` | string | `http://localhost:9090` | No presence/scheme validation; no automatic OCP monitoring discovery |
| `alertmanager_url` | string | Empty | Required at invocation for alert/silence tools, not by startup validator |
| `insecure` | boolean | `false` | Backend TLS verification switch |
| `guardrails` | string | `all` | Grammar below |
| `max_metric_cardinality` | optional unsigned 64-bit integer | `20000` | Explicit value requires enabled `max-metric-cardinality`; zero rejected |
| `max_label_cardinality` | optional unsigned 64-bit integer | `500` | Explicit value requires enabled `disallow-blanket-regex`; zero means always reject blanket regex |
| `range_query_full_response` | boolean | `false` | Full range-query datapoints instead of summaries |

Guardrail input is lowercased/trimmed. Empty/`all` enables all four rules; `none` disables configurable rules. Positive comma-separated names enable only listed rules; negative `!` names disable listed rules from all; mixing signs fails. Names: `disallow-explicit-name-label`, `require-label-matcher`, `disallow-blanket-regex`, `max-metric-cardinality`. `!tsdb` disables the last two TSDB-dependent rules; bare `tsdb` is invalid. `all`/`none` are whole-value shortcuts, not list elements. Empty list tokens are ignored. Metric-existence validation remains even with `none`. Default cardinality rules can require `/api/v1/status/tsdb`; OLS already supplies `!tsdb` for Thanos. See [guardrail implementation](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/vendor/github.com/rhobs/obs-mcp/pkg/metrics/prometheus/guardrails.go).

#### Logs

Source: [logs config](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/vendor/github.com/rhobs/obs-mcp/pkg/logs/config.go), [endpoint discovery](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/vendor/github.com/rhobs/obs-mcp/pkg/logs/discovery/discovery.go).

| Field | Type | Default | Validation / behavior |
|---|---|---|---|
| `auth_mode` | string | `header` | Values above |
| `loki_url` | string | Empty | Static endpoint overrides discovery; no URL validation |
| `insecure` | boolean | `false` | Backend TLS verification switch |
| `use_route` | boolean | `false` | Discovery chooses Routes instead of service DNS |

Without static URL, invocation requires `lokiNamespace` and `lokiName` together; discovery lists `loki.grafana.com/v1` LokiStacks. OCP tenant modes use HTTPS gateway service DNS and `/api/logs/v1`; other modes use HTTP. Route discovery checks known names then matching gateway Service. `tenant` is a tool argument, not a config field, and supplies `X-Scope-OrgID` and the OCP path segment. Requires appropriate Kubernetes discovery/Route permissions and backend authorization. Missing endpoint/selectors fail at invocation. Internal `ClientMetrics` has `toml:"-"` and is not configurable.

#### Traces

Source: [traces config](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/vendor/github.com/rhobs/obs-mcp/pkg/traces/config.go), [resolution](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/vendor/github.com/rhobs/obs-mcp/pkg/traces/common.go), [discovery](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/vendor/github.com/rhobs/obs-mcp/pkg/traces/discovery/discovery.go).

| Field | Type | Default | Validation / behavior |
|---|---|---|---|
| `auth_mode` | string | `header` | Values above |
| `tempo_url` | string | Empty | Static API base overrides discovery; no URL validation |
| `insecure` | boolean | `false` | Backend TLS verification switch |
| `use_route` | boolean | `false` | Discovery chooses Routes instead of service DNS |

Without static URL, invocation requires `tempoNamespace` and `tempoName`. Discovery lists both `tempo.grafana.com/v1alpha1` TempoStacks and TempoMonolithics; failure listing either aborts discovery. Multitenant endpoints use HTTPS gateway; single-tenant endpoints use HTTP. Routes use the computed service name. Discovered multitenant instances require a declared tenant tool argument and add the tenant API path. A static URL bypasses that validation and must already include the correct API base; the tenant argument does not complete it. Internal `ClientMetrics` is excluded from TOML.

#### Collector Schemas

Source: [otelcol parser and registration](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/vendor/github.com/rhobs/obs-mcp/pkg/otelcol/toolset.go).

No supported user-facing settings. Omitted/empty config uses embedded Red Hat Collector component schemas (`0.144.0`, `0.152.0` at this pin). Internal `SchemaFS` is an exported, untagged Go interface initialized by the parser; it is not a supported TOML path/URL or a CR configuration field. `version`, component type and YAML/JSON content are tool arguments. Operations validate individual components, not a running Collector or complete pipeline.

#### Shared Observability Transport

Source: [backend auth/TLS](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/vendor/github.com/rhobs/obs-mcp/pkg/auth/auth.go), [token sourcing](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/vendor/github.com/rhobs/obs-mcp/pkg/auth/token.go).

- Requires derived Kubernetes REST config even for static endpoints. Header mode reads original handler-context credentials; kubeconfig mode reads derived BearerToken/BearerTokenFile. Actual metrics/logs/traces calls in both modes use caller token when a bearer header exists: pinned [Manager.Derived](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/kubernetes/manager.go) replaces REST BearerToken. Without that header, header mode sends an anonymous request (no local rejection or REST fallback), while kubeconfig mode uses the supplied synthetic kubeconfig token. These upstream wrinkles do not grant operator identity or prove real/global authentication: permissive mocks accept anything, with no TokenReview/SubjectAccessReview. Do not assume provider-local token exchange or static identity isolation; see [observations](../what/ocpmcp.md#authentication-observations).
- Plain HTTP returns the transport **without attaching bearer auth**. HTTPS uses minimum TLS 1.2; insecure skips verification.
- Verified HTTPS starts with system roots, appends REST `CAData`, and only if that is not successfully loaded tries `/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt`. Does not directly read REST `CAFile` or inherit REST `Insecure`.
- Missing/invalid fallback CA warns and can produce handshake failure. OLS provisions validated shared MCP roots through the snapshot/system-directory mechanism below, independent of this conditional fallback; app-server CA mounts do not provision MCP backend trust.
- These vendor configs/transports do not implement global `require_tls` enforcement or inherit global TLS minimum/cipher settings. Vendored error messages naming flags/environment variables are not proof that the OCP binary binds them.

### Global Settings, Filters and Operator Design Implications

Source: [OCP defaults](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/config/config_default_overrides.go), [tool filtering](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/mcp/mcp.go), [global validation](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/config/validate.go).

1. Distinguish image defaults from the OLS TOML above. Image defaults include `toolsets=["core","config"]`, `read_only=true`, `disable_destructive=true`, `disabled_tools=["configuration_view"]`, and Secret/ServiceAccount/RBAC denial. OLS overrides selection, read-only, denial list, serving TLS and metrics settings, and enables compatibility filters. It now explicitly pins `disable_destructive=true` without changing the prior effective policy, and does not override `disabled_tools`.
2. Selection does not guarantee exposure: read-only/destructive annotations, allow/deny tool lists and compatibility predicates filter tools. Inherited `disable_destructive=true` hides core resource create/update/delete/scale and pod exec/delete, several VM mutations and Tekton PipelineRun lifecycle. `cluster-diagnostics` contains only destructive `nodes_debug_exec` and no prompts, so its selection currently exposes nothing. The agreed OLS-2715 policy accepts its selection anyway and preserves destructive filtering; zero exposed tools is a documented runtime limitation, not an admission blocker. CNI's six destructive=false tools bypass that filter and invoke debug/exec helpers directly; annotations do not establish nonprivileged execution. In normal single-target OLS, `config` also exposes no tools because target-list tools are filtered and `configuration_view` is disabled. OADP has only a prompt, whose availability to OLS consumers must be verified separately.
3. With OLS compatibility filters enabled, logs require LokiStack discovery; traces require TempoStack even with a static URL or only TempoMonolithic; otelcol requires OpenTelemetryCollector even though schemas are local. Metrics has no equivalent predicate. API presence is neither authorization nor service health. Preserve awareness of these limitations when defining support.
4. Kubernetes auth/provider configuration is global, not per-toolset. Passthrough uses caller bearer credentials when present and otherwise configured credentials. The kcp provider needs a valid workspace kubeconfig URL; `cluster_provider_strategy`, `workspace_poll_interval` (60s), and `workspace_debounce_window` (5s) are not fields under `toolset_configs.kcp`.
5. Global `certificate_authority` serves OAuth/token exchange, not universal backend trust. Inbound `tls_cert`/`tls_key` serve MCP HTTPS, not outbound toolset TLS. Different toolsets have different trust/auth implementations.
6. Enabling all supported choices must not automatically enable every choice, grant RBAC/SCC, install operators/backends, or relax resource protections. Runtime image pulls, privileged debug workloads, credential mounts and discovery-filter limitations are additional integration work, not TOML-only configuration. Archive mounts are outside OLS-2715 because must-gather is excluded.
7. Upstream startup validation is incomplete for operational requiredness. OLS implements independent structural schema/CEL validation for typed requiredness, endpoints, image syntax, references and defaults; source existence and certificate PEM are validated during reconciliation. Unknown typed fields and mixed recognized/unknown selection keys require `fieldValidation=Strict` because CEL cannot inspect pruned fields. Empty-only maps use MaxProperties=0 and reject populated blocks even without Strict.
8. Tool input schemas, embedded Collector schemas, cluster-provider configs and global server/OAuth/telemetry settings are not additional toolset configuration fields. Selection uses the agreed typed singleton-key array below; supported-toolset configuration schemas/defaults are specified. Preserve `read_only=false`, `disable_destructive=true` and existing safeguards; typed validation and shared-trust generation are implemented, with scoped actual shipped-image passes in [runtime verification](../what/ocpmcp.md#runtime-verification) and remaining [PLANNED: OLS-2715] integration work, not a new security control. No archive-volume API is planned for this change.

### Typed Selection Generation

1. Preserve field presence independently of array length. Absent `spec.ols.mcpKubeServerConfig.toolsets` uses existing operator defaults; a present array is the complete selection. Present empty emits explicit TOML `toolsets = []`, never omission (which would invoke image defaults).
2. For a nonempty array, map each singleton CR key to the exact server selection name. Emit only selected toolsets; do not union with defaults or add dependencies. Configuration-free entries such as `core: {}` produce selection without an unregistered `toolset_configs.core` table.
3. Typed configurable entries generate the corresponding registered table when needed, including CR `ossm` to TOML `toolset_configs.kiali`. Do not carry default-toolset configuration into unselected tables. Per-toolset defaults and field/reference mappings are specified in the blocks below.
4. Structural schema/CEL reject duplicate/singleton/configuration violations and unknown-only selections (including excluded `openshift/mustgather`). Strict requests are required for unknown fields in typed blocks or mixed recognized/unknown keys; non-Strict pruning is not rejection. `*[]MCPToolsetSelection` retains explicit emptiness across serialization/deepcopy. The list is bounded at 17; field bounds and empty-map validation are in the API spec.
5. Keep serving TLS, resource restrictions and lifecycle wiring independent of the selection. Desired TOML changes use the existing ConfigMap reconciliation and MCP Deployment rollout path. Selecting no toolsets must not call operand removal or disable the app-server client/handoff endpoint.
6. Operator tests verify omission, empty/nonempty replacement and transitions against generated TOML; full `make test` passes with default Kubernetes 1.27.1 envtest. Actual `GenerateConfigTOML` inputs `null`, explicit empty and all seven typed blocks passed pinned-image startup/initialize/tools/list with 22, 0 and 39 tools respectively. Counts depend on mock discovery (including LokiStack/TempoStack for 39); not universal counts or all-tool functionality. Upstream TOML omission gave 11 core-only tools in the first harness, not operator default parity. Explicit empty selects no toolsets, not a promise about unrelated protocol/server facilities.

### Toolset CA Reference Generation

- For selected OSSM/NetObserv entries with `caBundleRef`, resolve exactly one named ConfigMap or Secret key in `r.GetNamespace()`. Validate the selected certificate bundle before generating/applying the desired MCP resources; surface source/key/bundle errors instead of relying on upstream existence checks.
- `trust.go` strictly validates nonempty certificate-only X.509 PEM, reconstructs canonical public PEM and copies only selected certificates into operator-owned `openshift-mcp-server-trust`. No credentials, private keys, arbitrary bytes or unselected keys are copied. A same-name ConfigMap not controlled by this OLSConfig is rejected, not adopted/overwritten; deletion also checks ownership.
- MCP mounts selected snapshot keys read-only (0444), never live user CA objects. Toolset directories project only `ossm.crt` or `netobserv.crt` as `ca.crt`: `/etc/mcp-server/toolset-ca/ossm/ca.crt` and `/etc/mcp-server/toolset-ca/netobserv/ca.crt`. TOML maps these paths to `certificate_authority` in `kiali` and `netobserv` respectively.
- Creation, selected-key updates, deletion and recovery enqueue reconciliation before MCP can roll. Shared sources union all active consumers, retaining app-server/PostgreSQL or other applicable targets rather than replacing them with MCP-only mappings. Disabled MCP-only references ignore stale annotations. Source tag `mcp-ca` is distinct from `mcp-header-*`; app-server header mount generation excludes MCP CA references.
- Snapshot and pod template carry `ols.openshift.io/mcp-trust-hash`, SHA-256 over canonical certificate data plus declared reference identities. Valid content changes and name/key-only changes roll MCP even when certificate bytes match. Phase 2 revalidates sources and requires persisted Phase 1 snapshot data/hash to match before deployment generation. Invalid source changes leave the last validated snapshot/runtime configuration intact and do not roll MCP; unverified live source data never reaches mounted files.
- Removal/deselection drops obsolete projections/settings/tracking, not user sources or shared baseline. Cleanup never deletes a currently referenced user CA, even on reserved-name collisions. Runtime/trust/legacy ConfigMap cleanup additionally requires OLSConfig ownership even after ref removal; unowned outputs are preserved. Serving TLS cleanup requires OLSConfig ownership or the originating OLSConfig-owned Service's matching name/UID and runs before Service removal. Enabled reconciliation rejects references to runtime/trust/legacy CA output ConfigMaps and the serving TLS Secret; see the [reference API](../what/crd-api.md#toolset-ca-bundle-reference).
- Operator tests cover these generation/lifecycle behaviors, and the separate [full-manager live run](../what/ocpmcp.md#full-manager-live-cluster-verification) verifies public selected-key projections/checksums, private/unselected-key nonprojection, full `SetupWithManager` source events, content/reference-identity PodUID rollouts, invalid-source snapshot/pod retention, creation/CM binaryData recovery and disable/finalizer source preservation. NetObserv default baseline and explicit override projection passed; real OSSM/NetObserv backend semantics/auth remain unverified. Obsolete-key FailedMount/client-CA deletion-order transients recovered without source fixes; reserved-output annotation before rejection can cause metadata-RV recovery rolls despite unchanged trust data. Invalid-state retention is not atomic rollout or an unconditional no-recovery-roll guarantee. The first pinned-image harness verified actual OSSM/NetObserv HTTPS calls using singular custom CA files with that root absent from shared trust; the actual serializer extension verified path mapping using dummy refs only. Shared Prometheus/Loki/Tempo trust passes and remaining [PLANNED: OLS-2715] proofs are in [runtime verification](../what/ocpmcp.md#runtime-verification). Other clients retain their documented upstream-specific behavior.

### OSSM and NetObserv Mapping

| CR block/field | Generated TOML / behavior |
|---|---|
| `ossm` | Selection `ossm`, table `toolset_configs.kiali` |
| `ossm.url` | `url`; required validated HTTPS endpoint |
| `ossm.caBundleRef` | `certificate_authority`; required projected/validated bundle path |
| `netobserv.url` | `url`; explicit-URL form forbids any service fields and requires CA reference |
| `netobserv.namespace`, `.service`, `.port` | Without URL, compute HTTPS service URL from these fields and runtime defaults `netobserv`, `netobserv-plugin`, `9001`; emit explicit `url`, not a dependency on upstream endpoint detection |
| `netobserv.caBundleRef` | `certificate_authority`; explicit bundle path, or service-form implicit cluster service-CA path |
| Both blocks | Emit `insecure = false`; no corresponding CR option or insecure fallback |

For implicit NetObserv service trust, validate the operand-namespace `openshift-service-ca.crt` key `service-ca.crt`, copy its canonical PEM into snapshot key `netobserv.crt`, and project the toolset path above. This selects the service CA as the toolset source only in implicit mode; either mode projects a NetObserv-specific snapshot key. MCP's shared baseline always consumes and tracks the service CA when enabled, even without NetObserv or with `toolsets: []`. Union baseline, NetObserv and existing app-server/PostgreSQL consumers; switching forms or deselecting NetObserv must not remove baseline MCP tracking/trust. Creation/recovery, selected-key updates and deletion require reconciliation; valid changes trigger MCP rollout. Remove only obsolete toolset-specific desired projections/settings/consumer registrations without deleting sources or shared-baseline consumption.

Use field-presence-aware optional service fields and apply defaults in generation after mutual-exclusion validation. Admission defaults must not populate namespace/service/port on explicit-URL entries. HTTPS endpoint validation is an OLS contract stronger than upstream scheme/host checks. Do not apply this new mapping to omitted `toolsets` in a way that changes existing default behavior; OSSM/NetObserv are opt-in selections.

See [OSSM API](../what/crd-api.md#ossm-configuration) and [NetObserv API](../what/crd-api.md#netobserv-configuration).

### Helm and Metrics Mapping

| CR field | TOML key / mapping |
|---|---|
| `helm.storageDriver` | `[toolset_configs.helm].storage_driver`; default `configmap` for selected explicit Helm block; reject `secret` |
| `helm.allowedRegistries` | `allowed_registries`; optional allowlist with upstream normalization/path matching |
| `observability/metrics.prometheusURL` | `[toolset_configs."observability/metrics"].prometheus_url`; absent uses existing OLS Thanos URL |
| `observability/metrics.alertmanagerURL` | `alertmanager_url`; absent uses existing OLS Alertmanager URL |
| `observability/metrics.authMode` | `auth_mode`; default `header`, enum also permits `kubeconfig` |
| `observability/metrics.guardrails` | `guardrails`; explicit value wins; absent defaults to `!tsdb` without a Prometheus URL field, otherwise `all` |
| `observability/metrics.maxMetricCardinality` | `max_metric_cardinality`; emit only when supplied and its effective guardrail is active |
| `observability/metrics.maxLabelCardinality` | `max_label_cardinality`; emit only when supplied and its effective guardrail is active; preserve explicit zero |
| `observability/metrics.rangeQueryFullResponse` | `range_query_full_response`; default false, preserve supplied false/true |
| Explicit metrics block | `insecure = false`; no corresponding CR option |

Apply new Helm storage/metrics default mapping only for selected explicit entries. If `toolsets` is omitted, preserve the current TOML shown above, not an auto-generated Helm table. If either toolset is unselected, omit its config table; do not retain a default metrics table for an explicit list excluding metrics.

Metrics defaults are presence-dependent: preserve URL/guardrails/cardinality field presence, validate grammar and explicit limits against effective rules, and do not inject inactive cardinality defaults. Validate both metrics endpoints as absolute HTTPS URLs with nonempty hosts, reject HTTP/insecure settings, and emit verified TLS for explicit metrics selections. Do not add nonexistent per-toolset CA fields; shared observability trust uses the MCP-process contract below, with separate local pinned-image and full-manager live namespace HTTPS metric trust passes recorded in [runtime verification](../what/ocpmcp.md#runtime-verification), not real backend auth/RBAC proof. Logs/traces endpoint policy is specified below. See [Helm API](../what/crd-api.md#helm-configuration) and [metrics API](../what/crd-api.md#observability-metrics-configuration).

### Logs and Traces Endpoint Mapping

| Selected CR block/form | Generated config |
|---|---|
| `observability/logs` with `lokiURL` | Registered logs table with `loki_url` set to validated HTTPS endpoint; no ignored discovery setting |
| `observability/traces` with `tempoURL` | Registered traces table with `tempo_url` set to validated HTTPS API base; do not synthesize tenant paths |
| Either block without static URL | Corresponding registered table with explicit `use_route = true`; preserves per-call instance discovery |
| Either block `.authMode` | `auth_mode`; default `header`, enum also permits `kubeconfig`; no new credential fields |
| Either block | Explicit `insecure = false`; no CR option to override it |

Use field-presence-aware optional `useRoute` in the API: reject false and static URL plus any explicit value. Do not admission-default it; generation determines the form and emits true only for discovery. Route-resolution failures remain errors/unavailable instances according to the pinned implementation; the operator must not add Service/HTTP fallback or claim to intercept per-call discovery. Selecting a static endpoint does not bypass compatibility filters.

No new endpoint-specific `certificate_authority` keys are supported by these vendor parsers. Actual serializer-generated static Prometheus/Loki/Tempo HTTPS calls passed with shared custom trust; all three reject unknown CA and wrong hostname before backend HTTP. Route endpoint discovery and real stacks remain [PLANNED: OLS-2715]. Auth-mode mapping matches metrics and the derived-token/anonymous-request wrinkles in [shared transport](#shared-observability-transport); do not add credential fields, imply operator identity or treat permissive mock success as backend authorization. See [endpoint API](../what/crd-api.md#observability-logs-and-traces-endpoint-configuration).

### Shared MCP Trust Generation

1. For enabled MCP, preserve image/system roots and add validated `openshift-service-ca.crt` / `service-ca.crt` from the operand namespace to process system trust. Merge selected certificate keys from typed `mcpKubeServerConfig.caBundleRefs`; omitted/empty adds no user certificates and does not remove baseline roots.
2. Validate all source existence/keys/bundles before publishing the Phase 1 snapshot and runtime TOML. Project snapshot `service-ca.crt` and selected `shared-%03d.crt` keys read-only at `/etc/mcp-server/ca`; no live user volumes, user paths, credentials/private keys, source adoption or deletion. Shared references retain all consumer watcher/reconciliation targets.
3. Set `SSL_CERT_DIR=/etc/ssl/certs:/etc/pki/tls/certs:/etc/mcp-server/ca` and leave `SSL_CERT_FILE` unset. Default image certificate files remain available and image directories are not overlaid. Pinned-image inspection confirms that exact environment, SSL_CERT_FILE unset, no image certificate paths overmounted and 146 certificates retained in `/etc/pki/tls/certs/ca-bundle.crt`. Actual static Prometheus/Loki/Tempo HTTPS validates shared custom trust; external public-root HTTPS was not exercised. Separate full-manager live projection/checksum/watch/PodUID-rollout and real Service CA injection checks passed; see [live verification](../what/ocpmcp.md#full-manager-live-cluster-verification), not a hot-reload or production-deployment claim.
4. Make both baseline service CA and user roots available through system trust rather than solely the vendor's service-CA fallback path: populated REST CAData can suppress that fallback. Vendor clients append REST CAData to system roots, so shared roots must remain usable in that case. Never emit unsupported observability `certificate_authority` fields or reuse global OAuth trust as a universal setting.
5. Track canonical certificate content and reference identities with the snapshot/pod-template trust hash and compare complete desired projections/environment. Phase 2 requires matching persisted snapshot data/hash after revalidation. Valid changes roll MCP (system-root pools are cached); invalid/deleted sources preserve the last validated snapshot and suppress MCP roll. User-source mutation cannot update mounted data directly. This is not a claim of atomic multi-resource publication or immutability of the operator-owned ConfigMap.
6. Process trust affects clients that use system roots, possibly beyond observability. It does not override client-specific replacement root pools or provide per-endpoint isolation. Keep OSSM/NetObserv `caBundleRef` projection/TOML mappings separate: local calls succeeded via singular CA files with their custom root absent from shared trust, not proof that those clients universally consume shared roots.
7. Local pinned-image passes cover actual serializer defaults/empty/seven selections, static observability HTTPS/custom trust and unknown-CA/hostname rejection. First-harness Prometheus/Alertmanager calls verify synthetic shared service roots with unrelated populated REST CAData; metric calls also verify REST CAData-only roots and expected accept/reject after root removal/rotation and process restart, not hot reload. Separate full-manager live CA events/public projections/checksums, real Service CA injection, invalid-source retention/recovery, lifecycle cleanup and bounded real Kubernetes caller RBAC passed (93/93 unique live assertions; 30/30 Strict dry-run checks with singleton-aware wrong-name-only valid controls, not successful creates). Actual namespace Python HTTPS backend returned metric 2715 using custom shared roots; root removal/rotation failed TLS before additional HTTP, source updates rolled MCP and real Service CA-only baseline succeeded. Local-development mode skipped operator ServiceMonitor/metrics reader reconciliation. [PLANNED: OLS-2715] Verify external public-root HTTPS, live authenticated Prometheus/Loki/Tempo backend auth/RBAC, Route discovery/real stacks and remaining tool/helper diagnostic prerequisites. No insecure fallback or authorization bypass is added; [runtime matrix](../what/ocpmcp.md#verification-matrix) bounds each pass. See [API](../what/crd-api.md#shared-mcp-ca-bundles) and [TLS contract](../what/tls.md#shared-mcp-process-trust).

### CNI Diagnostics Mapping

| CR field | TOML key under `toolset_configs.cni-diagnostics` | Effective default |
|---|---|---|
| `kernelDebugImage` | `kernel_debug_image` | `nicolaka/netshoot:v0.16` |
| `tcpdumpImage` | `tcpdump_image` | `nicolaka/netshoot:v0.16` |
| `pwruImage` | `pwru_image` | `docker.io/cilium/pwru:v1.0.10` |

Generate a table only for selected explicit CNI entries, applying defaults for absent fields and rejecting supplied empty/invalid image references. Retain image override values unchanged after syntax validation; do not claim the operator mirrors images, installs binaries or grants debug-pod privileges. Helper images are separate from the MCP image override and require disconnected/runtime validation.

All six CNI tools have destructive=false annotations in this pin, and therefore pass inherited destructive filtering. They directly invoke debug/exec helpers: hiding `nodes_debug_exec` or `pods_exec` does not block those paths. Preserve restricted MCP pod security and caller authorization; privileged node debug workload admission is an independent runtime prerequisite. See [CNI API](../what/crd-api.md#cni-diagnostics-configuration).

### Empty Blocks and Retained Security Policy

- Emit selection names for `core`, `config`, `kcp`, `kubevirt`, `tekton`, `cluster-diagnostics`, `netedge`, `oadp`, `ovn-kubernetes` and `observability/otelcol`, with no user-facing settings or unregistered config tables. Collector schema config can remain absent and use embedded defaults.
- Always pin `read_only = false` and `disable_destructive = true`. The latter makes the currently inherited value explicit without changing effective behavior. Retain denied resources, compatibility filtering, disabled-tool defaults and restricted MCP pod security; do not add privilege/provider/dependency grants or security switches.
- A selected toolset may advertise zero tools/prompts because of filters or missing runtime prerequisites. Do not reject its otherwise valid CR, count tools to decide selection, auto-enable dependencies or switch off filters. This includes inert cluster-diagnostics/config selections, prompt-only OADP and conditional Collector-schema tools.
- Preserve omitted-field backward behavior: existing default toolsets and per-toolset config remain unchanged; pinning destructive=true and implementing the separately agreed shared-trust behavior are independent of replacement selection. Exercise remaining backend/provider/prompt wrinkles in integration testing and subsequent fixes rather than broaden this change's admission gates.
- See [API](../what/crd-api.md#configuration-free-toolsets) and [security baseline](../what/security.md#openshift-mcp-server-security). No new tool exposure or universal verified-TLS claim is made for clients such as NetEdge that have their own upstream behavior.

## Key Abstractions

### Credential Injection Pattern
Provider credentials are mounted as files at `/etc/apikeys/<secretName>/`. The OLS config references the directory path as `credentials_path`. The secret key used is `apitoken` by default, overridable by `credentialKey` in the CR.

### External Resource Iteration
`utils.ForEachExternalSecret(cr, callback)` and `utils.ForEachExternalConfigMap(cr, callback)` provide consistent iteration over CR-referenced external resources. Each callback receives `(name, source)` where `source` identifies the reference origin:
- `"llm-provider-<providerName>"` for LLM credential secrets
- `"mcp-header-<serverName>"` for MCP header secrets
- `"mcp-ca"` for selected toolset/shared MCP CA sources (enabled MCP only); baseline service CA is independently tracked
- `"additional-ca"` for additional CA configmaps
- `"proxy-ca"` for proxy CA configmaps

### Config Building Pattern
Config is built programmatically using typed Go structs from the `utils/` package (e.g., `utils.AppSrvConfigFile`) and marshaled with `yaml.Marshal()`. No templates are used.

### PostgreSQL Schema Isolation
PostgreSQL schemas isolate data from different components within the same database:
- `conversation_cache` schema: conversation history (created by Postgres bootstrap)
- `quota` schema: token quota tracking (created by Postgres bootstrap)
- `templogs` schema: temporary audit log storage (always created by OTEL Collector `postgres_admin` at startup; not part of Postgres bootstrap). `spec.audit.logging` only toggles the logs export pipeline. See `templog.md`.

## Integration Points

| Config Section | Source | Notes |
|---|---|---|
| Provider credentials | CR `spec.llm.providers[].credentialsSecretRef` | File mount at `/etc/apikeys/<secretName>/` |
| Default model/provider | CR `spec.ols.defaultModel`, `spec.ols.defaultProvider` | Required fields |
| Log level | CR `spec.ols.logLevel` | Enum: DEBUG, INFO, WARNING, ERROR, CRITICAL. Default: INFO |
| PostgreSQL connection | `utils/constants.go` | Host built from service name + namespace + ".svc" |
| TLS certs | Service-ca operator or user-provided secret | Path: `/etc/certs/lightspeed-tls/` |
| BYOK RAG indexes | CR `spec.ols.rag[]` | Local FAISS indexes only; empty list when no BYOK RAG configured |
| `solr_hybrid` | Operator defaults + `!byokRAGOnly` | OCP product docs via OKP Solr at `https://lightspeed-rhokp.<ns>.svc:8443` (standalone RHOKP Deployment) |
| RHOKP image | `--rhokp-image` flag | Standalone RHOKP Deployment image; default from `related_images.json` (`rhokp`); listed in bundle `relatedImages` |
| ROSA product | Console brand + Infrastructure topology (detected at operator startup) | `OLS_ROSA_PRODUCT` env var on app-server when brand is `ROSA` (not in config YAML). `External` → HCP product; otherwise Classic. Omitted on detection failure or non-ROSA. |
| Built-in MCP client | CR `spec.ols.introspectionEnabled` + `spec.ols.mcpKubeServerConfig.timeout` | Enabled by introspection (absent means true); no `MCPServer` gate required |
| External MCP clients | CR `spec.mcpServers[]` | Require `MCPServer` gate |
| Built-in MCP runtime | `ocpmcp.GenerateConfigTOML()` + typed `mcpKubeServerConfig.toolsets` | Omitted-selection defaults or exact explicit selection; mounted only by standalone MCP |
| Tool filtering | CR `spec.ols.toolFilteringConfig` | Feature gated by `ToolFiltering` gate; requires MCP servers |
| Proxy config | CR `spec.ols.proxyConfig` | Proxy URL + optional CA cert configmap |
| Query filters | CR `spec.ols.queryFilters[]` | Regex patterns for content filtering |
| Quota config | CR `spec.ols.quotaHandlersConfig` | Rate limiting with scheduler period fixed at 300s |
| Tool-result inspection | CR `spec.ols.guardrails.toolResultInspection.enabled` | [PLANNED: OLS-3928] Writes `ols_config.guardrails.tool_result_inspection.enabled`; same effective value enters the agentic handoff |

## Implementation Notes

- Config YAML is built programmatically using Go structs and marshaled with `yaml.Marshal()`, not templates.
- The fake provider config is hardcoded with test response values (`"This is a preconfigured fake response."`).
- PostgreSQL uses `POSTGRESQL_ADMIN_PASSWORD` env var for the admin password (mapped from the generated secret in the deployment spec, not shown in config files).
- Exporter config for data collector uses a separate ConfigMap (`utils.ExporterConfigCmName`) with collection interval of 300 seconds, cleanup after send, and ingress URL to `console.redhat.com`.
- The `OLSSystemPromptFileName` is stored as a separate key in the OLS config ConfigMap when `querySystemPrompt` is set.
