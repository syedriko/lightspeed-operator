# Project Structure

## Module Map

| Path | Key Symbols | Responsibility |
|---|---|---|
| `api/v1alpha1/olsconfig_types.go` | `OLSConfig`, `OLSConfigSpec`, `OLSConfigStatus`, `ProviderSpec`, `ModelSpec` | CRD type definitions, validation markers, defaults |
| `api/v1alpha1/mcp_types.go` | `MCPToolsetSelection`, `MCPCAReference`, typed toolset configs, `MCPEmptyConfig`, `CAReferences()` | 17-selection schema/CEL validation; empty-map MaxProperties=0; Strict unknown-field caveat; local CA key references |
| `api/v1alpha1/groupversion_info.go` | `SchemeBuilder`, `GroupVersion` | API group/version registration |
| `api/v1alpha1/zz_generated.deepcopy.go` | Generated `DeepCopyObject()` methods | Auto-generated deep copy |
| `cmd/main.go` | `main()`, `overrideImages()` | Operator entry point, flag parsing, manager setup |
| `internal/controller/olsconfig_controller.go` | `OLSConfigReconciler`, `Reconcile()`, `SetupWithManager()` | Main reconciler, orchestration, watcher registration |
| `internal/controller/olsconfig_helpers.go` | `UpdateStatusCondition()`, `checkDeploymentStatus()`, `annotateExternalResources()`, `syncOpenShiftMCPServerTLSWatcher()`, `shouldWatchSecret()`, `GetAgenticConsoleImage()` | Status management, diagnostics, annotation, conditional MCP TLS watcher, watcher predicates, image getter for agentic console |
| `internal/controller/operator_assets.go` | `ReconcileServiceMonitorForOperator()`, `ReconcileNetworkPolicyForOperator()` | Operator-level resources |
| `internal/controller/appserver/reconciler.go` | `ReconcileAppServerResources()`, `ReconcileAppServerDeployment()` | AppServer Phase 1 + Phase 2 orchestration |
| `internal/controller/appserver/deployment.go` | `GenerateOLSDeployment()`, `updateOLSDeployment()` | AppServer deployment generation, update detection |
| `internal/controller/appserver/assets.go` | `GenerateOLSConfigMap()`, service/RBAC/ServiceMonitor/PrometheusRule generators, `RefreshClientCASecrets()`, separate classic/agentic client CA Secrets | AppServer resource generation, OLS config YAML |
| `internal/controller/appserver/rag.go` | `GenerateRAGInitContainers()`, `reconcileImageStreams()` | RAG init container and ImageStream management |
| `internal/controller/postgres/reconciler.go` | `ReconcilePostgresResources()`, `ReconcilePostgresDeployment()` | PostgreSQL Phase 1 + Phase 2 |
| `internal/controller/postgres/deployment.go` | `GeneratePostgresDeployment()` | PostgreSQL deployment generation |
| `internal/controller/postgres/assets.go` | `GeneratePostgresConfigMap()`, `GeneratePostgresBootstrapSecret()`, `GeneratePostgresSecret()` | PostgreSQL config, bootstrap script, credentials |
| `internal/controller/otelcollector/reconciler.go` | `ReconcileOtelCollectorResources()`, `ReconcileOtelCollectorDeployment()`, `RestartOtelCollector()` | OTEL Collector Phase 1 + Phase 2 + rolling restart |
| `internal/controller/otelcollector/deployment.go` | `GenerateOtelCollectorDeployment()`, `UpdateOtelCollectorDeployment()` | OTEL Collector deployment generation, update detection |
| `internal/controller/otelcollector/assets.go` | Runtime ConfigMap, Service, NetworkPolicy, ServiceMonitor, ServiceAccount, Postgres DSN Secret generators | OTEL Collector resource generation, collector runtime YAML, HTTPS metrics |
| `internal/controller/agenticintegration/reconciler.go` | `ReconcileAgenticIntegrationResources()` | Classic→agentic handoff ConfigMap at end of Phase 2 |
| `internal/controller/agenticintegration/assets.go` | Thin PodSpec, handoff ConfigMap, `TouchAgenticConfiguration()` | Agentic handoff ConfigMap generation, including resolved provider-egress TLS values and CA references / cert-reload touch |
| `internal/controller/ocpmcp/reconciler.go` | `ReconcileResources()`, `ReconcileDeployment()`, `Remove()`, `Restart()` | Standalone OpenShift MCP Phase 1 + Phase 2 + teardown + rolling restart |
| `internal/controller/ocpmcp/deployment.go` | `GenerateDeployment()`, `UpdateDeployment()` | MCP Deployment generation and update detection |
| `internal/controller/ocpmcp/assets.go` | ConfigMap (TOML), Service, NetworkPolicy, ServiceAccount, `GetConfigVolumeAndMount()`, `GetConfigPath()` | MCP resource generation |
| `internal/controller/ocpmcp/config.go` | `GenerateConfigTOML()` | Typed replacement selection and selected-table TOML generation; retained security policy |
| `internal/controller/ocpmcp/trust.go` | `GenerateTrustConfigMap()`, `canonicalCABundle()`, `trustVolumes()` | Validated public-only owned CA snapshot, selected-key projections, content/reference-identity hash and ownership guards |
| `internal/controller/console/reconciler.go` | `ReconcileConsoleUIResources()`, `ReconcileConsoleUIDeploymentAndPlugin()`, `RemoveConsoleUI()` | Chat console plugin Phase 1 + Phase 2 + cleanup |
| `internal/controller/console/deployment.go` | `GenerateConsoleUIDeployment()` | Chat console plugin deployment generation |
| `internal/controller/console/assets.go` | ConsolePlugin CR generator, nginx config, service, network policy | Chat console plugin resource generation |
| `internal/controller/agenticconsole/reconciler.go` | `ReconcileAgenticConsoleUIResources()`, `ReconcileAgenticConsoleUIDeploymentAndPlugin()`, `RemoveAgenticConsole()` | Agentic console plugin Phase 1 + Phase 2 + cleanup |
| `internal/controller/agenticconsole/deployment.go` | `GenerateAgenticConsoleUIDeployment()` | Agentic console plugin deployment generation |
| `internal/controller/agenticconsole/assets.go` | ConsolePlugin CR generator, nginx config, service, network policy | Agentic console plugin resource generation |
| `internal/controller/utils/console_plugin_reconciler.go` | Shared ConsolePlugin reconcile helpers | Used by `console/` and `agenticconsole/` |
| `internal/controller/utils/service_monitor_reconciler.go` | `ReconcileServiceMonitor()` | Shared ServiceMonitor create/update (appserver + otelcollector) |
| `internal/controller/alertsadapter/reconciler.go` | `ReconcileAlertsAdapterResources()`, `ReconcileAlertsAdapterDeployment()`, `RemoveAlertsAdapter()`, `RestartAlertsAdapter()` | Alerts adapter Phase 1 + Phase 2 + operand teardown (disable/finalizer) + rolling restart |
| `internal/controller/alertsadapter/deployment.go` | `GenerateDeployment()` | Alerts adapter deployment generation |
| `internal/controller/alertsadapter/assets.go` | SA, ClusterRole, ClusterRoleBinding, monitoring RoleBinding, NetworkPolicy generators | Alerts adapter resource generation |
| `internal/controller/reconciler/interface.go` | `Reconciler` interface | Dependency injection interface for component packages |
| `internal/controller/utils/constants.go` | ~200 constants | Resource names, ports, paths, annotation keys, defaults |
| `internal/controller/utils/errors.go` | ~80 error message constants | Structured error messages for all operations |
| `internal/controller/utils/postgres_wait.go` | `GeneratePostgresWaitInitContainer()` | PostgreSQL readiness init container |
| `internal/controller/watchers/watchers.go` | `SecretUpdateHandler`, `ConfigMapUpdateHandler`, `SecretWatcherFilter()`, `ConfigMapWatcherFilter()` | External resource change handlers, deployment restart logic |
| `internal/tls/` | `GetTLSProfileSpec()`, `FetchAPIServerTlsProfile()` | TLS profile resolution |
| `config/crd/` | CRD YAML manifests | Generated CRD definitions |
| `config/rbac/` | RBAC YAML manifests | Generated RBAC rules |
| `config/manager/` | Deployment manifest | Operator deployment |
| `test/e2e/` | E2E test suites | End-to-end integration tests |

