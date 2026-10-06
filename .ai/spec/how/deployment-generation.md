# Deployment Generation

## Module Map

| File | Key Functions | Responsibility |
|---|---|---|
| `internal/controller/appserver/deployment.go` | `GenerateOLSDeployment()`, `updateOLSDeployment()`, `RestartAppServer()`, `dataCollectorEnabled()` | AppServer deployment spec, change detection, restart |
| `internal/controller/postgres/deployment.go` | `GeneratePostgresDeployment()`, `UpdatePostgresDeployment()` | PostgreSQL deployment spec |
| `internal/controller/console/deployment.go` | `GenerateConsoleUIDeployment()` | Console UI deployment spec |
| `internal/controller/agenticconsole/deployment.go` | `GenerateAgenticConsoleUIDeployment()` | Agentic console plugin deployment spec |
| `internal/controller/otelcollector/deployment.go` | `GenerateOtelCollectorDeployment()`, `UpdateOtelCollectorDeployment()` | OTEL Collector deployment spec; [PLANNED: OLS-3569] conditional Agentic exporter sidecar and spool mounts |
| `internal/controller/ocpmcp/deployment.go` | `GenerateDeployment()`, `UpdateDeployment()` | Standalone OpenShift MCP deployment spec |
| `internal/controller/rhokp/deployment.go` | `GenerateDeployment()`, `UpdateDeployment()` | Standalone RHOKP deployment spec |
| `internal/controller/alertsadapter/deployment.go` | `GenerateDeployment()` | Alerts adapter deployment spec |

## Data Flow

### AppServer Deployment Construction
```
GenerateOLSDeployment(r, cr)
  1. Check dataCollectorEnabled (requires both user config AND telemetry pull secret)
  2. Build LLM provider credential volumes + mounts (via ForEachExternalSecret, source "llm-provider-*")
  3. Build postgres secret volume + mount
  4. Build TLS volume + mount (user-provided KeyCertSecretRef OR service-ca generated OLSCertsSecretName)
  5. Build OLS config configmap volume + mount
  6. Conditionally add data collector volumes (user-data emptyDir, exporter config CM)
  7. Add kube-root-ca.crt configmap volume + cert-bundle emptyDir volume
  8. Add user-provided CA volumes (additional-ca CM, proxy-ca CM via ForEachExternalConfigMap)
  9. Add RAG emptyDir volume (if spec.ols.rag configured)
  10. Add postgres-ca configmap volume + tmp emptyDir volume
  11. Add MCP header secret volumes (via ForEachExternalSecret, source "mcp-*")
  12. Build init containers:
      a. PostgreSQL wait init container (polls pg service)
      b. RAG init containers (one per RAG entry, copies data to shared emptyDir)
      c. [PLANNED: OLS-3799] RHOKP wait init container (when `!byokRAGOnly`) — not yet implemented; today only the PostgreSQL wait + RAG init containers are generated.
  13. Get OLS config ConfigMap ResourceVersion for tracking (not the MCP TOML ConfigMap)
  14. Get proxy CA cert hash for tracking annotation
  15. Assemble Deployment:
      - Container: "lightspeed-service-api", image: r.GetAppServerImage(), port: 8443
      - Env: OLS_CONFIG_FILE path + proxy vars (HTTP_PROXY, HTTPS_PROXY, NO_PROXY)
      - Env: OCP_CLUSTER_VERSION (`<major>.<minor>`) when `!byokRAGOnly` (same cluster-version source as console UI)
      - Env: OLS_ROSA_PRODUCT when `!byokRAGOnly` and startup detection finds ROSA brand. `External` topology → `red_hat_openshift_service_on_aws` (HCP); any other topology on ROSA → `red_hat_openshift_service_on_aws_classic_architecture` (Classic). Omitted on non-ROSA or detection failure.
      - Probes: HTTPS GET on /readiness (initial: 30s, period: 30s, timeout: 30s, failure: 15) and /liveness (initial: 30s, period: 30s, timeout: 30s, failure: 3 — OLS-3221)
      - Default resources: 500m CPU request, 1Gi memory request (no limits)
  16. Apply pod-level config (replicas, nodeSelector, tolerations)
  17. Set ImageStream triggers annotation (if RAG configured)
  18. Set owner reference to OLSConfig CR
  19. Conditionally add data collector sidecar container ("lightspeed-to-dataverse-exporter")
  20. Mount classic OTEL CA Secret `lightspeed-otel-client-ca` at `/etc/certs/otel-collector-ca/`; when `!byokRAGOnly`, mount classic RHOKP CA Secret `lightspeed-rhokp-client-ca` at `/etc/certs/rhokp-ca/`. Their keys (`otel-ca.crt` / `rhokp-ca.crt`) project to `service-ca.crt`, referenced by `extra_ca`; OTEL also uses `OTEL_EXPORTER_OTLP_CERTIFICATE`. RHOKP is standalone, not a sidecar — see ../what/rhokp.md.
  21. When introspection is enabled, mount classic MCP CA Secret `lightspeed-mcp-client-ca` using `AgenticMCPCASecretDataKey` (`mcp-ca.crt`) projected as `AppOpenShiftMCPServerCACertFile` (`service-ca.crt`) in `/etc/certs/openshift-mcp-server-ca/`. No MCP sidecar or agentic CA Secret mount.
```

