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
  └─ TOML ConfigMap                   openshift-mcp-server-config
```

Gated by `spec.ols.introspectionEnabled` (default `true` when absent). When false, the operator sets `MCPServerReady=False`, `Reason=Disabled` without blocking overall readiness. Cleanup is conditional on a previously recorded non-Disabled MCP condition.

## Behavioral Rules

### Activation
1. When `spec.ols.introspectionEnabled` is true (or absent), Phase 1 and Phase 2 reconcile the standalone MCP operand.
2. When false, Phase 1 removes managed MCP resources only if an existing `MCPServerReady` condition has `Reason != Disabled`. With no condition or an already Disabled condition, cleanup is skipped. Phase 2 skips deployment reconciliation and records `MCPServerReady=False`, `Reason=Disabled`.

### Phase 1 Resources
3. ConfigMap `openshift-mcp-server-config` — TOML runtime config (pinned toolsets, denied Secret/RBAC resources, metrics endpoints).
4. ServiceAccount `openshift-mcp-server` — no RBAC bindings; callers pass their own token (app-server config uses the runtime placeholder `Authorization: kubernetes`).
5. NetworkPolicy `openshift-mcp-server` — allows TCP `:8443` ingress from any pod in the operator namespace and from cluster Prometheus pods in `openshift-monitoring`. The Prometheus peer requires both the namespace label `kubernetes.io/metadata.name: openshift-monitoring` and pod labels `app.kubernetes.io/name: prometheus` and `prometheus: k8s` (OLS-3943); it does not allow every pod in the monitoring namespace. The policy remains ingress-only and is deleted by the conditional disable cleanup in rule 2.

### Phase 2 Resources
6. Service `openshift-mcp-server` — ClusterIP, port `https` `:8443`, serving-cert annotation → Secret `openshift-mcp-server-tls`.
7. Wait for TLS Secret keys `tls.crt` / `tls.key` before creating/updating the Deployment.
8. Deployment `openshift-mcp-server` — starts with `--config` pointing to mounted TOML that configures HTTPS serving certificates; probes use HTTPS `/healthz`. Image comes from `--openshift-mcp-server-image`, with `PullIfNotPresent`. Replicas/resources/tolerations/nodeSelector from `spec.ols.deployment.mcpServer` (`Config`).

### App-server Integration
9. olsconfig `mcp_servers` includes an `openshift` entry pointing at `https://openshift-mcp-server.<namespace>.svc:8443/mcp` with the runtime placeholder `Authorization: kubernetes` when introspection is enabled. See [app-server.md](app-server.md) and [deployment-generation.md](../how/deployment-generation.md).
10. App-server mounts classic client CA Secret `lightspeed-mcp-client-ca` (sourced from `openshift-service-ca.crt`) at `/etc/certs/openshift-mcp-server-ca/`, projecting `mcp-ca.crt` as `service-ca.crt`, and adds that file to `extra_ca`. Agentic trust uses a distinct Secret, `lightspeed-agentic-mcp-ca`. See [tls.md](tls.md) and [agentic-sandbox-profile.md](agentic-sandbox-profile.md). There is no dedicated MCP inject-cabundle ConfigMap; enabled Phase 1 / `Remove` deletes leftover `openshift-mcp-server-ca` on upgrade.
11. App-server Deployment does not track an MCP CA hash or MCP TOML ConfigMap ResourceVersion.

### Watching and Restarts
12. Secret `openshift-mcp-server-tls` is listed statically in `WatcherConfig.Secrets.SystemResources`. Watching is gated by `OpenShiftMCPServerTLSWatchEnabled` (`syncOpenShiftMCPServerTLSWatcher`), set from `introspectionEnabled`, so enable/disable does not rewrite the SystemResources slice under the informer.
13. On TLS Secret data change, the watcher independently restarts MCP and the app-server. A separate handoff touch runs only when the agentic gate is Enabled. App-server restart refreshes applicable client CA Secrets before rolling; refresh failure skips its roll, but does not stop the separate handoff callback. See [agentic-sandbox-profile.md](agentic-sandbox-profile.md).
14. ConfigMap `openshift-service-ca.crt` changes restart the app-server (refreshing applicable OTEL/MCP/RHOKP client CAs) and PostgreSQL; they do not directly touch the handoff ConfigMap.
15. MCP Deployment also tracks ConfigMap and TLS Secret ResourceVersions and rolls when they change.

### Security
16. TOML denies `core/v1` `Secret` and all `rbac.authorization.k8s.io/v1` resources so Secret/RBAC data cannot reach the LLM via the shipped server.
17. Toolsets are pinned to `core`, `config`, `helm`, `observability/metrics`, `kubevirt`. TOML sets `read_only = false` and enables target compatibility tool filters. Observability metrics uses in-cluster Thanos Querier and Alertmanager URLs. Metrics `guardrails = "!tsdb"` (PromQL query safety, not RBAC) follows upstream OpenShift guidance when Thanos lacks the TSDB status API; auth remains the caller's bearer token.
18. User-defined MCP servers (`spec.mcpServers`) are out of scope for this operand.

### Monitoring
19. ServiceMonitor `openshift-mcp-server-monitor` (OLS-3728) — scrapes MCP server metrics via HTTPS on port 8443, path `/metrics` (Go promhttp). Server TLS only (service-ca CA bundle + `serverName`; no client certs / Bearer token), 30s interval. Reconciled in Phase 2 via `utils.ReconcileServiceMonitor()`. Skipped if Prometheus Operator CRDs are not installed. The NetworkPolicy ingress in rule 5 admits the cluster Prometheus scrape (OLS-3943); the ServiceMonitor alone does not grant network access.

### Finalizer
20. On CR deletion, `ocpmcp.Remove()` deletes Deployment, Service, NetworkPolicy, ConfigMap, ServiceAccount, TLS Secret (`openshift-mcp-server-tls`), ServiceMonitor (`openshift-mcp-server-monitor`), and legacy CA ConfigMap (`openshift-mcp-server-ca`) before owned-resource sweep. Appserver-owned client CA Secrets are handled by that sweep.

## Configuration Surface

| Field path | Description |
|---|---|
| `spec.ols.introspectionEnabled` | Enable/disable standalone MCP (`*bool`, default true) |
| `spec.ols.mcpKubeServerConfig.timeout` | Timeout seconds for the built-in openshift MCP entry in olsconfig |
| `spec.ols.deployment.mcpServer` | Standalone MCP `Config` (replicas, resources, tolerations, nodeSelector) |
| `--openshift-mcp-server-image` | MCP container image override |

## Constraints

1. Multi-replica is allowed; Streamable HTTP is configured for stateless operation upstream.
2. The MCP ServiceAccount has no cluster RBAC; authorization uses the calling user's token.
3. The `openshift-mcp-server` image is shipped by the OCP MCP team from `registry.redhat.io/openshift-mcp/openshift-mcp-server-rhel9`. OLS does not build or release this image. Digest/tag updates track the OCP MCP team's releases; bump `related_images.json` and regenerate the bundle when a new release is available.
4. Agentic/sandbox reuse of the MCP Service URL is published in the handoff ConfigMap; the distinct agentic MCP client CA Secret is owned by appserver when the agentic gate and introspection are enabled — see [agentic-sandbox-profile.md](agentic-sandbox-profile.md). Optional auto-injection into agent runs remains deferred (OLS-3594).

## Planned Changes

None for the standalone HTTPS cutover itself. Optional agentic auto-injection remains planned (OLS-3594).