## Startup Sequence

```
main()
  1. Parse flags (images, namespace, leader election, secure metrics)
  2. Get Kubernetes config and client
  3. Detect OpenShift version (major, minor)
  4. Select console image (single image; OCP minor version no longer determines selection)
  5. Check Prometheus Operator availability (probe CRD existence)
  6. Configure metrics TLS (if --secure-metrics-server):
     a. Read client CA from openshift-monitoring/metrics-client-ca
     b. Read TLS profile from OLSConfig CR or API server
  7. Create controller manager with:
     - Multi-namespace cache (operator ns + openshift-config for secrets)
     - TLS metrics server
     - Health/readiness probes (ping)
     - Leader election (if enabled)
  8. Build WatcherConfig (system secrets + configmaps)
  9. Create OLSConfigReconciler with all options
  10. Register with manager via SetupWithManager()
  11. Start manager (blocking)
```

## Data Flow

### Reconciliation Flow
```
OLSConfigReconciler.Reconcile()
  1. getAndValidateCR()             -- Only processes CR named "cluster"
  2. handleFinalizer()              -- Add finalizer or run deletion cleanup
  3. reconcileOperatorResources()   -- ServiceMonitor, NetworkPolicy (operator-level)
  4. annotateExternalResources()    -- Mark external secrets/configmaps for watching
  5. reconcileIndependentResources()  -- Phase 1: ConfigMaps, Secrets, ServiceAccounts, RBAC, NetworkPolicies (order matches code)
     +-- console.ReconcileConsoleUIResources()
     +-- postgres.ReconcilePostgresResources()
     +-- otelcollector.ReconcileOtelCollectorResources()
     +-- ocpmcp.ReconcileResources()   # when introspectionEnabled; else always attempt idempotent Remove(), no condition-history gate
     +-- rhokp.ReconcileResources()     # when !byokRAGOnly; else Remove only if wasComponentEnabled(RHOKPReady)
     +-- agenticconsole.ReconcileAgenticConsoleUIResources()
     +-- alertsadapter.ReconcileAlertsAdapterResources()
        (opt-in via configMapRef; RemoveAlertsAdapter() when disabled; no ConfigMap validation;
         mount at /etc/alerts-adapter when CM exists)
     +-- appserver.ReconcileAppServerResources()
  6. reconcileDeploymentsAndStatus()  -- Phase 2: Deployments, Services, TLS certs, status (order matches code)
     +-- console.ReconcileConsoleUIDeploymentAndPlugin()
     +-- postgres.ReconcilePostgresDeployment()
     +-- otelcollector.ReconcileOtelCollectorDeployment()  -> OtelCollectorReady
     +-- ocpmcp.ReconcileDeployment()                      -> MCPServerReady (disabled: False/Disabled)
     +-- rhokp.ReconcileDeployment()                       -> RHOKPReady (or Disabled)
     +-- appserver.ReconcileAppServerDeployment()
     +-- agenticconsole.ReconcileAgenticConsoleUIDeploymentAndPlugin()
     +-- alertsadapter.ReconcileAlertsAdapterDeployment()  # when configMapRef set
     +-- agenticintegration.ReconcileAgenticIntegrationResources()  # last, Enabled agentic gate only (separate call after loop): handoff ConfigMap
     +-- checkDeploymentStatus() per deployment -> build newStatus
     +-- UpdateStatusCondition()
```