### OTEL Collector Deployment — Agentic Collection

[PLANNED: OLS-3569] Collector runtime configuration and `GenerateOtelCollectorDeployment()` use the same Agentic collection gate:

1. [PLANNED: OLS-3569] Generate these resources only where the parent-defined Agentic v2 bundle is available. Within that scope, evaluate `!spec.ols.userDataCollection.transcriptsDisabled` AND usable `cloud.openshift.com` auth in `openshift-config/pull-secret`; do not use the Classic feedback-OR-transcripts expression.
2. [PLANNED: OLS-3569] When enabled, append one shared `emptyDir` with `sizeLimit` set from an explicit Agentic spool deployment configuration value, mount it in the Collector at `/var/lib/lightspeed-data-collection`, and mount it in a separate `lightspeed-to-dataverse-exporter` sidecar at `/app-root/ols-user-data`. No authoritative numeric sizing convention is defined here. At capacity, follow Rules 33-34 in the parent `../../../../.ai/spec/what/agentic-data-collection.md`; do not add local eviction, overwrite, back-pressure, loss, or aggregation policy.
3. [PLANNED: OLS-3569] Build the sidecar with `GetDataverseExporterImage()`, the existing telemetry credentials, `lightspeed-exporter-config`, `spec.olsDataCollector.logLevel`, and `spec.ols.deployment.dataCollector.resources`. As a sidecar, it inherits Collector pod scheduling from `spec.ols.deployment.otelCollector`.
4. [PLANNED: OLS-3569] When enabled, include the trace-only Agentic product-collection pipeline in the Collector runtime ConfigMap. Preserve the existing OTLP receiver, logs/templog, admin, metrics, and optional trace-forwarding configuration.
5. [PLANNED: OLS-3569] When disabled, omit the Agentic pipeline, sidecar, `emptyDir`, and both mounts. A gate transition changes the desired pod template and follows the normal Collector rollout path.
6. [PLANNED: OLS-3569] Do not change the app-server exporter or add collection state to `lightspeed-agentic-configuration`. See `what/agentic-data-collection.md` for the operator contract and its parent-spec references.

### AppServer Phase 2 Task Order
`ReconcileAppServerDeployment()` in `appserver/reconciler.go` executes fail-fast: `RefreshClientCASecrets`, exporter ConfigMap, Deployment, Service, TLS certificates, ServiceMonitor, PrometheusRule. Client CA refresh precedes Deployment generation; it is also the first step in `RestartAppServer()`.

