# OpenShift MCP Server (ocp-mcp)

Standalone HTTPS OpenShift MCP server operand managed by the `ocpmcp` package ([OLS-3526](https://redhat.atlassian.net/browse/OLS-3526)). Replaces the former app-server sidecar. Related: [OLS-3684](https://redhat.atlassian.net/browse/OLS-3684) (agentic handoff MCP keys/CA), [OLS-3594](https://redhat.atlassian.net/browse/OLS-3594) (deferred agentic auto-injection).

## Architecture

```text
lightspeed-service (app-server)
  └─ HTTPS MCP client
       url: https://openshift-mcp-server.<ns>.svc:8443/mcp
       trust: Secret lightspeed-mcp-client-ca → /etc/certs/openshift-mcp-server-ca/service-ca.crt
            │  (PEM from openshift-service-ca.crt; same cluster CA as OTEL)
            ▼
openshift-mcp-server Deployment + ClusterIP Service (:8443)
  ├─ service-ca serving cert Secret  openshift-mcp-server-tls
  ├─ TOML ConfigMap                   openshift-mcp-server-config
  └─ validated public CA snapshot     openshift-mcp-server-trust
```

Gated by `spec.ols.introspectionEnabled` (default `true` when absent). When false, the operator sets `MCPServerReady=False`, `Reason=Disabled` without blocking overall readiness. Disable cleanup is always attempted idempotently, independent of readiness-condition history.

## Behavioral Rules

### Activation
1. When `spec.ols.introspectionEnabled` is true (or absent), Phase 1 and Phase 2 reconcile the standalone MCP operand.
2. When false, Phase 1 always attempts idempotent managed MCP removal, including when `MCPServerReady` is absent or already Disabled. This fixes leaked resources when a validation failure erased readiness-condition history; other components retain their existing conditional cleanup rules. Phase 2 skips deployment reconciliation and records `MCPServerReady=False`, `Reason=Disabled`.

### Phase 1 Resources
3. Validate all CA inputs and reconcile operator-owned public snapshot `openshift-mcp-server-trust` before ConfigMap `openshift-mcp-server-config` (typed TOML selection/configuration, denied Secret/RBAC resources, metrics endpoints), ServiceAccount and NetworkPolicy. Invalid inputs preserve the last validated snapshot/runtime config. See [generation](../how/config-generation.md#toolset-ca-reference-generation).
4. ServiceAccount `openshift-mcp-server` — no RBAC bindings; callers pass their own token (app-server config uses the runtime placeholder `Authorization: kubernetes`).
5. NetworkPolicy `openshift-mcp-server` — allows TCP `:8443` ingress from any pod in the operator namespace and from cluster Prometheus pods in `openshift-monitoring`. The Prometheus peer requires both the namespace label `kubernetes.io/metadata.name: openshift-monitoring` and pod labels `app.kubernetes.io/name: prometheus` and `prometheus: k8s` (OLS-3943); it does not allow every pod in the monitoring namespace. The policy remains ingress-only and is deleted by the idempotent disable cleanup in rule 2.

### Phase 2 Resources
6. Service `openshift-mcp-server` — ClusterIP, port `https` `:8443`, serving-cert annotation → Secret `openshift-mcp-server-tls`.
7. Wait for TLS Secret keys `tls.crt` / `tls.key` before creating/updating the Deployment.
8. Deployment `openshift-mcp-server` — starts with `--config` pointing to mounted TOML that configures HTTPS serving certificates; probes use HTTPS `/healthz`. Image comes from `--openshift-mcp-server-image`, with `PullIfNotPresent`. Replicas/resources/tolerations/nodeSelector from `spec.ols.deployment.mcpServer` (`Config`).

### App-server Integration
9. olsconfig `mcp_servers` includes an `openshift` entry pointing at `https://openshift-mcp-server.<namespace>.svc:8443/mcp` with the runtime placeholder `Authorization: kubernetes` when introspection is enabled. See [app-server.md](app-server.md) and [deployment-generation.md](../how/deployment-generation.md).
10. App-server mounts classic client CA Secret `lightspeed-mcp-client-ca` (sourced from `openshift-service-ca.crt`) at `/etc/certs/openshift-mcp-server-ca/`, projecting `mcp-ca.crt` as `service-ca.crt`, and adds that file to `extra_ca`. Agentic trust uses a distinct Secret, `lightspeed-agentic-mcp-ca`. See [tls.md](tls.md) and [agentic-sandbox-profile.md](agentic-sandbox-profile.md). There is no dedicated MCP inject-cabundle ConfigMap; the public backend-trust snapshot is separate from these client CAs. Enabled Phase 1 / `Remove` deletes leftover `openshift-mcp-server-ca` on upgrade only when OLSConfig-owned and not currently referenced as a user CA; unowned collisions remain protected even after reference removal.
11. App-server Deployment does not track an MCP CA hash or MCP TOML ConfigMap ResourceVersion.

### Watching and Restarts
12. Secret `openshift-mcp-server-tls` is listed statically in `WatcherConfig.Secrets.SystemResources`. Watching is gated by `OpenShiftMCPServerTLSWatchEnabled` (`syncOpenShiftMCPServerTLSWatcher`), set from `introspectionEnabled`, so enable/disable does not rewrite the SystemResources slice under the informer.
13. On TLS Secret data change, the watcher independently restarts MCP and the app-server. A separate handoff touch runs only when the agentic gate is Enabled. App-server restart refreshes applicable client CA Secrets before rolling; refresh failure skips its roll, but does not stop the separate handoff callback. See [agentic-sandbox-profile.md](agentic-sandbox-profile.md).
14. ConfigMap `openshift-service-ca.crt` changes restart app-server (refreshing applicable client CAs) and PostgreSQL, and always enqueue MCP CA validation while introspection is enabled, independently of NetObserv. No direct handoff touch. User CA events also queue validation before MCP can roll, retaining all shared-source consumers and ignoring disabled MCP-only stale annotations; they never create accidental app-server header CA mounts.
15. MCP tracks TOML ConfigMap/TLS Secret ResourceVersions and snapshot content/reference-identity hash. Phase 2 requires a matching persisted validated snapshot; invalid sources retain the last snapshot and suppress MCP roll. Trust mounts are snapshot-only; see [TLS](tls.md#shared-mcp-process-trust).

### Security
16. TOML denies `core/v1` `Secret` and all `rbac.authorization.k8s.io/v1` resources so Secret/RBAC data cannot reach the LLM via the shipped server.
17. Omitted selection defaults to `core`, `config`, `helm`, `observability/metrics`, `kubevirt`; explicit typed selection replaces it. Every form pins `read_only = false`, `disable_destructive = true` and target compatibility filters. The omitted-selection metrics table uses in-cluster Thanos Querier and Alertmanager URLs. Its `guardrails = "!tsdb"` (PromQL query safety, not RBAC) follows upstream OpenShift guidance when Thanos lacks the TSDB status API; header-mode auth uses the caller's bearer token when supplied; missing-header behavior is documented in [authentication observations](#authentication-observations).
18. User-defined MCP servers (`spec.mcpServers`) are out of scope for this operand.

### Toolset Selection

- `spec.ols.mcpKubeServerConfig.toolsets` implements 17 typed singleton-key selections. Schema/CEL validate bounded lists, uniqueness and supported settings; Strict requests are required for unknown typed fields or mixed recognized/unknown keys because Kubernetes otherwise prunes them. Empty-only maps reject populated blocks even non-Strict. See [crd-api.md](crd-api.md#typed-toolset-selection).
- Omission preserves the current operator selection in rule 17. Explicit configuration replaces it: enable exactly the declared toolsets, without adding operator defaults or implicit dependencies. An explicit empty list selects no toolsets.
- Empty selection does not disable introspection or remove the MCP operand/client/handoff wiring; `introspectionEnabled` remains the independent lifecycle gate.
- `openshift/mustgather` is an explicit OLS-2715 exclusion. Reject its selection with a clear unsupported-toolset validation error. Offline archive analysis needs a separate product requirement and storage, ingestion/freshness, retention and archive-access design; live Kubernetes authorization/resource denial does not filter archived data. Do not add archive/PVC fields or mounts in this change. The bundled planning prompt is not independently enabled through this excluded toolset.
- Keep `read_only=false` and `disable_destructive=true` for all selection forms. Do not introduce security controls or change existing resource restrictions, compatibility filtering, disabled tools or caller authorization. Valid selections remain accepted even when filters/prerequisites leave zero usable tools; document those runtime limitations and refine them after exercising the integration, rather than block OLS-2715 or silently relax safeguards.
- OSSM/NetObserv explicit CA configuration uses local ConfigMap-or-Secret `caBundleRef` with required name/key, read-only projection, reconciliation validation and rollout on valid bundle changes. References remain user-owned; only canonical public PEM is copied into the operator-owned snapshot and projected, never live user objects. See [crd-api.md](crd-api.md#toolset-ca-bundle-reference) and [tls.md](tls.md#mcp-toolset-backend-ca-bundles).

- OSSM/NetObserv generation accepts HTTPS-only endpoints and enables verification; no `insecure` option. Pinned-image local HTTPS calls passed using singular OSSM/NetObserv CA files with their custom root absent from shared trust; this local case does not verify source projection or backend authorization. The separate [full-manager live run](#full-manager-live-cluster-verification) verifies selected public CA projection, not real OSSM/NetObserv backend semantics or authorization. OSSM requires URL and CA reference. NetObserv selects either an explicit URL plus CA reference or service namespace/name/port with an optional CA override (cluster service CA otherwise). Reject mixed endpoint forms; service defaults are computed only for that form. See [typed blocks](crd-api.md#ossm-configuration).

- Explicit Helm selection uses ConfigMap-backed release storage by default and rejects Secret storage while OLS Secret denial remains mandatory. Omitted `toolsets` retains the existing behavior. See [Helm fields](crd-api.md#helm-configuration).
- An explicit empty metrics block uses the OLS Thanos/Alertmanager endpoints and `!tsdb`. An explicit Prometheus URL defaults guardrails to `all` unless overridden. Typed authentication, response and cardinality settings are specified in [metrics fields](crd-api.md#observability-metrics-configuration); both metrics endpoints require verified HTTPS with no insecure option; shared MCP trust is defined below.

- Logs/traces use HTTPS-only static endpoints or route-based discovery by default, with no insecure option or Service/HTTP fallback. Reject `useRoute: false` and static URL plus an explicit discovery setting; keep per-call selectors and compatibility filtering independent. Backend trust uses the shared MCP-process contract below. See [endpoint contracts](crd-api.md#observability-logs-and-traces-endpoint-configuration).

- All three observability backend blocks expose `authMode` with default `header` and enum `header` / `kubeconfig`. No independent credentials are added. Runtime verification shows both modes use the caller token when a bearer header exists because `Manager.Derived` replaces REST `BearerToken`; without that header, `header` sends an anonymous backend request and `kubeconfig` uses the supplied kubeconfig token. These upstream wrinkles do not grant operator identity or change global authentication policy; permissive mocks do not verify real backend authorization.

- `spec.ols.mcpKubeServerConfig.caBundleRefs` declares user ConfigMap/Secret CA bundles augmenting image/system roots and cluster service-CA baseline trust. Omitted/empty leaves baseline trust. This is shared across system-root consumers, not endpoint isolation or toolset selection; preserve separate OSSM/NetObserv client CA references. Validate bundles and roll MCP on valid changes without adopting/deleting sources. Local pinned-image shared-root composition/restart tests and separate full-manager live watch/projection/rollout, backend root removal/rotation and real Service CA baseline tests passed; external public-root HTTPS remains unverified. See [runtime verification](#runtime-verification) and [TLS contract](tls.md#shared-mcp-process-trust).

- CNI diagnostics exposes optional validated `kernelDebugImage`, `tcpdumpImage` and `pwruImage`, retaining pinned upstream helper-image defaults for absent fields. `cni-diagnostics: {}` is valid. Overrides enable administrator-managed mirrors; selection does not grant privileges, install binaries or change restricted MCP pod security. See [CNI fields](crd-api.md#cni-diagnostics-configuration).

- The ten [configuration-free selections](crd-api.md#configuration-free-toolsets) accept `{}` only. Selection does not install workloads/providers, enable prompt consumption or guarantee exposed tools. `cluster-diagnostics` is selectable despite its sole tool being filtered under the retained policy.

### Monitoring
19. ServiceMonitor `openshift-mcp-server-monitor` (OLS-3728) — scrapes MCP server metrics via HTTPS on port 8443, path `/metrics` (Go promhttp). Server TLS only (service-ca CA bundle + `serverName`; no client certs / Bearer token), 30s interval. Reconciled in Phase 2 via `utils.ReconcileServiceMonitor()`. Skipped if Prometheus Operator CRDs are not installed. The NetworkPolicy ingress in rule 5 admits the cluster Prometheus scrape (OLS-3943); the ServiceMonitor alone does not grant network access.

### Finalizer
20. On CR deletion, `ocpmcp.Remove()` idempotently deletes Deployment, Service, NetworkPolicy, runtime/trust ConfigMaps, ServiceAccount, TLS Secret (`openshift-mcp-server-tls`), ServiceMonitor (`openshift-mcp-server-monitor`) and legacy CA ConfigMap (`openshift-mcp-server-ca`) before owned-resource sweep. Cleanup never deletes a currently referenced user CA, even a reserved-name collision, and runtime/trust/legacy ConfigMap deletion requires OLSConfig ownership even after reference removal. Serving TLS Secret deletion requires OLSConfig ownership or a v1 Service owner reference with the originating OLSConfig-owned Service's matching name/UID; this check runs before deleting that Service. Unowned outputs are preserved, not inferred to be owned from reserved names. Enabled reconciliation rejects CA refs to these runtime/trust/legacy output ConfigMaps or serving TLS Secret. Appserver-owned client CA Secrets are handled by the sweep.

## Configuration Surface

| Field path | Description |
|---|---|
| `spec.ols.introspectionEnabled` | Enable/disable standalone MCP (`*bool`, default true) |
| `spec.ols.mcpKubeServerConfig.timeout` | Timeout seconds for the built-in openshift MCP entry in olsconfig |
| `spec.ols.mcpKubeServerConfig.toolsets` | Typed singleton-key replacement selection; omitted defaults, empty selects none |
| `spec.ols.mcpKubeServerConfig.caBundleRefs` | User bundles augmenting shared MCP-process baseline trust |
| `spec.ols.deployment.mcpServer` | Standalone MCP `Config` (replicas, resources, tolerations, nodeSelector) |
| `--openshift-mcp-server-image` | MCP container image override |

## Constraints

1. Multi-replica is allowed; Streamable HTTP is configured for stateless operation upstream.
2. The MCP ServiceAccount has no cluster RBAC; authorization uses the calling user's token.
3. The `openshift-mcp-server` image is shipped by the OCP MCP team from `registry.redhat.io/openshift-mcp/openshift-mcp-server-rhel9`. OLS does not build or release this image. Digest/tag updates track the OCP MCP team's releases; bump `related_images.json` and regenerate the bundle when a new release is available.
4. Agentic/sandbox reuse of the MCP Service URL is published in the handoff ConfigMap; the distinct agentic MCP client CA Secret is owned by appserver when the agentic gate and introspection are enabled — see [agentic-sandbox-profile.md](agentic-sandbox-profile.md). Optional auto-injection into agent runs remains deferred (OLS-3594).

## Runtime Verification

### Identity and Evidence

The registry authorization blocker is resolved: the exact pinned image was pulled successfully and executed locally. Local evidence is recorded in `/tmp/ocpmcp-research/runtime-verify/REPORT.md` (including its serializer extension) and `serializer-check-summary.json` (**PASS, 112 assertions**). These are disposable local artifacts, not repository dependencies. The extension supersedes the first harness's generated-equivalent TOML and unexercised Loki/Tempo limitations only for the scenarios recorded below; it does not establish shipping completion.

| Identity | Observed value / limit |
|---|---|
| Executed image | `registry.redhat.io/openshift-mcp/openshift-mcp-server-rhel9@sha256:a551fd58f7b2a7505a76ba2109ae5a8f031606605490c9c68d60f91f502f6fe5` |
| Local image config ID | `e25aa25e21043b7ba25bd2f4777aeeea471e510be960f2b22d1191c833abad83` |
| Labels | version `1.0.0`, release `1789004305`, architecture `x86_64`, revision/vcs-ref `495310209578a754f3383016486fa8f611de7452` |
| Source pins | [OpenShift source](https://github.com/openshift/openshift-mcp-server/tree/495310209578a754f3383016486fa8f611de7452); vendored `github.com/rhobs/obs-mcp v0.7.1` |
| Runtime version caveat | CLI `--version` exits zero with a blank line; MCP `serverInfo.version` is empty. Labels are provenance only, not independent version/source-to-binary attestation. |
| User | Image default `65532:65532`; no user override or privileged options |

### Verification Matrix

This matrix covers the earlier local-container run only; the separate [full-manager live cluster results](#full-manager-live-cluster-verification) follow below. All backend/API fixtures in this matrix are local synthetic HTTPS mocks, not real cluster services. Tool counts depend on mock discovery and compatibility filtering, not universal deployment counts. Static Loki/Tempo URLs drive the calls; advertised LokiStack/TempoStack CRDs enable tools but do not prove Route endpoint discovery or real stack integration.

| Scenario | Passed scope | Boundary / remaining work |
|---|---|---|
| Actual `GenerateConfigTOML` input `null` | Explicit operator defaults; startup/initialize and tools/list: **22 tools**; actual Prometheus query succeeds | KubeVirt absent from mock discovery; upstream TOML omission instead yielded **11**, core-only tools in the first harness, not operator omission parity |
| Actual input `{"toolsets":[]}` | Startup/initialize succeeds, **0 tools** | MCP protocol/operand remains enabled |
| Actual seven typed selections | OSSM (`kiali`), NetObserv, Helm, metrics, logs, traces, CNI tables accepted; **39 tools** with LokiStack/TempoStack discovery | First harness had 30 before that discovery; not all 39 tools functionally exercised |
| Serialized table/security invariants | Helm ConfigMap storage; all three CNI image defaults; singular CA paths; explicit insecure=false; metrics endpoints/guardrails and both auth modes; `read_only=false`, `disable_destructive=true`, compatibility filtering and both denied-resource entries preserved | Dummy CA object refs verify serializer path mapping, not admission/source existence/PEM validation/projection; CNI listed, never invoked |
| Actual serializer Prometheus/Loki/Tempo static HTTPS calls | Shared custom trust with unrelated populated REST CAData: Prometheus GET `/api/v1/label/__name__/values` + POST `/api/v1/query`; Loki GET `/loki/api/v1/query_range`; Tempo GET `/api/search`; functional synthetic vector/log/trace outputs | Read-only queries only; real backends/auth/RBAC and other tools unverified |
| All three observability clients: unknown CA / wrong hostname | All **six** calls fail with unknown authority or missing matching IP SAN; **zero backend HTTP requests**, failing before HTTP/auth capture | No insecure fallback observed in these cases |
| First harness: shared service/custom roots | Actual Prometheus HTTPS calls accept either root despite unrelated REST CAData; actual Alertmanager GET `/api/v2/alerts` accepts shared service root | Synthetic service root is not live OpenShift Service CA injection; Alertmanager was not re-proved by the serializer extension |
| First harness: REST CAData-only root | Actual Prometheus query accepts root present only in REST CAData, not shared/toolset mounts | Metric-client scope, not universal client trust |
| First harness: root removal/rotation | After stop/recreate: removed custom root rejects old endpoint; rotated root accepts new leaf and rejects old leaf | Restart-based acceptance/rejection only, **no hot-reload claim** or controller rollout proof |
| First harness: singular OSSM/NetObserv CA | Custom root absent from shared directory; POST `/api/chat/mcp/get_mesh_status` and GET `/api/loki/flow/records` succeed via `/etc/mcp-server/toolset-ca/{ossm,netobserv}/ca.crt` | Generic fixtures verify requests/TLS, not real service semantics or shared-root consumption |
| Image/system-root preservation | Exact `SSL_CERT_DIR=/etc/ssl/certs:/etc/pki/tls/certs:/etc/mcp-server/ca`; `SSL_CERT_FILE` unset; image `/etc/pki/tls/certs/ca-bundle.crt` retains **146 certificates**; no image certificate paths overmounted | **External public-root HTTPS not exercised** |
| First harness: resource denial | Secret, Role, ClusterRole get/list: all **six** rejected `resource not allowed`, **zero mock API requests**; allowed Pod list reaches mock API | Local resource filter, not real Kubernetes RBAC. No write/diagnostic tool invoked; no advertised destructiveHint=true, but `helm_install`/`pods_run` remain non-read-only |
| Cleanup | Containers removed and ports 18443, 18444, 19443–19448 closed | Cached image and synthetic artifacts remain under `/tmp` |

### Full-manager Live Cluster Verification

Evidence: fully read `/tmp/ols2715-cluster/LIVE-REPORT.md` and `run-summary.json`, with per-case details in `live-results-unique.json` and admission diagnostics in `admission-results.json`. These are disposable evidence artifacts, not repository dependencies. Main's subsequent cleanup facts below supersede the report's earlier handoff statements that the manager and cluster resources were still running/intact; cleanup is additional evidence, not part of the assertion counts.

**93/93 unique live assertions and 30/30 admission checks passed.** The actual full production controller binary `./bin/manager` (PID `363031`), not the preparation/scoped harness, ran against CRC on **OpenShift 4.22.14 / Kubernetes 1.35.6**. Production root reconciliation, full `SetupWithManager()` event registration, annotation/predicate routing, deployment generation and status updates were exercised. The manager ran in **local development mode**: operator ServiceMonitor and app-server metrics reader secret reconciliation were skipped. This is real-cluster/full-controller evidence, **not** an OLM/production-deployment or operator metrics-scraping proof.

Before final cleanup, the singleton spec was restored exactly to `live-start-cr.json`: introspection enabled, `toolsets: []`, `byokRAGOnly: true`, collection/audit disabled. OverallStatus was Ready and all five deployed operands' current-generation conditions were True: MCPServerReady, ApiReady, CacheReady, OtelCollectorReady and ConsolePluginReady. RHOKP and agentic operands were not covered by this readiness claim. The report verified 485 repository file hashes unchanged during the live run; this later update changes specs only.

| Live scenario | Passed scope | Boundary |
|---|---|---|
| Selection and transport | TLS-verified initialize/tools/list; explicit empty exposes zero tools, omission emits legacy defaults with actual tools, core replaces defaults, empty roundtrip returns zero | Counts remain discovery/filter-dependent. Port-forward used SNI and leaf/chain validation for `openshift-mcp-server.openshift-lightspeed.svc`, but `Host: localhost:18443` to avoid upstream Host protection; verification was not disabled |
| Admission | 30/30 Strict server dry-run checks: 25 configuration-specific invalid rejections and five valid-configuration controls | Separate name `ols-2715-test-admission` violates the generated CRD's singleton `metadata.name == cluster` rule. Valid controls assert **only** that wrong-name diagnostic, not successful admission/create; no second CR persisted |
| Real Service CA and public projections | Real Service CA injection; mixed selected ConfigMap/Secret public certificates, snapshot owner UID, stable snapshot UID until disable, actual mounted certificate checksums and SSL_CERT_DIR; private/unselected source keys not projected | Operand's own serving TLS key is a separate expected mount. No genuine private keys or tokens are included in documentation/evidence output |
| Full-manager CA events | Selected CM/Secret updates roll hash and ready Pod UID; same-certificate name/key identity changes also roll; unselected updates do not; selected CM binaryData works; missing-source creation recovers | NetObserv default dedicated path uses baseline Service CA, explicit CA overrides it, deselection keeps baseline. These projections do not prove real OSSM/NetObserv backend operation |
| Invalid-source safety and recovery | Invalid PEM/mixed malformed bundles, missing selected Secret key and deleted CM retain last-valid snapshot and old ready Pod UID with ResourceReconciliation=False/Failed; changed-valid recovery rolls; same-cert missing-key repair restores Ready without CA roll | Reserved runtime/trust/legacy ConfigMap and serving TLS Secret refs rejected with invalid-state snapshot/pod preserved; recovery may still roll on metadata ResourceVersion, as noted below |
| Disable without status history | Twice disabled after invalid CA input erased component-ready conditions; removed Deployment, Service, SA, NetworkPolicy, ServiceMonitor, serving TLS Secret, runtime ConfigMap and trust snapshot; MCPServerReady=False/Disabled | User CA UIDs and lack of ownerReferences preserved through disable/ref removal; fix/re-enable restored Ready with new snapshot UID |
| Actual namespace HTTPS backend | Restricted-v2 UBI9 Python HTTPS test backend; actual Prometheus-compatible metric query returns **2715** using custom shared root; root removal gives unknown-authority rejection with no additional backend HTTP requests; restore succeeds | Backend is synthetic, not authenticated production Prometheus; HTTP counters record paths only, not authorization headers |
| Backend trust rotation and baseline | Backend leaf/root rotation fails TLS before new root with no new backend HTTP requests; public source update triggers MCP hash/PodUID rollout and successful query; switching backend to real Service CA serving Secret succeeds with shared refs removed and baseline-only trust | Backend was explicitly restarted for rotation; MCP trust refresh is controller rollout, not hot reload |
| Real caller RBAC and resource policy | Real caller-token Pod `resources_get` succeeds despite operand SA having no Pod-read RBAC; Secret, Role and RoleBinding return `resource not allowed`; allowed ConfigMap request instead returns Kubernetes caller-RBAC `is forbidden` | No live API audit/request tracing: **do not claim zero upstream API requests** for these live resource denials. The earlier local mock's zero-request proof is separate |
| Pod security | MCP operand uses `restricted-v2` SCC and non-root UID | No CNI/privileged diagnostic tool calls; public mount/checksum `oc exec` inspection only. Restricted MCP readiness does not prove helper/debug workload SCC functionality |

#### Observed Transients and Invariant Limits

- Pruning obsolete `netobserv.crt` / `shared-000.crt` snapshot keys caused transient FailedMount warnings on old pod templates. During second disable/re-enable, an appserver pod briefly requested deleted `lightspeed-mcp-client-ca`; transient failing-pod reconciliation errors recovered. Final readiness passed. This bounded run does not guarantee seamless/atomic multi-resource publication or rollout ordering.
- Reserved-output references can be annotated **before** validation rejects them (observed on the runtime ConfigMap). Metadata ResourceVersion changes can cause an extra recovery rollout despite unchanged trust data/hash. The safety invariant is last-valid snapshot and unchanged ready pod **during invalid input**, not an unconditional no-data-change/no-recovery-roll guarantee.
- These were accepted as documented wrinkles; **no source fix was made**. See `warning-event-summary.json`, `controller-error-summary.json` and the report's archived first-attempt diagnostics. Environment/protocol assertion adjustments were test-harness corrections, not production changes.

#### Finalizer and Subsequent Main Cleanup

Main's `oc delete OLSConfig cluster` succeeded through the **normal finalizer in approximately two seconds**, without forced finalizer removal. The post-finalizer namespace inventory retained only user CA/test-backend resources; all user CAs were preserved by finalization. Console plugins/activation were restored exactly to the before list, `networking-console-plugin`, `monitoring-plugin`, with no Lightspeed plugin. The owned global app-server SubjectAccessReview role/binding were gone; only the worker's test discovery ClusterRole/ClusterRoleBinding remained, and main explicitly deleted both.

Main then stopped full manager PID `363031` with SIGTERM, deleted `openshift-lightspeed`, and deleted the generated CRD `olsconfigs.ols.openshift.io`. That **separate, explicitly user-approved namespace deletion** removed the remaining test/user-source objects; it is not operator adoption or finalizer deletion of user CAs. Before/after lists showed no leftover test-created ClusterRoles/ClusterRoleBindings or ConsolePlugins. The local caller-token file was deleted. All cluster resources created for this run were cleaned; this supersedes the report's worker handoff/manager-running snapshot. Private synthetic key artifacts are not publishable evidence.

### Authentication Observations

The earlier local-container serializer extension verified the following independently for metrics, logs and traces (18 tools with the three observability selections). Evidence stores authorization classification booleans only, not token/header values.

| `authMode` | Caller bearer header | Observed backend identity |
|---|---|---|
| `header` | Present | Caller token |
| `kubeconfig` | Present | **Also caller token**, not static kubeconfig identity |
| `header` | Absent | **Anonymous request**, accepted by permissive mock |
| `kubeconfig` | Absent | Supplied synthetic kubeconfig token |

Pinned [Manager.Derived](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/pkg/kubernetes/manager.go) copies TLS verification settings and replaces REST `BearerToken` with a supplied caller bearer token; vendored [token sourcing](https://github.com/openshift/openshift-mcp-server/blob/495310209578a754f3383016486fa8f611de7452/vendor/github.com/rhobs/obs-mcp/pkg/auth/token.go) reads that derived config for kubeconfig mode. Missing-header header mode neither rejects locally nor falls back to REST credentials. No TokenReview or SubjectAccessReview occurred, and generated configuration does not opt into `require_oauth`. The mock accepts any credentials/anonymous requests: PASS is proof of observed sourcing, **not** operator-identity grants, mandatory/global authentication, real token validation or backend/Kubernetes RBAC from that local case. The separate live run above proves bounded real Kubernetes caller RBAC, but not authenticated observability-backend authorization. Document these upstream wrinkles without code or policy expansion.

### Reproduction and Limits

With the disposable evidence directory available, reproduce using the recorded commands below. The supplied compiled helper invokes actual `GenerateConfigTOML`; no independent helper/source binary attestation is claimed. Harness substitutions add synthetic kubeconfig and local port/default URLs but retain generated table/security output and the actual `/etc/tls` serving paths. Synthetic CA refs map paths only. Do not mount genuine credentials.

```bash
IMAGE=registry.redhat.io/openshift-mcp/openshift-mcp-server-rhel9@sha256:a551fd58f7b2a7505a76ba2109ae5a8f031606605490c9c68d60f91f502f6fe5
E=/tmp/ocpmcp-research/runtime-verify
timeout 180 podman pull "$IMAGE"
go run "$E/certs.go" "$E"
timeout 300 python3 "$E/harness.py"
timeout 150 python3 "$E/extra.py"
timeout 90 python3 "$E/cadata.py"
python3 "$E/check_results.py"
"$E/operator-toml-generator" "$E/serializer-seven-input.json" "$E/serializer-seven.toml"
timeout 300 python3 "$E/serializer_verify.py"
python3 "$E/check_serializer_results.py"
```

Each serializer run uses the pinned cached image with `--pull=never`, `--network=host`, `--config /harness/runtime.toml`, the exact `SSL_CERT_DIR` above, and these read-only mounts:

| Host artifact under `$E` | Container path |
|---|---|
| Whole evidence directory | `/harness:ro,Z` |
| `ca/` | `/etc/mcp-server/ca:ro,Z` |
| `toolset-ca/` | `/etc/mcp-server/toolset-ca:ro,Z` |
| `mcp.crt` | `/etc/tls/tls.crt:ro,Z` |
| `mcp.key` (synthetic only) | `/etc/tls/tls.key:ro,Z` |

Full invocations/inputs/results are in inline local paths `commands.json`, `extra-commands.json`, `cadata-commands.json`, `serializer-commands.json`, `serializer-*-input.json`, `serializer-results.json`, and `serializer-backend-requests.json` under `$E`. Mock servers bind loopback; the MCP host-network listener defaults to all interfaces, so this is not loopback-only MCP binding or the full operator pod security/cluster policy environment. Stop/recreate between trust scenarios is essential. These commands rely on disposable scripts, not a repository test harness.

[PLANNED: OLS-2715] Remaining proof: external public-root endpoint HTTPS; live authenticated Prometheus/Loki/Tempo backend auth/RBAC (including negative authorization); Route-based endpoint discovery/real stacks; other tool/provider/prompt functionality and diagnostic helper image/command/kernel/RBAC/SCC prerequisites. Full-manager live operator watches/projection/rollout, real Service CA injection, bounded Kubernetes caller RBAC, disable/recovery and normal finalizer cleanup passed as recorded above, separately from local-container synthetic logs/traces/auth evidence. Existing full `make test` passes with default Kubernetes 1.27.1 envtest are separate evidence; no new operator tests were run for this documentation update. Neither live local-development execution nor local mock success establishes production deployment, all-tool functionality or shipping completion.

## Planned Changes

| Ticket | Planned scope |
|---|---|
| OLS-2715 | [PLANNED: OLS-2715] Remaining live authenticated Prometheus/Loki/Tempo backend auth/RBAC, Route discovery/real stacks, all-tool/provider/prompt/diagnostic helper prerequisites and external public-root HTTPS proofs. Full-manager live controller/CA/lifecycle, bounded Kubernetes caller RBAC and finalizer cleanup passed; earlier local synthetic logs/traces/auth evidence remains separate. Not production-deployment or shipping-completion proof. |
| OLS-3594 | Optional agentic auto-injection remains deferred; independent of toolset selection/configuration. |

The [versioned source inventory](../how/config-generation.md#ocp-mcp-toolset-inventory--ols-2715-design-input) records upstream prerequisites/filtering limitations; the implemented CR API is in [crd-api.md](crd-api.md#typed-toolset-selection). [Runtime verification](#runtime-verification) records precise passed and remaining scopes, separate from operator/envtest coverage. Support still excludes `openshift/mustgather`, archive storage/ingestion, automatic enablement, privilege grants and prerequisite installation.