Phase 1 uses continue-on-error (reconciles all resources even if some fail).
Phase 2 uses fail-fast per step but collects status for all steps.

### Watcher-Triggered Restart Flow
```
External secret/configmap changes
  -> Watches() with custom predicate (shouldWatchSecret/shouldWatchConfigMap)
  -> SecretUpdateHandler.Update() / ConfigMapUpdateHandler.Update()
     -> Compare old vs new Data (DeepEqual)
     -> If changed: SecretWatcherFilter() / ConfigMapWatcherFilter()
        -> Match against SystemResources list (by name+namespace)
        -> OR match against WatcherAnnotationKey annotation
        -> For MCP CA inputs: enqueue reconciliation/validation before MCP roll; union other consumers
        -> Otherwise call restart function for each affected deployment (appserver, OTEL, MCP TLS, RHOKP, etc.)
           -> Set force-reload annotation with current timestamp
        -> If applicable, call TouchAgenticConfiguration() to update the handoff ConfigMap timestamp
```

## Key Abstractions

### Image Management
Default images are stored in a `defaultImages` map in `cmd/main.go` keyed by logical name (e.g., `"lightspeed-service"`, `"postgres-image"`, `"console-plugin"`, `"agentic-console-plugin"`, `"alerts-adapter"`, `"otel-collector"`, `"agentic-sandbox"`). Default values come from `internal/relatedimages/` which reads `related_images.json` at build time. Command-line flags override individual images (`--console-image`, `--agentic-console-image`, `--alerts-adapter-image`, `--otel-collector-image`, `--agentic-sandbox-image`, etc.). The map is passed to the reconciler via `OLSConfigReconcilerOptions` as individual named fields (e.g., `LightspeedServiceImage`, `ConsoleUIImage`, `AgenticConsoleUIImage`, `AlertsAdapterImage`, `OtelCollectorImage`, `AgenticSandboxImage`).