### Standalone MCP Deployment and Client Wiring
- `ocpmcp.GenerateConfigMap()` writes `config.toml` from `configTOML` in `internal/controller/ocpmcp/assets.go`. Current toolsets: `["core", "config", "helm", "observability/metrics", "kubevirt"]`. It sets `read_only = false`, `experimental_enable_target_compatibility_tool_filters = true`, port `8443`, `tls_cert = "/etc/tls/tls.crt"`, and `tls_key = "/etc/tls/tls.key"`. It denies core/v1 Secret and rbac.authorization.k8s.io/v1 resources. `[toolset_configs."observability/metrics"]` supplies Thanos/Alertmanager URLs and `guardrails = "!tsdb"`.
- `ocpmcp.GenerateDeployment()` starts `/openshift-mcp-server --config /etc/mcp-server/config.toml`; no `--tls-cert` / `--tls-key` flags. It mounts TOML, serving Secret `openshift-mcp-server-tls` at `/etc/tls`, and `/tmp` EmptyDir, with HTTPS `/healthz` probes and `PullIfNotPresent`.
- MCP Phase 2 is fail-fast: Service, TLS-key wait, Deployment, ServiceMonitor. The monitor scrapes port `https`, path `/metrics`, interval `30s`, validating `openshift-mcp-server.<ns>.svc` against `/etc/prometheus/configmaps/serving-certs-ca-bundle/service-ca.crt`, without client certificate or bearer token. `utils.ReconcileServiceMonitor()` skips unavailable Prometheus CRDs. See [ocpmcp.md](../what/ocpmcp.md).
- `appserver.generateMCPServerConfigs()` emits the built-in `openshift` endpoint via `utils.OpenShiftMCPServerServiceURL()`. Headers use `utils.K8S_AUTH_HEADER` (`Authorization`) mapped to `utils.KUBERNETES_PLACEHOLDER` (`kubernetes`), not a literal token. User header types `kubernetes` and `client` use `KUBERNETES_PLACEHOLDER` / `CLIENT_PLACEHOLDER` (`client`); secret headers resolve to file paths. See [app-server.md](../what/app-server.md).
- `RefreshClientCASecrets()` in `appserver/assets.go` uses separate table entries for classic `lightspeed-*-client-ca` and agentic `lightspeed-agentic-*-ca`. Classic OTEL is always enabled, MCP follows introspection, RHOKP follows `!byokRAGOnly`. Agentic entries require Enabled `AgenticGate`; Disabled/Unknown skip reads/writes/deletes. With Enabled, MCP/RHOKP opt-out deletes their agentic Secret. Source PEM is `openshift-service-ca.crt` / `service-ca.crt`.

### Change Detection Pattern
Deployment update functions compare desired vs existing specs via `DeploymentSpecEqual()` and component-specific tracked inputs. A detected change updates spec/tracking annotations and sets pod-template `ols.openshift.io/force-reload` to RFC3339Nano to roll pods, either directly or through a component restart function.

**AppServer tracks:** OLS config CM ResourceVersion (`OLSConfigMapResourceVersionAnnotation`), proxy CA content hash (`ProxyCACertHashAnnotation`), RAG spec hash (`RAGSpecHashAnnotation`), and desired Deployment spec. It does **not** track MCP TOML ConfigMap ResourceVersion or any MCP CA hash.

**MCP tracks:** TOML ConfigMap ResourceVersion (`OpenShiftMCPServerConfigMapResourceVersionAnnotation`, `ols.openshift.io/mcp-server-configmap-version`) and TLS Secret ResourceVersion (`OpenShiftMCPServerTLSSecretResourceVersionAnnotation`, `ols.openshift.io/mcp-server-tls-secret-version`) on Deployment metadata, plus desired spec. `UpdateDeployment()` copies both annotations, stamps force-reload directly on the pod template, and persists a single Update when any tracked input changes. It does not call `Restart()`; that function is used by TLS watchers and re-fetches the Deployment.

**AppServer restart:** Get OLSConfig, `RefreshClientCASecrets()`, re-Get Deployment, apply optional caller Spec/object-annotation mutations, set force-reload, Update. Refresh failure skips roll. No direct handoff touch occurs; serving-cert watchers dispatch a separate Enabled-agentic-gated `TouchAgenticConfiguration()` callback and continue to it after errors. Service-ca ConfigMap changes target app-server and PostgreSQL only. See [reconciliation.md](reconciliation.md).

## Key Abstractions

