# Resource Lifecycle

The operator manages two categories of Kubernetes resources: owned resources (created by the operator) and external resources (created by users or other controllers). Each category uses a different mechanism for change detection and reconciliation triggering.

## Behavioral Rules

### Owned Resources

1. The operator creates resources with an OwnerReference pointing to the OLSConfig CR. Controller-runtime detects changes to these resources automatically via `Owns()` registrations and triggers reconciliation.
2. Owned resource types: Deployments, ServiceAccounts, ClusterRoles, ClusterRoleBindings, Services, ConfigMaps, Secrets, PersistentVolumeClaims, ConsolePlugins, ServiceMonitors, PrometheusRules, ImageStreams.
3. The ConsolePlugin CR is cluster-scoped and cannot use standard namespace-scoped owner references. It is cleaned up explicitly during finalizer processing.
4. On CR deletion, explicit operand cleanup precedes an OwnerReference-UID sweep (not label matching) of namespaced Deployments, PVCs, Services, ConfigMaps, Secrets, ServiceAccounts, NetworkPolicies, Roles, RoleBindings, ServiceMonitors, and PrometheusRules. Monitoring lists are best-effort when CRDs are unavailable. The sweep waits for deletion before finalizer removal, subject to timeout. It is not identical to the `Owns()` type list. See [reconciliation.md](reconciliation.md) for sequencing, including MCP TLS/monitor/legacy-CA cleanup.
5. Owned resource changes (e.g., someone manually edits a managed ConfigMap) trigger reconciliation, and the operator overwrites them with the desired state.

### External Resources

6. External resources fall into two categories: system resources (fixed, known at compile time) and user-provided resources (derived from the CR spec at runtime).
7. System Secrets: telemetry pull secret (`openshift-config/pull-secret`), chat and agentic console serving certs (`lightspeed-console-plugin-cert`, `lightspeed-agentic-console-plugin-cert`), PostgreSQL certs (`lightspeed-postgres-certs`), OTEL cert (`lightspeed-otel-collector-cert`), MCP cert (`openshift-mcp-server-tls`), and RHOKP cert (`lightspeed-rhokp-tls`). MCP/RHOKP entries remain static but their watches are runtime-gated by introspection/OKP enablement.
8. System configmaps: the OpenShift root CA (`kube-root-ca.crt`), the service CA bundle (`openshift-service-ca.crt`).
9. User-provided secrets: LLM provider credential secrets (`spec.llm.providers[].credentialsSecretRef`), custom TLS secret (`spec.ols.tlsConfig.keyCertSecretRef`), MCP server header secrets (`spec.mcpServers[].headers[].valueFrom.secretRef`).
10. User-provided configmaps: additional CA ConfigMap (`spec.ols.additionalCAConfigMapRef`), proxy CA ConfigMap (`spec.ols.proxyConfig.proxyCACertificate`), alerts adapter runtime config (`spec.ols.deployment.alertsAdapter.configMapRef`, when set).

### Annotation-Based Watching

11. The operator annotates each user-provided external resource with `ols.openshift.io/watcher: cluster` to mark it for watching.
11a. **Credential hot-reload exception (OLS-3450):** When `spec.ols.credentialHotReload` is `true`, LLM credential secrets (those with source prefix `llm-provider-*`) are excluded from annotation. Instead, `removeSecretAnnotationIfNeeded()` removes the watcher annotation if it was previously set. This prevents the watcher predicate from matching these secrets, so `SecretUpdateHandler` never fires for them. Non-LLM secrets (TLS, MCP headers) are always annotated regardless of the flag.
12. On each reconciliation, the operator clears the `AnnotatedSecretMapping` and `AnnotatedConfigMapMapping` in `WatcherConfig` and repopulates them from the current CR spec via `ForEachExternalSecret()` and `ForEachExternalConfigMap()`, then annotates any resources that lack the annotation.
13. The watcher predicate on Update events checks for two conditions: (a) the resource has the `ols.openshift.io/watcher` annotation, or (b) the resource is a configured system resource. Create events are allowed for all resources in the operator namespace (to handle recreated resources that have not been annotated yet). Create events also verify the resource is referenced in the CR before acting. Delete events are allowed for resources in the operator namespace and for configured system resources in other namespaces. The Delete handler enqueues OLSConfig when the object is referenced on the CR or is a configured system resource. Objects owned by OLSConfig are skipped (handled via `Owns()`).