### WatcherConfig
Declarative configuration for external resource watching. Built in `cmd/main.go` and passed via `OLSConfigReconcilerOptions.WatcherConfig`. Contains:
- `Secrets.SystemResources`: Fixed list of system secrets with affected deployment names:
  - Telemetry pull secret → app server (`lightspeed-app-server`)
  - `lightspeed-console-plugin-cert` → chat console deployment
  - `lightspeed-agentic-console-plugin-cert` → agentic console deployment (`AgenticConsoleUIDeploymentName`)
  - Postgres TLS cert → postgres + app server
  - `lightspeed-otel-collector-cert` → OTEL Collector + app server + agentic ConfigMap
  - `openshift-mcp-server-tls` → OpenShift MCP server + app server + agentic ConfigMap; static SystemResources entry, gated by `OpenShiftMCPServerTLSWatchEnabled` when `spec.ols.introspectionEnabled` is true (absent means true)
  - `lightspeed-rhokp-tls` → RHOKP + app server + agentic ConfigMap; gated by `RHOKPTLSWatchEnabled` when `!byokRAGOnly`
  - These three serving-cert mappings dispatch independent callbacks: operand restart, `RestartAppServer` (refresh applicable client CAs then roll), and `TouchAgenticConfiguration` (Enabled agentic gate only). Refresh failure skips the app-server roll but does not prevent the separate touch. `RestartAppServer` itself does not touch the handoff.
