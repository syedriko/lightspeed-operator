# Agentic Integration Handoff

Classic lightspeed-operator publishes cluster objects that lightspeed-agentic-operator consumes for sandbox provisioning and OTEL/MCP connectivity. Sandbox Pod/Claim lifecycle stays in agentic-operator. Stories: [OLS-3683](https://redhat.atlassian.net/browse/OLS-3683) (CRD + image), [OLS-3684](https://redhat.atlassian.net/browse/OLS-3684) (handoff artifacts). Agentic-operator consumption: OLS-3685+.

See also: `templog.md` (collector), `ocpmcp.md` (MCP Service/CA), `rhokp.md` (RHOKP Service/CA), `crd-api.md` (`spec.agenticOLS`), `bundle-composition.md` (dual controller), `app-server.md` (client CA Secrets).

## Behavioral Rules

### Ownership

1. Appserver owns separate classic and agentic client-only CA Secrets. The app-server mounts classic `lightspeed-otel-client-ca`, `lightspeed-mcp-client-ca` (when introspection enabled), and `lightspeed-rhokp-client-ca` (when OKP enabled), not the `lightspeed-agentic-*-ca` Secrets published for agentic consumers. All copy public service-ca PEM; serving-cert private keys are never published.
2. Package `internal/controller/agenticintegration` owns only the handoff ConfigMap (`lightspeed-agentic-configuration`). It references CA Secret **names** in ConfigMap data; it does not create or refresh those Secrets. It does not manage sandbox Pods, SandboxClaims, or SandboxTemplates.
3. The former OTEL client ConfigMap `lightspeed-otel-collector-client` is no longer created. OTEL endpoints and CA are published only via the handoff ConfigMap + appserver-owned CA Secrets (no dual-write). On upgrade, otelcollector Phase 1 deletes any leftover `lightspeed-otel-collector-client` ConfigMap (`IgnoreNotFound`). Likewise, ocpmcp Phase 1 / `Remove` deletes leftover `openshift-mcp-server-ca`.

### OLSConfig

1. `spec.agenticOLS` is optional. When omitted or `sandboxMode` is empty, sandbox mode is `bare-pod`.
2. `spec.agenticOLS.sandboxMode` is `bare-pod` or `sandbox-claim` (OpenAPI enum).
3. `spec.agenticOLS.agenticSandboxConfig` uses shared `Config` for resources, tolerations, and nodeSelector. Replicas are ignored (sandbox count is managed by agentic-operator).
4. Sandbox container image comes from classic operator `--agentic-sandbox-image` / `related_images.json` entry `lightspeed-agentic-sandbox`, not from the CR.
4a. `spec.agenticOLS.terminalTTL` is optional, positive whole days (minimum 1) and has no OLSConfig default. When omitted, the agentic operator supplies its own 14-day fallback; the classic operator does not materialize it.
7a. [PLANNED: OLS-3928] The handoff MUST conform to `openshift/ols/.ai/spec/what/tool-result-inspection.md`. `spec.ols.guardrails.toolResultInspection.enabled` is optional and defaults to `true`.
7b. The classic operator MUST use one effective value for the Classic service and the agentic handoff.

### Handoff ConfigMap (`lightspeed-agentic-configuration`)

1. Reconciled **last in Phase 2** (after appserver) only when the agentic version gate is Enabled (completed OpenShift version at least 5.0). Disabled/Unknown gates preserve the existing handoff without mutation; Unknown schedules a retry.
8a. **Create gate (reconcile path only):** under an Enabled agentic gate, first create is skipped (error/`ErrAgenticConfigurationPrerequisitesNotReady`, requeue) until the OTEL Collector Service and agentic OTEL CA Secret with non-empty CA key exist, and — when introspection is enabled — the MCP Service and agentic MCP CA Secret with non-empty CA key exist. This avoids advertising endpoints/trust refs before infrastructure is present.
8b. **Update path:** under an Enabled agentic gate, if the ConfigMap already exists, reconcile still applies Data updates (`sandbox-mode`, `sandbox-pod-spec`, endpoint/CA keys) even when those prerequisites are temporarily unmet (e.g. OTEL Progressing).
8c. **Watcher path:** `TouchAgenticConfiguration` requires an Enabled agentic gate, but does not check Service/CA prerequisites or depend on earlier restart success. A missing handoff ConfigMap is not created by touch.
2. Keys always present:
   - `sandbox-mode` — `bare-pod` or `sandbox-claim`
   - `sandbox-pod-spec` — JSON-serialized thin `corev1.PodSpec`
   - `otel-collector-endpoint` — `lightspeed-otel-collector.<ns>.svc:4317`
   - `otel-admin-endpoint` — `https://lightspeed-otel-collector.<ns>.svc:8080`
   - `otel-ca-secret` — name of the OTEL client CA Secret (`lightspeed-agentic-otel-ca`)
   - `tls-profile` — resolved TLS profile type for agentic provider egress
   - `tls-min-version` — resolved minimum TLS version for agentic provider egress
   - `tls-cipher-suites` — JSON-serialized resolved cipher-suite list for agentic provider egress
   - [PLANNED: OLS-3928] `tool-output-inspection-enabled` — effective `spec.ols.guardrails.toolResultInspection.enabled` value as `"true"` or `"false"`
2a. Optional `terminal-ttl-days` key: decimal positive whole days from `spec.agenticOLS.terminalTTL`. Omit/remove this key on every reconcile when that field or `spec.agenticOLS` is absent; never publish the 14-day fallback. Updates do not change deadlines already recorded on terminal runs.
3. When `spec.ols.introspectionEnabled` is true (default), also set:

- `mcp-endpoint` — OpenShift MCP HTTPS Service URL
- `mcp-ca-secret` — name of the MCP client CA Secret (`lightspeed-agentic-mcp-ca`)

 1. When introspection is disabled and the agentic gate is Enabled, MCP keys are omitted and appserver deletes the agentic MCP client CA Secret when present. Disabled/Unknown gates preserve agentic artifacts.
11b. When OKP is enabled (`!spec.ols.byokRAGOnly`, default), also set `rhokp-endpoint` (RHOKP HTTPS Service URL) and `rhokp-ca-secret` (name of the RHOKP client CA Secret, `lightspeed-agentic-rhokp-ca`). When `byokRAGOnly` is true and the agentic gate is Enabled, both keys are omitted and appserver deletes the agentic RHOKP CA Secret. Disabled/Unknown gates preserve existing artifacts.
11a. [PLANNED: OLS-3491] Optional per-step instruction keys derived from `spec.agenticOLS.instructions`:

- `instructions-analysis` — cluster default analysis system instructions
- `instructions-execution` — cluster default execution system instructions
- `instructions-verification` — cluster default verification system instructions
- `instructions-escalation` — cluster default escalation system instructions
   On each reconcile, the classic operator MUST replace the complete `instructions-*` key subset: publish a key only when its source string is non-empty; **delete** any existing `instructions-*` key whose source is empty or unset. Stale keys MUST NOT remain after a non-empty→empty/unset transition. Agentic-operator consumes these for create-time materialization (analysis/execution/verification) and for call-time escalation resolution. See agentic-operator `what/sandbox-execution.md` and `what/crd-api.md`.
11c. [PLANNED: OLS-3569] Agentic collection does not add or change any `lightspeed-agentic-configuration` key. In particular, the ConfigMap carries no collection state, `transcriptsDisabled` value, telemetry-credential state, or spool path; Agentic trace transport continues to use the existing `otel-collector-endpoint`.

### Thin sandbox PodSpec

 1. PodSpec contains one container (`lightspeed-agentic-sandbox`) with image from `GetAgenticSandboxImage()`, optional resource/toleration/nodeSelector overrides, and writable emptyDirs:
    - `home` → `/home/agent`
    - `skills-workdir` → `/app/skills/.agents`
 2. Default resources are requests only: `500m` CPU, `128Mi` memory (no limits; OpenShift / OLS-3397).
 3. PodSpec does **not** include OTEL/MCP env vars, CA volume mounts, or TLS Secret mounts. Agentic-operator injects connectivity from the ConfigMap and CA Secrets.

### Client-CA Secrets (appserver)

 1. With an Enabled agentic gate, appserver Phase 2 refreshes these opaque Secrets before the Deployment: OTEL `lightspeed-agentic-otel-ca` (`otel-ca.crt`), MCP `lightspeed-agentic-mcp-ca` (`mcp-ca.crt`, only with introspection), and RHOKP `lightspeed-agentic-rhokp-ca` (`rhokp-ca.crt`, only with OKP). All copy `openshift-service-ca.crt` / `service-ca.crt`.
 2. Under that gate, disabling introspection/OKP deletes the corresponding agentic Secret. Disabled/Unknown agentic gates skip all reads, writes, and deletes of agentic CA Secrets.
 3. Secrets contain public CA material only — never serving-cert private keys.
 4. App-server mounts the distinct classic Secrets at `/etc/certs/otel-collector-ca/`, `/etc/certs/openshift-mcp-server-ca/`, and `/etc/certs/rhokp-ca/`, projecting each CA key as `service-ca.crt`. There is no dedicated MCP or RHOKP inject-cabundle ConfigMap. See [tls.md](tls.md).

### Refresh / rotation

 1. Serving-cert watchers restart the server Deployment then the app-server (`lightspeed-app-server`) and touch `lightspeed-agentic-configuration`:
    - OTEL: `lightspeed-otel-collector-cert` → `RestartOtelCollector` + `RestartAppServer` + `TouchAgenticConfiguration`
    - MCP: `openshift-mcp-server-tls` → MCP restart + `RestartAppServer` + `TouchAgenticConfiguration`
    - RHOKP: `lightspeed-rhokp-tls` → RHOKP restart + `RestartAppServer` + `TouchAgenticConfiguration`
    All three targets are declared in `AffectedDeployments` for each Secret; the watcher continues to subsequent targets after errors. Touch is agentic-gated, independent of refresh/roll success.
 2. `RestartAppServer` order: (1) refresh client CA Secrets from `openshift-service-ca.crt` (`RefreshClientCASecrets`), (2) **re-Get** the app-server Deployment (current resourceVersion), apply any caller Spec mutations, bump `force-reload`, Update. **Fail-closed:** if step (1) fails (source CA ConfigMap missing/empty), the app-server roll is skipped so pods are not rolled with stale CA material. Retry happens on a later OLSConfig reconcile or watcher event once the source CA is ready.
 3. `TouchAgenticConfiguration` bumps `ols.openshift.io/client-ca-reload` on an existing handoff ConfigMap only with an Enabled agentic gate. It is a separate watcher callback, never part of `RestartAppServer`; refresh failure skips only the app-server roll, not this callback. Changes to the service-ca bundle restart app-server and PostgreSQL without directly touching the handoff.
 4. `RestartOtelCollector` only rolls the collector; it does **not** refresh agentic artifacts (that work is on the app-server restart path).
 5. Agenticintegration ConfigMap reconcile preserves the cert-reload annotation when updating Data/Labels.
 6. Content equality skips Secret/ConfigMap updates when Data, Labels, and OwnerReferences are unchanged.

### Agentic-operator contract (classic operator expectations)

 1. Agentic-operator reads fixed object names from the cluster (bundle may pass names as flags). Prefer Kubernetes objects over importing `ols.openshift.io` types.
 2. Agentic-operator should wait/requeue until the handoff ConfigMap exists and (when needed) collector Service Endpoints / CA Secrets are present. Do not rely on CSV install order between the two controllers.
 3. Watch for `ols.openshift.io/client-ca-reload` (or ConfigMap RV) to reload mounted CA PEMs after rotation.
 4. Consuming the new ConfigMap/Secrets (and dropping `lightspeed-otel-collector-client`) is agentic-operator work (OLS-3685+).

## Resource Names

| Resource | Name | Owner |
| --- | --- | --- |
| Handoff ConfigMap | `lightspeed-agentic-configuration` | `agenticintegration` |
| OTEL client-CA Secret | `lightspeed-agentic-otel-ca` (`otel-ca.crt`) | `appserver` |
| Agentic MCP client-CA Secret | `lightspeed-agentic-mcp-ca` (`mcp-ca.crt`) | `appserver` |
| Classic MCP client-CA Secret | `lightspeed-mcp-client-ca` (`mcp-ca.crt` projected as `service-ca.crt`) | `appserver` |
| RHOKP client-CA Secret | `lightspeed-agentic-rhokp-ca` (`rhokp-ca.crt`) | `appserver` |
| Sandbox container (in PodSpec) | `lightspeed-agentic-sandbox` | (embedded in ConfigMap) |

## Configuration Surface

| Field / flag | Description |
| --- | --- |
| `spec.agenticOLS.sandboxMode` | `bare-pod` (default) or `sandbox-claim` |
| `spec.agenticOLS.agenticSandboxConfig` | Resources / tolerations / nodeSelector for thin PodSpec |
| `spec.agenticOLS.terminalTTL` | Optional admin ceiling in positive whole days; published as `terminal-ttl-days` |
| `spec.agenticOLS.instructions.*` | [PLANNED: OLS-3491] Optional cluster per-step system instructions → ConfigMap `instructions-*` keys |
| `spec.ols.introspectionEnabled` | Gates MCP keys and MCP client CA Secrets; agentic mutations also require Enabled version gate |
| `spec.ols.additionalCAConfigMapRef` | Conditionally publishes the referenced ConfigMap name as `additional-ca-configmap` |
| `spec.ols.guardrails.toolResultInspection.enabled` | [PLANNED: OLS-3928] Publishes `tool-output-inspection-enabled`; defaults to `true` |
| `--agentic-sandbox-image` | Sandbox container image in thin PodSpec |

## Constraints

1. Classic operator does not create SandboxTemplate or manage sandbox lifecycle.
2. No raw user-editable full PodSpec on OLSConfig.
3. Serving Secrets must not be published for agentic mount (private key risk).
4. OTEL collector remains always deployed; OTEL keys are always present in a published handoff regardless of `spec.audit.logging`; handoff publication still requires an Enabled agentic gate.
5. `RestartAppServer` is fail-closed on client CA refresh failure (see Refresh / rotation).

## Out of Scope

- Agentic-operator wait loop, PodSpec injection, and SandboxTemplate path (OLS-3685+)
- Optional agentic auto-injection of MCP into runs ([OLS-3594](https://redhat.atlassian.net/browse/OLS-3594))
- Defining `agentic.openshift.io` CRD changes

## Planned Changes

- [PLANNED: OLS-3928] Publish `tool-output-inspection-enabled` from the cluster-wide tool-result inspection configuration.

## Cross-References

- `what/templog.md` — collector operand; OTEL connectivity consumed via this handoff
- [ocpmcp.md](ocpmcp.md) — MCP Service and distinct classic/agentic trust
- [reconciliation.md](reconciliation.md) — Phase 2 ordering (OTEL before MCP/appserver, handoff last under Enabled gate)
- [tls.md](tls.md) — service-ca PEM sources and independent rotation callbacks
- [reconciliation architecture](../how/reconciliation.md) — version/create gates and watcher dispatch
- [deployment generation](../how/deployment-generation.md) — classic CA mounts and MCP tracked versions
- `how/project-structure.md` — `appserver` / `agenticintegration` packages
- `what/agentic-data-collection.md` — collection gate and unchanged-handoff contract

## Planned Changes

- [PLANNED: OLS-3569] Keep Agentic collection state and storage paths out of the handoff ConfigMap.