### Change Detection and Restart

14. When a watched secret's `.data` changes (compared via `apiequality.Semantic.DeepEqual`), the `SecretUpdateHandler` triggers restarts of affected deployments directly, without triggering a full reconciliation.
15. When a watched configmap's `.data` or `.binaryData` changes, the `ConfigMapUpdateHandler` triggers restarts of affected deployments directly.
16. Each external resource has a list of affected deployments configured in `WatcherConfig`. All deployment names are explicit (e.g. `lightspeed-app-server`, `lightspeed-rhokp`).
17. Restarts are triggered by updating the `ols.openshift.io/force-reload` annotation on the deployment's pod template with the current timestamp (RFC3339Nano), causing a rolling update. Alerts adapter runtime ConfigMap changes restart `lightspeed-agentic-alerts-adapter` via `RestartAlertsAdapter()`.
18. OTEL/MCP/RHOKP serving Secrets target operand restart, app-server restart (refresh applicable client CAs before roll), and a separate handoff touch gated on Enabled agentic support. The watcher continues after errors, so refresh failure suppresses only the app-server roll, not the later touch callback. Other TLS Secrets have their own target mappings; user-provided Secrets default to app-server only.
18a. Service-ca bundle changes target app-server and PostgreSQL only: no direct handoff touch. Classic MCP trust is `lightspeed-mcp-client-ca`; agentic trust is separately `lightspeed-agentic-mcp-ca`. Disabled/Unknown agentic gates preserve agentic CA Secrets. See [tls.md](tls.md) and [agentic-sandbox-profile.md](agentic-sandbox-profile.md).

### Validation

19. Before annotating resources, the operator validates LLM provider credential secrets via `ValidateLLMCredentials()` (secret must exist and contain expected key) and custom TLS secrets via `ValidateTLSSecret()` (must contain `tls.crt` and `tls.key`).
20. Missing secrets for user-provided resources during annotation are not treated as errors. If a secret does not exist, `annotateSecretIfNeeded()` returns nil, and the resource will be picked up on the next reconciliation when it appears.
21. If `ValidateLLMCredentials()` or `ValidateTLSSecret()` fails, the operator sets `OverallStatus=NotReady` and a `ResourceReconciliation` Failed condition (preserving existing component conditions), then returns an error so controller-runtime retries with backoff.

## Configuration Surface

Resource lifecycle behavior is not directly user-configurable. External resources are derived from CRD fields:

| CR field | Resulting external resource |
|---|---|
| `spec.llm.providers[].credentialsSecretRef` | Provider credential secret |
| `spec.ols.tlsConfig.keyCertSecretRef` | Custom TLS secret |
| `spec.ols.additionalCAConfigMapRef` | Additional CA ConfigMap |
| `spec.ols.proxyConfig.proxyCACertificate` | Proxy CA ConfigMap |
| `spec.ols.deployment.alertsAdapter.configMapRef` | Alerts adapter runtime ConfigMap (restarts `lightspeed-agentic-alerts-adapter` on data change) |
| `spec.mcpServers[].headers[].valueFrom.secretRef` | MCP header secret |

## Constraints

1. The operator can only watch resources in its own namespace and in fixed external namespaces (`openshift-config` for the pull secret, `openshift-monitoring` for the client CA).
2. Delete events on watched external resources enqueue OLSConfig reconciliation so missing credentials, TLS secrets, or CA/config ConfigMaps are detected without waiting for an unrelated event.
3. System resources are defined in static `WatcherConfig.Secrets.SystemResources` and `WatcherConfig.ConfigMaps.SystemResources` lists. MCP/RHOKP TLS watching is conditionally enabled without rewriting those lists.
4. Owned resources with an OwnerReference are skipped by the external resource Create handler to avoid redundant processing; they are handled via the `Owns()` relationship.
5. Normal reconciliation may explicitly delete resources on operand disable or legacy-resource cleanup. MCP disable removal requires an existing `MCPServerReady` condition with `Reason != Disabled`; no condition or an already Disabled condition skips removal. Finalization calls MCP removal unconditionally, independent of the agentic gate.

## Planned Changes

- [OLS-3450] Credential hot-reload: `removeSecretAnnotationIfNeeded()` added to remove watcher annotations from LLM credential secrets when `credentialHotReload` is enabled. See design spec `docs/superpowers/specs/2026-09-01-credential-hot-reload-design.md`.