- `ConfigMaps.SystemResources`: Fixed list of system configmaps (kube-root-ca.crt, service-ca bundle). Service-ca changes retain app-server/PostgreSQL targets and enqueue MCP CA validation whenever MCP is enabled, independently of NetObserv, without a direct handoff touch.
- `AnnotatedSecretMapping`: Dynamic map populated from CR spec at runtime (maps secret name to deployment names)
- `AnnotatedConfigMapMapping`: Dynamic map populated from CR spec at runtime (maps configmap name to deployment names)
All deployment names in `AffectedDeployments` are explicit (e.g. `lightspeed-app-server`, `lightspeed-rhokp`). MCP CA mappings union shared consumers, ignore disabled MCP-only stale annotations, and use distinct `mcp-ca` / `mcp-header-*` source tags so no CA-only reference becomes an app-server header mount. See [snapshot generation](config-generation.md#toolset-ca-reference-generation).

When the service-ca operator rotates or populates a watched TLS secret, `SecretUpdateHandler` restarts the mapped deployment via `RestartConsoleUI()` or `RestartAgenticConsoleUI()` (registered in `watchers/watchers.go`).

### Component Package Pattern
Each component (appserver, postgres, otelcollector, ocpmcp, agenticintegration, console, agenticconsole, alertsadapter) follows the same package structure:
- `reconciler.go`: Phase 1 (resources) and Phase 2 (deployment) entry points (agenticintegration is Phase 2-only handoff, no Deployment)
- `deployment.go`: Deployment spec generation and update detection (when applicable)
- `assets.go` and/or `config.go`: Resource and config generation
The packages receive `reconciler.Reconciler` interface, never import the controller package.

### Reconciler Interface (`internal/controller/reconciler/interface.go`)
Embeds `client.Client` and adds getter methods for:
- `GetScheme()`, `GetLogger()`, `GetNamespace()`
- Image getters: `GetAppServerImage()`, `GetPostgresImage()`, `GetConsoleUIImage()`, `GetAgenticConsoleImage()`, `GetAlertsAdapterImage()`, `GetOpenShiftMCPServerImage()`, `GetDataverseExporterImage()`, `GetOtelCollectorImage()`, `GetAgenticSandboxImage()`
- Version getters: `GetOpenShiftMajor()`, `GetOpenshiftMinor()`
- Config getters: `IsPrometheusAvailable()`, `GetWatcherConfig()`

`OLSConfigReconciler` implements the interface in `olsconfig_helpers.go`. Component packages call `r.GetAgenticConsoleImage()` when generating the agentic console deployment; the value comes from `OLSConfigReconcilerOptions.AgenticConsoleUIImage`, set in `cmd/main.go` from `--agentic-console-image` (with default from `defaultImages["agentic-console-plugin"]`).

### Finalizer Pattern
The OLSConfig CR uses finalizer `ols.openshift.io/finalizer` (defined in `utils.OLSConfigFinalizer`). On deletion:
1. Remove chat console UI (deactivate plugin, delete ConsolePlugin CR)
2. Remove agentic console UI (deactivate plugin, delete ConsolePlugin CR)
3. Remove alerts adapter operand resources (`alertsadapter.RemoveAlertsAdapter()`: deployment, namespaced RBAC, SA, NetworkPolicy, monitoring RoleBinding; AgenticRun ClusterRole/ClusterRoleBinding when the platform permits delete)
4. Remove OpenShift MCP server operand (`ocpmcp.Remove()`: Deployment, Service, NetworkPolicy, TOML/trust and legacy CA ConfigMaps, ServiceAccount, TLS Secret, ServiceMonitor; currently referenced user CAs protected; runtime/trust/legacy ConfigMap deletion OLSConfig-ownership-guarded even after ref removal; serving TLS Secret ownership checked via OLSConfig or originating OLSConfig-owned Service's matching name/UID before Service deletion), then remove RHOKP
5. List all owned resources via owner references
6. Explicitly delete owned resources
7. Wait up to 3 minutes for deletion (poll every 5 seconds)
8. Remove finalizer (proceeds even if cleanup times out)

## Integration Points

| Component | External Dependency | Mechanism |
|---|---|---|
| Manager cache | `openshift-config` namespace | Multi-namespace cache config for telemetry pull secret |
| Console image selection | OpenShift version | API call to `clusterversions.config.openshift.io` |
| Metrics TLS | `openshift-monitoring/metrics-client-ca` | ConfigMap read at startup |
| TLS profile | OLSConfig CR or API server | CR field or `apiservers.config.openshift.io` |
| Prometheus resources | Prometheus Operator CRDs | CRD existence check at startup; skips if unavailable |
| External secret watching | User-provided LLM secrets, MCP header secrets | Annotation-based (`watchers.openshift.io/watch`) |
| External configmap watching | Additional CA, proxy CA configmaps | Annotation-based (`watchers.openshift.io/watch`) |

## Testing

### Unit Tests

Unit tests are co-located with source files (`*_test.go`). They use envtest (a local Kubernetes API server) with Ginkgo v2/Gomega. `make test` is required instead of `go test` because the Makefile handles envtest binary download, CRD installation, and build flags.

OLS-2715 typed API/TOML/security and CA snapshot/lifecycle tests pass in the full `make test` suite with default Kubernetes 1.27.1 envtest. Separate [local pinned-image runtime verification](../what/ocpmcp.md#runtime-verification) now passes actual `GenerateConfigTOML` default/empty/seven inputs (22/0/39 tools, mock-discovery-dependent), table/security invariants, static Prometheus/Loki/Tempo HTTPS/custom trust and all six negative CA/hostname cases. First-harness scoped shared service/custom and REST CAData-only metric roots, Alertmanager service root, singular OSSM/NetObserv CA, restart-based rotation/removal and six resource get/list denials with allowed Pod list positive also passed. Both auth modes use caller token when present via derived REST config; missing-header header mode sends anonymous requests and kubeconfig mode uses the supplied synthetic kubeconfig token, not operator identity or real authentication/RBAC proof. Image label `1.0.0` is provenance only (`--version` blank, `serverInfo.version` empty). Separate [full-manager live cluster verification](../what/ocpmcp.md#full-manager-live-cluster-verification) passed 93/93 unique assertions and 30/30 Strict server dry-run checks on CRC OCP 4.22.14 / Kubernetes 1.35.6 using actual production `bin/manager`, not a scoped harness. Full `SetupWithManager` CA watches/public projection/checksums, real Service CA injection, content/reference-identity PodUID rolls, invalid-source retention/recovery, twice disable after ready-condition erasure, bounded real Kubernetes caller RBAC and namespace HTTPS mock metric trust/removal/rotation passed. Five deployed operands reached Ready in local-dev mode (operator ServiceMonitor/metrics reader skipped), not production-deployment proof. Normal finalizer cleanup preserved user CAs/restored console plugins; main subsequently stopped the manager and removed all test-created cluster resources with explicit approval. [PLANNED: OLS-2715] Live authenticated Prometheus/Loki/Tempo backend auth/RBAC, Route endpoint discovery/real stacks, other tool/provider/prompt/diagnostic helper image/kernel/RBAC/SCC prerequisites and external public-root HTTPS remain unverified. Local synthetic logs/traces/auth evidence is separate; no hot-reload, atomic rollout or all-tool/shipping-complete claim. See live observations for recovered mount/order transients and extra metadata-RV recovery rolls; no source fixes were made.

### E2E Tests

E2E tests live in `test/e2e/` and run against a real OpenShift cluster with the operator deployed.

**Framework:** Ginkgo v2 with Gomega. All suites use `Ordered` for serial execution. Tests prone to transient failures use `FlakeAttempts(5)`.

**Suite setup** (`suite_test.go` `BeforeSuite`):
- Registers OLSConfig API, creates Kubernetes client
- Waits for operator deployment to be ready
- Creates LLM provider credential secrets (from `LLM_TOKEN` env var)
- `AfterSuite` runs `oc adm must-gather` for diagnostics and cleans up secrets

**Test suites by area:**

| File | Area | What it validates |
|---|---|---|
| `reconciliation_test.go` | Reconciliation | Deployment creation, config changes (log level, model, secrets) trigger updates, CA certificate volume mounting |
| `autocorrection_test.go` | Auto-correction | Operator restores manually modified deployments, services, ConsolePlugin CRs, ConfigMaps |
| `tls_test.go` | TLS & RBAC | Service TLS activation, HTTPS endpoints, authorized vs unauthorized access (metrics, query API) |
| `proxy_test.go` | Proxy | Queries succeed through squid proxy with TLS |
| `database_test.go` | Database persistence | Conversation records survive postgres pod restart via PVC |
| `postgres_restart_test.go` | Postgres recovery | Operator restores postgres after scale-to-zero, queries resume |
| `metrics_test.go` | Prometheus metrics | Operator metrics scraped by Prometheus, reconcile metrics available |
| `byok_test.go` | BYOK RAG | Custom RAG image used, ByokRAGOnly prevents OCP docs fallback, image update propagation |
| `byok_auth_test.go` | BYOK auth | Authenticated registry access with pull secrets |
| `all_features_test.go` | All features combined | 2 replicas, multiple providers, quotas, MCP servers, tool filtering, proxy, BYOK, data collector -- all enabled simultaneously |
| `upgrade_test.go` | Operator upgrade | CR persists and queries continue after operator bundle upgrade |
| `rapidast_test.go` | Security scanning | Route creation for OWASP ZAP / RapiDAST scanning |

**Test pattern:** Each suite creates its own OLSConfig CR in `BeforeAll`, runs ordered tests, then calls `mustGather()` and `DeleteAndWait()` in `AfterAll`. Port forwarding provides local HTTPS access to in-cluster services.

**Supporting files:**

| File | Purpose |
|---|---|
| `constants.go` | Namespace, deployment names, ports, LLM env var names, test CA certificate |
| `assets.go` | OLSConfig CR generation helpers (`generateBaseOLSConfig()`, `generateAllFeaturesOLSConfig()`) |
| `client.go` | Kubernetes client wrapper with wait/poll helpers, port forwarding, image registry operations, storage class management |
| `utils.go` | `OLSTestEnvironment` setup/teardown, HTTPS query helpers, must-gather, route creation |
| `http_client.go` | HTTPS client with custom CA, polling GET/POST helpers |
| `prometheus_client.go` | Prometheus query wrapper via thanos-querier route |

**How to run:**

| Command | Scope | Timeout |
|---|---|---|
| `make test-e2e` | Standard tests (excludes AllFeatures, Upgrade, Rapidast) | 2h |
| `make test-e2e-local` | Local tests (excludes Database-Persistency, Rapidast) | 2h |
| `make test-e2e-all-features` | Comprehensive all-features test | 3h |
| `make test-upgrade` | Upgrade scenario only (requires `BUNDLE_IMAGE`) | 2h |

**Required environment variables:**

| Variable | Required | Description |
|---|---|---|
| `KUBECONFIG` | Yes | Path to cluster kubeconfig |
| `LLM_TOKEN` | Yes | API token for LLM provider |
| `LLM_PROVIDER` | No | Provider name (default: `openai`) |
| `LLM_MODEL` | No | Model name (default: `gpt-4o-mini`) |
| `BUNDLE_IMAGE` | For upgrade | Operator bundle image for upgrade test |
| `CONDITION_TIMEOUT` | No | Custom timeout in seconds for condition checks |
| `ARTIFACT_DIR` | No | Directory for must-gather diagnostics output |

## Local Development

`make run` sets `LOCAL_DEV_MODE=true` and runs the operator on the host against the cluster kubeconfig.

| Behavior | When `LOCAL_DEV_MODE=true` |
|---|---|
| Operator ServiceMonitor | Skipped in `reconcileOperatorResources()` |
| App-server metrics reader secret | Skipped in `appserver.reconcileMetricsReaderSecret()` |
| App-server ServiceMonitor / PrometheusRule | Still reconciled if Prometheus Operator CRDs exist |

Skipping metrics reader secret reconciliation avoids a local reconcile loop: creating the token secret triggers `Owns(Secret)` and immediate requeue.

`make run` also runs `dev-setup` (namespace, metrics RBAC, user-access). Image overrides: `--console-image`, `--agentic-console-image`, and other flags in `cmd/main.go`.

## Implementation Notes

- The operator uses kubebuilder v3 markers for CRD generation and RBAC.
- The `cmd/check-isa-level/` package is a build-time utility for AMD64 ISA level checking.
- All generated files (deepcopy, CRD YAML) should be regenerated after API type changes using `make generate manifests`.
- The OLSConfig CRD is cluster-scoped and validated to require `.metadata.name == "cluster"`.
- `SetupWithManager()` registers `Owns()` watches for: Deployment, ServiceAccount, ClusterRole, ClusterRoleBinding, Service, ConfigMap, Secret, PersistentVolumeClaim, ConsolePlugin, ServiceMonitor, PrometheusRule, ImageStream.
- Controller-runtime handles retry with exponential backoff; the operator does not use periodic reconciliation.
