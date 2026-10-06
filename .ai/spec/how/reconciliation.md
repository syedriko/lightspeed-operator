# Reconciliation Architecture

## Module Map

| File | Key Symbols | Responsibility |
|---|---|---|
| `internal/controller/olsconfig_controller.go` | `OLSConfigReconciler`, `Reconcile()`, `SetupWithManager()` | Main reconciler, orchestration, watcher setup |
| `internal/controller/olsconfig_helpers.go` | `UpdateStatusCondition()`, `checkDeploymentStatus()`, `annotateExternalResources()` | Status management, diagnostics, resource annotation |
| `internal/controller/operator_assets.go` | `ReconcileServiceMonitorForOperator()`, `ReconcileNetworkPolicyForOperator()` | Operator-level resources |
| `internal/controller/reconciler/interface.go` | `Reconciler` interface | Dependency injection for component packages |

## Data Flow

Main reconciliation loop:
```
Reconcile(ctx, req)
  -> getAndValidateCR()                    # Fetch CR, validate name == "cluster"
  -> handleFinalizer()                      # Add/remove finalizer, run cleanup
  -> ReadAgenticVersion() / WithAgenticVersion() # completed-version gate snapshot; Unknown retries
  -> reconcileOperatorResources()           # ServiceMonitor, NetworkPolicy (operator-level)
  -> annotateExternalResources()            # Validate secrets, annotate for watching
  -> reconcileIndependentResources()        # Phase 1 (continue-on-error; order below matches code)
  |   |-- console.ReconcileConsoleUIResources()
  |   |-- postgres.ReconcilePostgresResources()
  |   |-- otelcollector.ReconcileOtelCollectorResources()
  |   |-- ocpmcp.ReconcileResources()
  |   |   (when introspectionEnabled; else Remove only if wasComponentEnabled(MCPServerReady))
  |   |-- rhokp.ReconcileResources()
  |   |   (when !byokRAGOnly; else Remove only if wasComponentEnabled(RHOKPReady))
  |   |-- agenticconsole.ReconcileAgenticConsoleUIResources() # Enabled agentic gate + image
  |   |-- alertsadapter.ReconcileAlertsAdapterResources() # Enabled agentic gate + image
  |   |   (opt-in via configMapRef; cleanup only with Enabled gate and previous non-Disabled condition;
  |   |    no ConfigMap validation;
  |   |    mount at /etc/alerts-adapter when CM exists)
  |   +-- appserver.ReconcileAppServerResources()
  -> reconcileDeploymentsAndStatus()        # Phase 2: deployments + status update (order below matches code)
      |-- console.ReconcileConsoleUIDeploymentAndPlugin()   # ConsolePluginReady
      |-- postgres.ReconcilePostgresDeployment()            # CacheReady
      |-- otelcollector.ReconcileOtelCollectorDeployment()  # OtelCollectorReady
      |-- ocpmcp.ReconcileDeployment()                      # MCPServerReady; disabled => False/Disabled
      |-- rhokp.ReconcileDeployment()                       # RHOKPReady; disabled => False/Disabled
      |-- appserver.ReconcileAppServerDeployment()          # ApiReady (OTEL/MCP/RHOKP Services attempted first)
      |-- agenticconsole.ReconcileAgenticConsoleUIDeploymentAndPlugin() # Enabled agentic gate + image
      |-- alertsadapter.ReconcileAlertsAdapterDeployment()  # Enabled agentic gate + image + configMapRef
      |   (each deployment step above: checkDeploymentStatus → conditions)
      |-- agenticintegration.ReconcileAgenticIntegrationResources()  # Enabled agentic gate only; last, after loop: ConfigMap only; failure → OverallStatus NotReady
      +-- UpdateStatusCondition()           # Single status update
```

## Key Abstractions

### Reconciler Interface
The `reconciler.Reconciler` interface breaks the circular dependency between the main controller and component packages. Component packages (appserver, postgres, otelcollector, ocpmcp, rhokp, agenticintegration, console, agenticconsole, alertsadapter) receive this interface instead of importing the controller package directly. It embeds `client.Client` and adds getter methods for images, namespace, and OpenShift version.

### ReconcileSteps Pattern
Both phases use a slice of `ReconcileSteps` structs, each containing a Name, reconcile function, and (for Phase 2) a ConditionType and Deployment name. Phase 1 iterates with continue-on-error; Phase 2 iterates but tracks all conditions and diagnostics.

### Resource Ownership
Two ownership models:
1. **Owned resources**: Controller-runtime Owns() declarations. Owner references set on creation. Changes trigger reconciliation automatically.
2. **External resources**: Watches() with custom predicates. Annotation-based filtering. Secret/ConfigMap handlers compare data and trigger deployment restarts on update. Deletes of referenced external resources or configured system resources enqueue OLSConfig reconcile so credentials/CA can be re-validated.

### Finalizer Cleanup
`finalizeOLSConfig()` runs before version gating: chat console removal, agentic console removal, alerts adapter removal, `ocpmcp.Remove()`, `rhokp.Remove()`, then `listOwnedResources()` / delete / wait. MCP removal deletes Deployment, Service, NetworkPolicy, TOML ConfigMap, ServiceAccount, TLS Secret, ServiceMonitor, and legacy CA ConfigMap. Component removal errors are logged; alerts-adapter pending cleanup retries until timeout.