### Resource Requirement Defaults
Each component defines default CPU/memory requests in local `get*Resources()` functions. Per [OpenShift conventions](https://github.com/openshift/enhancements/blob/master/CONVENTIONS.md#resources-and-limits), operator defaults set requests only and do not set limits. User-provided values from the CR override defaults via `utils.GetResourcesOrDefault()` which returns user values if non-nil, otherwise defaults. Users may still set limits via the CRD if needed for their environment.

Default resources by container:
| Container | CPU Request | Memory Request | Ephemeral Storage Request |
|---|---|---|---|
| AppServer `lightspeed-service-api` | 500m | 1Gi | — |
| Data collector | 50m | 64Mi | — |
| Collector-side Agentic data exporter | [PLANNED: OLS-3569] 50m | [PLANNED: OLS-3569] 64Mi | — |
| MCP server (standalone) | 50m | 64Mi | — |
| RHOKP `rhokp` (standalone) | 2000m | 2Gi | — (75Gi EmptyDir `sizeLimit`, not an ephemeral-storage request) |

### Volume/Mount Construction
Volumes and mounts are built as slices and conditionally appended using inline append patterns.

### Init Container Generation
- **PostgreSQL wait:** `utils.GeneratePostgresWaitInitContainer()` generates a container that polls the PostgreSQL service until it responds.
- **RHOKP wait (when `!byokRAGOnly`):** [PLANNED: OLS-3799] — not yet implemented. When added, `utils.GenerateRHOKPWaitInitContainer()` would poll the RHOKP Solr ping endpoint until it responds (~360s budget), following the PostgreSQL wait pattern. No such function exists today.
- **RAG (AppServer only):** `GenerateRAGInitContainers()` creates one init container per RAG entry, each copying data from the RAG image to the shared emptyDir volume at `/app-root/rag/rag-<index>`.

### ImageStream Triggers (AppServer only)
RAG images use OpenShift ImageStreams for automatic updates. The deployment is annotated with `image.openshift.io/triggers` JSON that maps ImageStreamTag changes to init container image fields. This allows RAG content updates without operator intervention.

### Data Collector Enablement
The existing Classic app-server collector gate is computed from two inputs:
1. User data collection config: `!FeedbackDisabled || !TranscriptsDisabled`
2. Telemetry pull secret: `openshift-config/pull-secret` has `.auths."cloud.openshift.com"` entry in `.dockerconfigjson`

Both must be true. The service ID is `"ols"` unless the CR has `openstack.org/lightspeed-owner-id` label, in which case it's `"rhos-lightspeed"`.

[PLANNED: OLS-3569] The Collector-side Agentic resources use the independent gate defined in `what/agentic-data-collection.md`; the Classic gate and handoff ConfigMap remain unchanged.

### Pod Scheduling Configuration
`utils.ApplyPodDeploymentConfig()` applies scheduling from `cr.Spec.OLSConfig.DeploymentConfig.APIContainer`:
- Replicas (configurable for API container; forced to 1 for postgres and console)
- NodeSelector
- Tolerations

Affinity and topology spread constraints are not exposed on `Config` (CRD size); use cluster-level defaults or patch deployments out of band if needed.

## Integration Points

| Consumer | Provider | Data |
|---|---|---|
| Deployment spec | `utils/constants.go` | Resource names, ports, mount paths |
| Container resources | CR `spec.ols.deployment.api.resources` | User-overridable CPU/memory |
| RHOKP resources | CR `spec.ols.deployment.rhokp.resources` | User-overridable CPU/memory/ephemeral storage |
| Pod scheduling | CR `spec.ols.deployment.api` | Tolerations, nodeSelector |
| Volume secrets | Kubernetes Secrets | LLM credentials, TLS certs, PostgreSQL password, MCP header values |
| Volume configmaps | Generated ConfigMaps | OLS config, nginx config; MCP TOML is mounted only by the standalone MCP Deployment; [PLANNED: OLS-3569] existing exporter config also mounted by the Collector-side Agentic exporter |
| Proxy env vars | `utils.GetProxyEnvVars()` | HTTP_PROXY, HTTPS_PROXY, NO_PROXY from cluster |
| RAG images | CR `spec.ols.rag[].image` | Container images for init containers |
| RHOKP image | `--rhokp-image` flag | Standalone RHOKP Deployment container image; default from `related_images.json` (`rhokp`) |

## Agentic Controller Deployment (OLM-managed)

Unlike the AppServer, PostgreSQL, and Console UI deployments (which are reconciled by the lightspeed-operator controller at runtime), the agentic controller deployment is statically defined in the CSV and managed by OLM. The lightspeed-operator controller has no code to generate, update, or restart the agentic controller deployment. The agentic controller's operand images (agentic console plugin, etc.) are configured via startup flags on its deployment in the CSV, not via the lightspeed-operator's flags.

## Implementation Notes

- `RevisionHistoryLimit` is set to 1 for all deployments to minimize stored ReplicaSets.
- All sidecar containers use `utils.RestrictedContainerSecurityContext()` which sets: `RunAsNonRoot: true`, `ReadOnlyRootFilesystem: true`, `AllowPrivilegeEscalation: false`, Drop ALL capabilities, RuntimeDefault seccomp profile.
- The force-reload annotation (`ols.openshift.io/force-reload`) is set to `time.Now().Format(time.RFC3339Nano)` to guarantee uniqueness and trigger pod replacement.
- The OpenShift MCP server always uses `PullIfNotPresent`.
- The `VolumeDefaultMode` is `int32(420)` (0644 octal), defined in `utils/constants.go`.
- AppServer deployment name is `utils.OLSAppServerDeploymentName` (`"lightspeed-app-server"`).