The sweep matches OwnerReference UID, not labels. Its exact namespaced lists are Deployments, PVCs, Services, ConfigMaps, Secrets (including classic/agentic client CAs), ServiceAccounts, NetworkPolicies, Roles, RoleBindings, ServiceMonitors, and PrometheusRules. Monitoring list errors are ignored. It is not an enumeration of all `Owns()` types: ConsolePlugins are explicitly removed; ClusterRoles/ClusterRoleBindings rely on operand cleanup or garbage collection; ImageStreams are not listed. The wait loop uses `wait.PollUntilContextTimeout`.

### MCP Disable and Agentic Gates
- `wasComponentEnabled(cr, TypeMCPServerReady)` returns true only when that condition exists and its `Reason != "Disabled"`; it does not test condition Status. Disabled introspection invokes `ocpmcp.Remove()` during Phase 1 step construction only under this predicate, before the task loop. Phase 2 always emits `MCPServerReady=False`, `Reason=Disabled` without making OverallStatus NotReady solely for that condition.
- `ReadAgenticVersion` in `utils/utils.go` requires the newest history entry to be Completed and match `status.desired.version`. Parsed major >= 5 is Enabled, earlier releases Disabled, unreadable/incomplete/mismatched versions Unknown. Normal reconciliation snapshots this in context; Unknown skips agentic mutations and schedules a retry. Classic MCP is gated by introspection, not by this agentic gate.
- Only Enabled allows handoff reconciliation, handoff touch, or agentic client CA Secret reconciliation. Disabled/Unknown preserve existing agentic artifacts, including Secrets on operand opt-out.
- First handoff create checks OTEL Service and non-empty `otel-ca.crt` in `lightspeed-agentic-otel-ca`, plus (when introspection enabled) MCP Service and non-empty `mcp-ca.crt` in `lightspeed-agentic-mcp-ca`. Existing ConfigMap updates skip those infrastructure checks, but still require the Enabled version gate. See [agentic-sandbox-profile.md](../what/agentic-sandbox-profile.md).

### TLS Watcher Callbacks
`cmd/main.go` statically lists `openshift-mcp-server-tls`; `syncOpenShiftMCPServerTLSWatcher()` atomically toggles `OpenShiftMCPServerTLSWatchEnabled` from introspection without rewriting the list. Its targets are MCP restart, app-server restart, then handoff touch. OTEL/RHOKP serving Secrets use the analogous three targets.

`watchers.restartDeployment()` dispatches each target via `restartFuncs` and continues after errors. `RestartAppServer()` refreshes applicable client CA Secrets, re-fetches the Deployment, applies caller mutations, and rolls; refresh failure stops that callback before rolling. It does not call `TouchAgenticConfiguration()`. The separate touch callback still runs after earlier errors, but returns without mutation unless `AgenticGate` is Enabled; it skips a missing ConfigMap and checks no infrastructure prerequisites. `openshift-service-ca.crt` targets only app-server and PostgreSQL, with no direct handoff touch. See [deployment-generation.md](deployment-generation.md) for CA mounts and tracked versions.

### Status Update Mechanics
`UpdateStatusCondition()` uses `retry.RetryOnConflict` with `client.MergeFrom` patch. It preserves `LastTransitionTime` for conditions whose status hasn't changed. It re-fetches the CR before each update attempt to get the latest ResourceVersion.

### Deployment Health Check
`checkDeploymentStatus()` returns one of three states:
- "Ready": `DeploymentAvailable` condition is True
- "Failed": Terminal pod failures detected (CrashLoopBackOff, ImagePullBackOff, etc.)
- "Progressing": Not ready but no terminal failures

`collectDeploymentDiagnostics()` lists pods matching the deployment's selector and inspects:
- Container statuses (Waiting with reason, Terminated with non-zero exit)
- Last termination state (for CrashLoopBackOff context)
- Init container statuses
- Pod scheduling conditions (Unschedulable)
- Pod readiness conditions
- Pod phase (Failed, Unknown)

## Integration Points

| Consumer | Provider | Mechanism |
|---|---|---|
| Component packages | Main controller | `reconciler.Reconciler` interface |
| Watcher handlers | Component restart functions | `watchers.SecretUpdateHandler`, `watchers.ConfigMapUpdateHandler` |
| Status updates | Kubernetes API | `retry.RetryOnConflict` with `client.MergeFrom` patch |
| Finalizer cleanup | Kubernetes API | Owner reference UID matching + explicit delete |

## Implementation Notes

- `SetupWithManager()` registers Owns() for 12 resource types and Watches() for Secrets and ConfigMaps with custom predicates.
- Secret watch predicates: Create events allowed for all secrets in operator namespace (handles recreated secrets); Update events filtered by watcher annotation or system-resource rules; Delete events allowed for operator-namespace secrets and configured system secrets elsewhere—the Delete handler enqueues OLSConfig when the secret is referenced on the CR or is a configured system resource (owned secrets are skipped).
- ConfigMap watch predicates: Same pattern as secrets.
- The `LOCAL_DEV_MODE` environment variable skips operator ServiceMonitor creation and app-server metrics reader secret reconciliation when running locally (`make run`).
- Phase 1 failures update status with `ResourceReconciliation` condition type (not the component-specific types used in Phase 2).
