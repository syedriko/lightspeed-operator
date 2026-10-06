# CRD API

Specification of the OLSConfig Custom Resource Definition. Source of truth: `api/v1alpha1/olsconfig_types.go`.

## Behavioral Rules

### Resource Identity

1. API group: `ols.openshift.io`, version: `v1alpha1`, kind: `OLSConfig`.
2. Cluster-scoped (not namespaced). Marker: `+kubebuilder:resource:scope=Cluster`.
3. `.metadata.name` must be `"cluster"`. Enforced by XValidation rule on the OLSConfig type: `self.metadata.name == 'cluster'`.
4. Has a status subresource (`+kubebuilder:subresource:status`).
5. Finalizer: `ols.openshift.io/finalizer` (constant `OLSConfigFinalizer` in `internal/controller/utils/constants.go`).
6. `spec` is required on the OLSConfig object.

### Top-Level Spec Structure

Field path | JSON key | Go type | Required | Description
---|---|---|---|---
`spec.llm` | `llm` | `LLMSpec` | Yes | LLM provider configuration
`spec.ols` | `ols` | `OLSSpec` | Yes | OLS service settings
`spec.olsDataCollector` | `olsDataCollector` | `OLSDataCollectorSpec` | No | Data collector settings (logLevel only)
`spec.mcpServers` | `mcpServers` | `[]MCPServerConfig` | No | External MCP server configurations. MaxItems=20
`spec.featureGates` | `featureGates` | `[]FeatureGate` | No | Feature gates. Enum values: `MCPServer`, `ToolFiltering`
`spec.audit` | `audit` | `AuditConfig` | No | OTEL Collector audit log storage and trace forwarding. Does not configure lightspeed-service. Value type (not pointer).
`spec.agenticOLS` | `agenticOLS` | `*AgenticOLSSpec` | No | Classic→agentic sandbox handoff settings. When omitted, sandbox mode is treated as `bare-pod`.

### Audit Configuration (spec.audit)

Collector-only settings for the in-cluster OTEL Collector ([OLS-3505](https://redhat.atlassian.net/browse/OLS-3505)). See `templog.md` for operand behavior. **Does not** propagate into `olsconfig.yaml`.

#### AuditConfig Fields

Field path (relative to `spec.audit`) | JSON key | Go type | Required | Default | Validation | Description
---|---|---|---|---|---|---
`logging` | `logging` | `*bool` | No | `true` when absent | Optional | Enable Collector logs → Postgres pipeline
`tracingEndpoint` | `tracingEndpoint` | `string` | No | (empty) | MaxLength=253 | OTLP trace export backend (e.g. `"jaeger:4317"`). TLS always used by collector.

#### Audit Behavioral Rules

54. `spec.audit` is a value type (`Audit AuditConfig`). Go's `encoding/json` always serializes it as at least `{}` when other spec fields are present; the tag has no `omitempty`. No helper methods on `AuditConfig`.
55. `spec.audit.logging` defaults to **enabled** (`true`) when absent. When `false`, the collector omits the Postgres logs pipeline ([OLS-3510](https://redhat.atlassian.net/browse/OLS-3510)).
56. `spec.audit.tracingEndpoint` is optional. When set, the collector forwards traces to that backend with TLS ([OLS-3510](https://redhat.atlassian.net/browse/OLS-3510)).
57. Service stdout audit and in-cluster trace export are configured separately — see `spec.ols.auditEventsEnabled` and `audit-logging.md`.

#### Removed (breaking change)

- `AuditLoggingMode`, `AuditOTELConfig`, `AuditOTELTLSMode` types
- `spec.audit.otel.endpoint`, `spec.audit.otel.tlsMode`
- `AuditConfig.LoggingEnabled()`, `OTELEndpoint()`, `OTELInsecure()` helpers
- Previous semantics: `spec.audit.logging` as `Enabled`/`Disabled` stdout enum — replaced by `spec.ols.auditEventsEnabled`

### Agentic OLS Configuration (spec.agenticOLS)

Settings consumed by the classic operator when publishing the agentic handoff ConfigMap (`lightspeed-agentic-configuration`). See OLS-3683 / OLS-3684.

#### AgenticOLSSpec Fields

Field path (relative to `spec.agenticOLS`) | JSON key | Go type | Required | Default | Validation | Description
---|---|---|---|---|---|---
`sandboxMode` | `sandboxMode` | `SandboxMode` | No | `bare-pod` | Enum: `bare-pod`, `sandbox-claim` | How the agentic operator provisions sandbox pods
`agenticSandboxConfig` | `agenticSandboxConfig` | `Config` | No | — | — | Resources, tolerations, nodeSelector for the thin sandbox PodSpec. Replicas ignored.
`terminalTTL` | `terminalTTL` | `*int32` | No | — | Minimum=1 | Cluster ceiling for terminal AgenticRun retention, in whole days. Omitted value is not defaulted in this CRD; the agentic operator supplies its own 14-day fallback.
`instructions` | `instructions` | `*AgenticStepInstructions` | No | — | — | ~~[SUPERSEDED]~~ Removed by OLS-3491 redesign. Per-step instructions now live on the `Agent` CR in `agentic.openshift.io`. See design spec `docs/superpowers/specs/2026-09-01-configurable-instructions-design.md`.

#### AgenticStepInstructions Fields ~~[SUPERSEDED by OLS-3491 redesign]~~

This section is removed. Per-step instructions now live on the `Agent` CR (`agentic.openshift.io`), not on `OLSConfig`. See design spec `docs/superpowers/specs/2026-09-01-configurable-instructions-design.md`.

#### AgenticOLS Behavioral Rules

58. `spec.agenticOLS` is optional (pointer). When omitted or `sandboxMode` is empty, the operator treats sandbox mode as `bare-pod`.
59. `sandboxMode=bare-pod` runs agent sandboxes as bare Pods (no Agent Sandbox API CRDs required). `sandbox-claim` uses the Agent Sandbox API.
60. OpenAPI enum validation rejects values other than `bare-pod` and `sandbox-claim`.
61. `agenticSandboxConfig` overrides default sandbox PodSpec scheduling/resources (requests-only defaults: 500m CPU / 128Mi memory). Replicas are ignored.
62. Classic operator publishes handoff via appserver-owned client CA Secrets plus `agenticintegration` ConfigMap (`lightspeed-agentic-configuration`). See `agentic-sandbox-profile.md`.
62a. When `spec.agenticOLS.terminalTTL` is set, validate it as a positive whole number (minimum 1) and publish its decimal value as `terminal-ttl-days` in the handoff ConfigMap. When absent, omit/remove that key; do not default it in OLSConfig. See the parent `ols/.ai/spec/what/terminal-run-ttl.md`.
63–67. ~~[SUPERSEDED by OLS-3491 redesign]~~ Rules 63–67 removed. Per-step instructions now live on the `Agent` CR in `agentic.openshift.io`, not on `OLSConfig`. The classic operator no longer participates in instruction delivery. See design spec `docs/superpowers/specs/2026-09-01-configurable-instructions-design.md`.

### LLM Provider Configuration (spec.llm)

7. `spec.llm.providers` is required. Type: `[]ProviderSpec`. MaxItems=10.

#### ProviderSpec Fields

Field path (relative to each provider) | JSON key | Go type | Required | Description
---|---|---|---|---
`name` | `name` | `string` | Yes | Provider name
`url` | `url` | `string` | No | Provider API URL. Pattern: `^https?://.*$`
`credentialsSecretRef` | `credentialsSecretRef` | `corev1.LocalObjectReference` | Yes | Secret containing API credentials
`models` | `models` | `[]ModelSpec` | Yes | Provider models. MaxItems=50
`type` | `type` | `string` | Yes | Provider type enum: `azure_openai`, `bam`, `openai`, `watsonx`, `rhoai_vllm`, `rhelai_vllm`, `fake_provider`, `google_vertex`, `google_vertex_anthropic`, `bedrock`
`deploymentName` | `deploymentName` | `string` | No | Azure OpenAI deployment name
`apiVersion` | `apiVersion` | `string` | No | Azure OpenAI API version
`projectID` | `projectID` | `string` | No | Watsonx project ID
`googleVertexConfig` | `googleVertexConfig` | `*VertexConfig` | No | Google Vertex provider configuration. Required when `type == "google_vertex"`, forbidden otherwise
`googleVertexAnthropicConfig` | `googleVertexAnthropicConfig` | `*VertexConfig` | No | Google Vertex Anthropic provider configuration. Required when `type == "google_vertex_anthropic"`, forbidden otherwise
`fakeProviderMCPToolCall` | `fakeProviderMCPToolCall` | `bool` | No | Fake provider MCP tool call flag
`tlsSecurityProfile` | `tlsSecurityProfile` | `*configv1.TLSSecurityProfile` | No | TLS profile for provider connection
`credentialKey` | `credentialKey` | `string` | No | Key name within `credentialsSecretRef` to read credential from. Defaults to `"apitoken"` if unset

#### VertexConfig Fields

Field path (relative to VertexConfig) | JSON key | Go type | Required | Description
---|---|---|---|---
`projectID` | `projectID` | `string` | No | Google Cloud project ID
`location` | `location` | `string` | No | Server region location

For `type == "bedrock"`, use provider `url` for the Mantle gateway endpoint and `credentialsSecretRef` for authentication (Bearer `apitoken` or IAM keys `aws_access_key_id` / `aws_secret_access_key`, with optional `role_arn`). The operator validates credentials at reconcile time and maps them to `credentials_path` for the service.

#### Provider XValidation Rules

8. Azure OpenAI requires `deploymentName`: when `type == "azure_openai"`, `deploymentName` must not be empty.
9. Watsonx requires `projectID`: when `type == "watsonx"`, `projectID` must not be empty.
10. `credentialKey` must not be empty or whitespace: if set, it must not match `^[ \t\n\r\v\f]*$`.
11. Google Vertex requires `googleVertexConfig`: when `type == "google_vertex"`, `googleVertexConfig` must be present.
12. Google Vertex Anthropic requires `googleVertexAnthropicConfig`: when `type == "google_vertex_anthropic"`, `googleVertexAnthropicConfig` must be present.
13. `googleVertexConfig` may only be set when `type == "google_vertex"`.
14. `googleVertexAnthropicConfig` may only be set when `type == "google_vertex_anthropic"`.

#### ModelSpec Fields

Field path (relative to each model) | JSON key | Go type | Required | Description
---|---|---|---|---
`name` | `name` | `string` | Yes | Model name
`url` | `url` | `string` | No | Model API URL. Pattern: `^https?://.*$`
`contextWindowSize` | `contextWindowSize` | `uint` | No | Context window in tokens. Minimum=1024
`parameters` | `parameters` | `ModelParametersSpec` | No | Model parameters

#### ModelParametersSpec Fields

Field path (relative to parameters) | JSON key | Go type | Required | Default | Validation
---|---|---|---|---|---
`maxTokensForResponse` | `maxTokensForResponse` | `int` | No | (unset; application default is 2048) | None
`toolBudgetRatio` | `toolBudgetRatio` | `float64` | No | `0.25` | Minimum=0.1, Maximum=0.5
`temperature` | `temperature` | `*float64` | No | (unset; service does not specify temperature) | Minimum=0; must be finite. Explicit zero is preserved. Replaces `temperatureSupported`, which the service rejects.
`reasoningConfig` | `reasoningConfig` | `map[string]runtime.RawExtension` | No | (unset) | None. Freeform map of provider-specific reasoning/thinking parameters. Passed through to the service as `reasoning_config`. Valid keys vary by provider and model generation — see lightspeed-service `what/llm-providers.md` rule 13. When absent, no reasoning params are sent. When present with invalid keys, the provider API returns a clear 400 error.

This Go type change does not alter the `reasoningConfig` JSON/YAML field or require changes to existing OLSConfig manifests.

### OLS Configuration (spec.ols)

#### Core Fields

14. `spec.ols.defaultModel` -- `string`, required. The default model name for usage.
15. `spec.ols.defaultProvider` -- `string`, required. The default provider name for usage.
16. `spec.ols.logLevel` -- `LogLevel` enum, optional. Values: `DEBUG`, `INFO`, `WARNING`, `ERROR`, `CRITICAL`. Default: `INFO`.

#### Guardrails (spec.ols.guardrails) [PLANNED: OLS-3928]

16a. Guardrail configuration MUST conform to `openshift/ols/.ai/spec/what/tool-result-inspection.md`. `spec.ols.guardrails` is the optional cluster-wide API surface.

16b. `spec.ols.guardrails.toolResultInspection.enabled` is an optional boolean with a default value of `true`.

16c. One effective value controls the Classic service and DeepAgents sandboxes.

16d. The operator must publish the effective value even when the administrator omits the field.

16e. Write access follows the existing cluster-administrator access policy for the cluster-scoped `OLSConfig` resource.

#### Conversation Cache (spec.ols.conversationCache)

17. `spec.ols.conversationCache.type` -- `CacheType` enum. Only valid value: `postgres`. Default: `postgres`.
18. `spec.ols.conversationCache.postgres.sharedBuffers` -- `string`, XIntOrString. Default: `"256MB"`.
19. `spec.ols.conversationCache.postgres.maxConnections` -- `int`. Default: `2000`. Minimum=1, Maximum=262143.

#### Deployment Configuration (spec.ols.deployment)

The deployment config uses two struct types:

- **`Config`**: has `replicas`, `resources`, `tolerations`, `nodeSelector`. Affinity and topology spread constraints are intentionally omitted to keep the CRD OpenAPI schema under the Kubernetes annotation size limit (controller-gen inlines `Config` per operand).
- **`ContainerConfig`**: has `resources` only.

Field path (relative to `spec.ols.deployment`) | JSON key | Go type | Notes
---|---|---|---
`api` | `api` | `Config` | API container. Replicas configurable (default 1, min 0)
`dataCollector` | `dataCollector` | `ContainerConfig` | Data collector container. Resources only
`mcpServer` | `mcpServer` | `Config` | Standalone OpenShift MCP server Deployment (replicas, resources, tolerations, nodeSelector)
`rhokp` | `rhokp` | `Config` | Standalone RHOKP Deployment (Solr / OKP). Replicas (forced to 1), resources, tolerations, nodeSelector
`console` | `console` | `Config` | Console container. Has replicas field but operator forces 1
`database` | `database` | `Config` | Database container. Has replicas field but operator forces 1
`alertsAdapter` | `alertsAdapter` | `AlertsAdapterSpec` | Agentic alerts adapter deployment and user-managed runtime config reference. Replicas forced to 1

`AlertsAdapterSpec` embeds `Config` (deployment scheduling/resources) and optional `configMapRef` (`LocalObjectReference`). Setting `configMapRef` **enables** the alerts adapter operand. The referenced ConfigMap name is `configMapRef.name` (commonly `alerts-adapter-config`; see [adapter manifests](https://github.com/openshift/lightspeed-agentic-alerts-adapter/tree/main/manifests)). The operator does not create or validate ConfigMap content. When the ConfigMap exists, it is mounted at `/etc/alerts-adapter`; when absent, no config volume is mounted. The adapter reads `config.yaml` from that path and uses built-in defaults when the file is missing or invalid.
`agenticConsole` | `agenticConsole` | `Config` | Agentic console plugin container. Replicas forced to 1
`otelCollector` | `otelCollector` | `Config` | OTEL Collector container ([OLS-3510](https://redhat.atlassian.net/browse/OLS-3510)). Replicas forced to 1

20. Replicas are user-configurable for the API container (`spec.ols.deployment.api.replicas`). For console, database, alerts adapter, agentic console, otel collector, and RHOKP, the operator always overrides replicas to 1 regardless of spec value.

##### Config Fields

Field path (relative to Config) | JSON key | Go type | Default | Validation
---|---|---|---|---
`replicas` | `replicas` | `*int32` | `1` | Minimum=0
`resources` | `resources` | `*corev1.ResourceRequirements` | (none) | Standard k8s resource requirements
`tolerations` | `tolerations` | `[]corev1.Toleration` | (none) | Standard k8s tolerations
`nodeSelector` | `nodeSelector` | `map[string]string` | (none) | Key-value label selector

##### ContainerConfig Fields

Field path (relative to ContainerConfig) | JSON key | Go type
---|---|---
`resources` | `resources` | `*corev1.ResourceRequirements`

#### Query Filters (spec.ols.queryFilters)

21. Type: `[]QueryFiltersSpec`. Each entry has:

Field | JSON key | Go type | Required
---|---|---|---
`name` | `name` | `string` | No
`pattern` | `pattern` | `string` | No
`replaceWith` | `replaceWith` | `string` | No

#### User Data Collection (spec.ols.userDataCollection)

22. `spec.ols.userDataCollection.feedbackDisabled` -- `bool`, optional. Disables user feedback collection.
23. `spec.ols.userDataCollection.transcriptsDisabled` -- `bool`, optional. Disables transcript collection.
23a. [PLANNED: OLS-3569] The operator reuses `transcriptsDisabled` as the Agentic collection opt-out and adds no CRD field. Subject to the parent-defined Agentic v2 bundle availability and telemetry credentials, `false` or absent permits the conditional Collector resources; `true` omits them. `feedbackDisabled` is not part of the Agentic gate. See `agentic-data-collection.md`.

#### TLS Configuration (spec.ols.tlsConfig)

24. `spec.ols.tlsConfig` -- `*TLSConfig`, optional. Pointer type (nil when absent).
25. `spec.ols.tlsConfig.keyCertSecretRef` -- `corev1.LocalObjectReference`. Secret must contain keys: `tls.crt` (required), `tls.key` (required), `ca.crt` (optional, for console proxy trust).

#### Additional CA (spec.ols.additionalCAConfigMapRef)

26. `spec.ols.additionalCAConfigMapRef` -- `*corev1.LocalObjectReference`, optional. ConfigMap with additional CA certificates for LLM provider TLS.

#### TLS Security Profile (spec.ols.tlsSecurityProfile)

27. `spec.ols.tlsSecurityProfile` -- `*configv1.TLSSecurityProfile`, optional. OpenShift TLS security profile for API endpoints.

#### Introspection (spec.ols.introspectionEnabled)

28. `spec.ols.introspectionEnabled` -- `*bool`, optional. Default: `true` when absent. Enables introspection features (built-in OpenShift MCP server).

#### Service audit events (spec.ols.auditEventsEnabled)

29. `spec.ols.auditEventsEnabled` -- `*bool`, optional. Default: **`true`** when absent. Controls structured compliance audit JSON on stdout by lightspeed-service.
30. Maps to `audit.logging: Enabled|Disabled` in generated `olsconfig.yaml`. Independent of `spec.audit.logging` (collector Postgres pipeline).

#### MCP Kubernetes Server (spec.ols.mcpKubeServerConfig)

31. `spec.ols.mcpKubeServerConfig.timeout` -- `int`. Default: `60`. Minimum=5. App-server client timeout in seconds for the built-in MCP Kubernetes server; not a server-runtime setting.

##### Typed Toolset Selection

`spec.ols.mcpKubeServerConfig.toolsets` is an optional array of typed single-toolset objects. It is a **replacement selection**, not an additive list.

| Input | Effective selection |
|---|---|
| `toolsets` absent, including absent `mcpKubeServerConfig` | Existing operator defaults: `core`, `config`, `helm`, `observability/metrics`, `kubevirt` |
| Nonempty `toolsets` | Exactly the named entries; do not add defaults or implicit dependencies |
| `toolsets: []` | No toolsets; do not fall back to defaults |

Example selection and CA-reference shape:

```yaml
spec:
  ols:
    mcpKubeServerConfig:
      toolsets:
        - ossm:
            url: https://kiali.example.com
            caBundleRef:
              configMap:
                name: kiali-ca
                key: ca.crt
        - netobserv:
            namespace: netobserv
            service: netobserv-plugin
            port: 9001
        - core: {}
        - tekton: {}
```

- MaxItems=17. Each entry contains exactly one recognized toolset key with an object value (MinProperties=1, MaxProperties=1 and CEL singleton validation); CEL also rejects duplicate toolsets. Empty or unknown-only entries are rejected after pruning. Requests must use `fieldValidation=Strict` to reject unknown typed configuration fields (including `insecure`) or mixed recognized/unknown selection keys: otherwise Kubernetes prunes unknown fields before CEL can inspect them. The ten empty-only blocks are maps with MaxProperties=0, so populated blocks are rejected even without Strict. The [versioned inventory](../how/config-generation.md#ocp-mcp-toolset-inventory--ols-2715-design-input) contains 18 compiled toolsets; the 17 other than `openshift/mustgather` are in scope. Reject `openshift/mustgather` selection with a clear unsupported-toolset error; do not expose its configuration or an archive/PVC API in OLS-2715.
- A configuration-free toolset is selected as `<name>: {}`. For configurable toolsets, `{}` is allowed only when that toolset's validation contract permits omission of all settings; it must not bypass required configuration.
- Settings are typed per toolset and colocated with selection. There is no separate `additionalToolsets` field or freeform TOML passthrough. CR key `ossm` maps to server selection `ossm` and configuration table `kiali`.
- Preserve absent versus explicitly empty arrays through API decoding, serialization, deepcopy and reconciliation. Do not admission-default this field to the operator list or use a length-zero check to choose defaults.
- `introspectionEnabled` remains the operand gate. Empty selection does not remove the Deployment, Service or client/handoff wiring. Selection does not grant permissions, override security/compatibility filters, or install dependencies.
- Endpoint URLs have MaxLength=2048, require absolute HTTPS with a host and no userinfo. NetObserv namespace/Service fields have MaxLength=63 and DNS-label patterns (Service starts with a letter). Helm allowlists have MaxItems=64, each URL MaxLength=2048, HTTPS/OCI with a host and no userinfo.
- Typed supported-toolset fields/defaults, structural schema/CEL validation and shared CA generation are implemented below. Local pinned-image execution with actual `GenerateConfigTOML` output passed default/empty/seven-block startup and static Prometheus/Loki/Tempo HTTPS scenarios; separate full-manager live cluster tests also passed default/empty/core replacement selection and Strict server dry-run validation. [Runtime verification](ocpmcp.md#runtime-verification) distinguishes these passed scopes from remaining [PLANNED: OLS-2715] authenticated backend/Route/tool-functionality proofs. No additional security-policy surface is introduced.

##### Toolset CA Bundle Reference

The OSSM and NetObserv typed blocks use `caBundleRef` for explicit backend trust. It is a keyed, local ConfigMap-or-Secret reference, not a filesystem path. It is required for OSSM and NetObserv's explicit-URL form; NetObserv's service form may omit it to use operator-provisioned cluster service-CA trust, as specified below. The reference does not automatically add trust settings to other toolsets whose server implementations lack a corresponding field.

```yaml
caBundleRef:
  configMap:
    name: kiali-ca
    key: ca.crt
```

Alternatively:

```yaml
caBundleRef:
  secret:
    name: kiali-ca
    key: ca.crt
```

| Field | Shape | Validation |
|---|---|---|
| `configMap` | Optional object with `name`, `key` | Exactly one of `configMap` / `secret` |
| `secret` | Optional object with `name`, `key` | Exactly one of `configMap` / `secret` |
| Selected source `name` | Required string | DNS subdomain pattern; MinLength=1, MaxLength=253 |
| Selected source `key` | Required string | Pattern `^[-._a-zA-Z0-9]+$`; MinLength=1, MaxLength=253; no key default |

- No `namespace`, `optional`, arbitrary path, or generic volume-source fields. Resolve sources in the operand namespace, even though OLSConfig is cluster-scoped.
- Admission validates the discriminated reference shape and names/keys. Reconciliation validates object existence, selected-key existence and a valid certificate bundle; failures produce a clear error rather than silently falling back to insecure TLS.
- Selected keys must contain only nonempty, valid X.509 certificate PEM. Reconciliation canonicalizes and copies only public certificate PEM into operator-owned `openshift-mcp-server-trust`; MCP projects selected snapshot keys read-only, never live user sources. Valid content or reference-identity changes roll MCP; invalid changes retain the last validated snapshot and do not roll it. Sources remain user-owned and are never deleted on selection changes, disable or finalization.
- CA references cannot select reserved MCP ConfigMaps `openshift-mcp-server-config`, `openshift-mcp-server-trust`, `openshift-mcp-server-ca`, or serving Secret `openshift-mcp-server-tls`. Enabled reconciliation rejects these references; cleanup still protects referenced sources. Colliding unowned runtime/trust/legacy ConfigMaps are not adopted, overwritten or deleted, even after CA reference removal; deletion requires OLSConfig ownership. Serving TLS Secret cleanup requires OLSConfig ownership or a v1 Service owner reference matching the originating OLSConfig-owned Service name/UID, checked before Service deletion; unowned outputs remain protected.
- See [TLS contract](tls.md#mcp-toolset-backend-ca-bundles) and [generation/lifecycle](../how/config-generation.md#toolset-ca-reference-generation).

##### Shared MCP CA Bundles

`spec.ols.mcpKubeServerConfig.caBundleRefs` is an optional array of the same typed ConfigMap-or-Secret name/key references used by `caBundleRef`. It augments baseline trust; it does not replace the baseline or select/enable toolsets.

```yaml
spec:
  ols:
    mcpKubeServerConfig:
      caBundleRefs:
        - configMap:
            name: observability-ca
            key: ca.crt
        - secret:
            name: private-backend-ca
            key: ca.crt
      toolsets:
        - observability/logs: {}
```

- Baseline trust preserves image/system roots and adds the validated cluster service CA. Omitted `caBundleRefs` or `[]` means no user-provided bundles; baseline trust remains. Unlike `toolsets`, this list has augmenting, not replacement, semantics.
- MaxItems=64. Each entry has exactly one `configMap` or `secret`, required explicit name/key, local operand-namespace resolution and no optional-missing flag/path. Admission validates shape; reconciliation validates sources, selected keys and certificate bundles.
- User bundles are shared MCP-process trust for clients using system roots, not endpoint-isolated trust and not a claim that every toolset consumes system roots. This can affect other system-root consumers, including clients beyond observability; document that scope rather than imply per-toolset isolation.
- OSSM/NetObserv singular `caBundleRef` remains their explicit client setting and is not replaced by this list. Shared roots may also affect clients that append to system roots; clients that replace their roots can behave differently, as recorded in the source inventory.
- Sources stay user-owned. Validate changes and roll MCP on valid bundle changes; removing an entry removes its user-provided contribution without deleting its source or baseline roots. Missing sources/keys or invalid bundles are clear reconciliation errors, never a reason to disable TLS verification.
- Trust configuration is independent of selection. `toolsets: []` retains the MCP operand and baseline trust; `introspectionEnabled=false` still disables the operand, without deleting referenced bundles. References are consumed by the managed MCP workload when that operand is enabled.
- The implemented snapshot mounts shared roots at `/etc/mcp-server/ca`, sets `SSL_CERT_DIR=/etc/ssl/certs:/etc/pki/tls/certs:/etc/mcp-server/ca`, and leaves `SSL_CERT_FILE` unset, preserving default image certificate files. Local pinned-image static Prometheus/Loki/Tempo HTTPS/custom trust and unknown-CA/hostname rejection passed. First-harness metrics verify shared synthetic service roots, populated/CAData-only REST roots and restart-based root rotation/removal; these are not live Service CA injection or hot reload. Inspection confirms 146 image bundle certificates retained. Separate full production `bin/manager` live tests verify public projection/checksums/private-key nonprojection, source events, content/reference-identity PodUID rollouts, invalid-input retention/recovery and real Service CA injection. Namespace Python HTTPS metric value 2715 succeeds with custom shared roots; removal/rotation fails TLS before extra HTTP, updated roots roll MCP, and real Service CA-only baseline succeeds. External public-root HTTPS remains [PLANNED: OLS-2715]; neither local restart tests nor live controller rolls establish hot reload or production deployment. See [runtime matrix](ocpmcp.md#verification-matrix). See [TLS contract](tls.md#shared-mcp-process-trust) and [generation](../how/config-generation.md#shared-mcp-trust-generation).

##### OSSM Configuration

| Field under `toolsets[].ossm` | Type | Required / default | Validation |
|---|---|---|---|
| `url` | string | Required; no discovery/default | Valid absolute HTTPS endpoint with nonempty host; reject HTTP/non-HTTPS schemes |
| `caBundleRef` | Typed CA reference | Required | Shared ConfigMap-or-Secret reference contract above |

`ossm: {}` is invalid. Do not expose `insecure`, arbitrary certificate paths or separate credentials. The operator generates verified HTTPS settings and maps this CR block to `toolset_configs.kiali`; authentication uses the server's derived caller credentials. Per-toolset selection does not change global authentication policy.

##### NetObserv Configuration

NetObserv has mutually exclusive explicit-URL and service-based endpoint forms.

| Field under `toolsets[].netobserv` | Type | Required / effective default | Validation |
|---|---|---|---|
| `url` | Optional string | If present, selects explicit-URL form | Valid absolute HTTPS endpoint with nonempty host; reject HTTP/non-HTTPS schemes |
| `namespace` | Optional string | Service form: `netobserv` | Valid nonempty Kubernetes namespace name; forbidden with `url` |
| `service` | Optional string | Service form: `netobserv-plugin` | Valid nonempty Kubernetes Service name; forbidden with `url` |
| `port` | Optional integer | Service form: `9001` | 1–65535 when supplied; forbidden with `url` |
| `caBundleRef` | Optional typed CA reference | Required with `url`; service form defaults to cluster service CA if omitted | Shared reference contract; allowed in either form |

- With `url`, reject explicitly supplied `namespace`, `service` or `port`, even if their values equal service defaults. Never silently ignore conflicting settings. Do not use unconditional CRD defaults on these fields: compute service defaults only after choosing the form, preserving field presence for validation.
- Without `url`, generate `https://<service>.<namespace>.svc.cluster.local:<port>` using supplied values or service defaults. `netobserv: {}` is valid and means the default service endpoint with operator-provisioned cluster service-CA trust.
- In service form, an explicit `caBundleRef` overrides the implicit cluster service-CA source. Otherwise validate and mount `openshift-service-ca.crt` / `service-ca.crt` from the operand namespace. Missing/invalid trust is a reconciliation error; no insecure or system-trust fallback for that error.
- Do not expose `insecure`, arbitrary certificate paths or separate credentials. Always generate `insecure = false` and an explicit CA path; do not rely on upstream OpenShift detection or opportunistic CA-file discovery.
- Backend namespace identifies the destination Service, not the namespace of a CA reference. CA references remain local to the operand namespace. Selection does not install or grant access to NetObserv.

##### Helm Configuration

| Field under `toolsets[].helm` | Type | Required / effective default | Validation |
|---|---|---|---|
| `storageDriver` | Optional string | `configmap` for explicit Helm selection | Only `configmap` supported; reject `secret` while OLS Secret denial is mandatory |
| `allowedRegistries` | Optional string array | Empty: no registry allowlist | Each entry must have scheme/host; HTTPS or OCI only; enforce upstream normalized URL/path-prefix allowlist semantics |

`helm: {}` is valid and generates ConfigMap-backed release storage. This is a default for the new explicit selection path, not a change to omitted `toolsets`: omission retains today's generated config, including no Helm table and the image's Secret-backed storage default. ConfigMap storage does not grant access or change denied-resource rules. Registry credentials/CA/local chart mounts are not added by these fields; any additional integration needs remain separate design work.

##### Observability Metrics Configuration

| Field under `toolsets[].observability/metrics` | Type | Required / effective default | Validation |
|---|---|---|---|
| `prometheusURL` | Optional string | OLS in-cluster Thanos URL when absent | Valid absolute HTTPS endpoint with nonempty host; reject HTTP/non-HTTPS schemes |
| `alertmanagerURL` | Optional string | OLS in-cluster Alertmanager URL when absent | Valid absolute HTTPS endpoint with nonempty host; reject HTTP/non-HTTPS schemes |
| `authMode` | Optional string | `header` | Enum `header`, `kubeconfig`; no independent credential fields |
| `guardrails` | Optional string | `!tsdb` when `prometheusURL` absent; `all` with an explicit Prometheus URL | MaxLength=512; validate the complete upstream grammar in the [inventory](../how/config-generation.md#metrics); explicit value overrides the conditional default |
| `maxMetricCardinality` | Optional integer | Server's `20000` when relevant and absent | Positive; explicit limit requires effective `max-metric-cardinality` guardrail |
| `maxLabelCardinality` | Optional integer | Server's `500` when relevant and absent | Nonnegative; explicit limit requires effective `disallow-blanket-regex` guardrail; zero means always reject blanket regex |
| `rangeQueryFullResponse` | Optional boolean | `false` | Explicit false and true supported |

- `observability/metrics: {}` preserves the useful OLS endpoint/guardrail configuration: `https://thanos-querier.openshift-monitoring.svc.cluster.local:9091`, `https://alertmanager-main.openshift-monitoring.svc.cluster.local:9094`, `!tsdb`. It does not fall back to upstream localhost.
- Default each endpoint independently when absent. Supplying only `alertmanagerURL` does not change the Prometheus guardrail default. An explicit `prometheusURL` chooses `all` unless `guardrails` is supplied; this is a presence-based rule, not URL-string equivalence or backend detection.
- Validate cardinality settings against the effective guardrails, including conditional defaults. For example, an explicit `maxMetricCardinality` with default `!tsdb` is invalid because its guardrail is disabled. Do not inject cardinality fields when absent merely to materialize inactive defaults; preserve explicit zero for the label limit.
- Do not unconditionally admission-default `guardrails`: its default depends on Prometheus URL presence. Preserve presence through API round trips and compute defaults in generation/validation.
- `header` and `kubeconfig` select upstream handler-context versus derived REST credentials, not operator privileges. Actual metric/log/trace calls in **both** modes use caller token when a bearer header exists because `Manager.Derived` replaces REST BearerToken. Without that header, `header` sends an anonymous backend request; `kubeconfig` uses the supplied synthetic kubeconfig token. These upstream wrinkles do not establish static identity isolation, mandatory/global authentication or real backend/Kubernetes RBAC; permissive mocks accept anything, with no TokenReview/SubjectAccessReview. See [auth observations](ocpmcp.md#authentication-observations); no code/security-policy expansion is implied.
- Metrics endpoints support verified HTTPS only. No `insecure` option is exposed; generate `insecure = false` for explicit metrics selections and reject insecure configuration rather than bypassing verification. Custom backend trust uses shared `mcpKubeServerConfig.caBundleRefs`, not a nonexistent vendor per-toolset CA field. The shared system-trust mechanism passed actual local pinned-image Prometheus HTTPS calls and separate full-manager live namespace HTTPS metric trust/removal/rotation/real Service CA-baseline checks, not public-root endpoint or authenticated production-backend auth/RBAC proof; OSSM/NetObserv-specific CA mapping is not a substitute.

##### Observability Logs and Traces Endpoint Configuration

| Block | Static endpoint field | Static endpoint contract | Discovery when endpoint absent |
|---|---|---|---|
| `observability/logs` | Optional `lokiURL` string | Valid absolute HTTPS endpoint with nonempty host | LokiStack/Route discovery using invocation instance selectors |
| `observability/traces` | Optional `tempoURL` string | Valid absolute HTTPS API base with nonempty host | Tempo instance/Route discovery using invocation instance selectors |

Both blocks expose optional string `authMode`, default `header`, enum `header` / `kubeconfig`. Reject explicit empty/unknown values; no independent token, username/password or credential-reference fields. Header mode uses handler-context credentials; kubeconfig mode uses the derived Kubernetes REST credentials, not an automatic grant of the operator's identity. This matches metrics, including the observed caller-token-in-both-modes and anonymous-header-without-token wrinkles above; it does not change global authentication or caller authorization policy.

Both blocks expose optional `useRoute` only for discovery, with effective value `true`. Reject `false`: service-DNS discovery can generate HTTP in this server version, so accepting it would violate the HTTPS-only contract. Reject a static endpoint combined with explicitly supplied `useRoute`, even `true`, instead of silently ignoring the setting. Do not unconditionally admission-default `useRoute`, which would create that conflict for static endpoints. Runtime generation emits `use_route = true` for discovery.

```yaml
toolsets:
  - observability/logs: {}
  - observability/traces:
      tempoURL: https://tempo.example.com/api/traces/v1/application/tempo
```

- Empty blocks select route-based discovery, not a hardcoded instance. Namespace/name/tenant and query selectors remain tool invocation arguments, not new CR settings. Static Tempo URLs must include the intended API base; upstream does not complete a tenant path from the invocation argument in that form.
- No `insecure` option. Generate verified TLS and reject HTTP/static non-HTTPS schemes. Do not rewrite discovered HTTP URLs to HTTPS or silently fall back to Service endpoints when Route discovery fails.
- Endpoint selection does not establish trust, Route availability or authorization. These vendor clients lack per-toolset CA-file settings; use shared MCP `caBundleRefs` to augment process trust. The OSSM/NetObserv `caBundleRef` mapping cannot simply be reused as a vendor TOML field.
- Existing compatibility filtering remains independent: logs needs LokiStack API, traces needs TempoStack API even for static endpoints or Monolithic-only installations in this pin. Discovery can additionally require both Tempo resource kinds. Document/test these limits; no implicit disabling of global filters.
- Actual serializer-generated static Loki/Tempo HTTPS calls passed with shared custom trust, functional synthetic log/trace outputs and both auth-mode sourcing classifications; unknown CA and wrong hostname both fail before HTTP. Mock LokiStack/TempoStack discovery enables tools, but real Route endpoint discovery/stacks, live authenticated Prometheus/Loki/Tempo backend auth/RBAC (including negative authorization) and external public-root HTTPS remain [PLANNED: OLS-2715]. Bounded real Kubernetes caller RBAC passed separately, not live logs/traces backend authorization. See [runtime verification](ocpmcp.md#runtime-verification).

##### CNI Diagnostics Configuration

| Field under `toolsets[].cni-diagnostics` | Type | Effective default | Validation |
|---|---|---|---|
| `kernelDebugImage` | Optional string | `nicolaka/netshoot:v0.16` | Nonempty syntactically valid container image reference when supplied |
| `tcpdumpImage` | Optional string | `nicolaka/netshoot:v0.16` | Nonempty syntactically valid container image reference when supplied |
| `pwruImage` | Optional string | `docker.io/cilium/pwru:v1.0.10` | Nonempty syntactically valid container image reference when supplied |

- Each supplied image has MaxLength=512 and a bounded reference pattern accepting conventional repositories, optional registry/port, tag and SHA-256 digest; exotic registry authorities and non-SHA-256 digests are unsupported.
- `cni-diagnostics: {}` is valid and uses the pinned upstream defaults. Explicit empty/invalid image strings are rejected rather than silently replaced. Defaults apply only to an explicitly selected CNI block; omission of `toolsets` does not add CNI.
- Overrides permit mirrored/custom images for disconnected deployments. Syntax validation does not prove image availability, pull permission, executable compatibility or kernel/eBPF capability. The operator does not build, mirror or install these helper images under this contract.
- These are images for diagnostic workloads, not an override of the MCP server image. Node diagnostics can create privileged host-access pods using caller permissions; selecting CNI does not grant RBAC/SCC or change the MCP Deployment's restricted security context.
- Pod-target tcpdump execs into an existing container: its image field does not install the capture binary there. Packet/host output is not sanitized by Kubernetes Secret/RBAC denied-resource rules. Source annotations let these tools survive destructive filtering even though execution uses privileged/exec machinery; the agreed policy keeps `read_only=false`, `disable_destructive=true`, existing resource denial and compatibility filters, without new privilege grants or per-toolset security controls.

##### Configuration-free Toolsets

These ten selections accept only an empty object and expose no configuration fields:

| Selection key | Relevant pinned-runtime limitation |
|---|---|
| `core` | Destructive-annotated writes remain filtered; operation-specific APIs and caller permissions apply |
| `config` | Normally no tools in single-target OLS: target-list tools are filtered and `configuration_view` remains disabled |
| `kcp` | Selection does not configure a kcp provider or supply workspace endpoints; useful operation requires an appropriate target |
| `kubevirt` | Requires virtualization APIs/workloads; several mutating tools remain filtered, while prompts have separate availability |
| `tekton` | Requires Tekton APIs/definitions; destructive PipelineRun lifecycle remains filtered |
| `cluster-diagnostics` | Sole `nodes_debug_exec` tool is destructive and currently filtered, leaving zero tools/prompts |
| `netedge` | Existing workloads, discovery, image access and caller permissions apply; monitoring uses a separate auth/TLS client |
| `oadp` | Prompt-only in this pin; selecting it does not establish that the consuming OLS client invokes prompts |
| `ovn-kubernetes` | Existing OVN/OVS workloads, expected containers/binaries and caller pod-exec permissions required |
| `observability/otelcol` | Embedded schemas need no user settings; current compatibility predicate requires Collector CRD even for local schema tools |

Example:

```yaml
toolsets:
  - core: {}
  - cluster-diagnostics: {}
  - observability/otelcol: {}
```

- All these selections are valid even if filters/prerequisites leave no usable tools. Do not reject a valid selection for absent installation, prompt-consumption limitations or zero exposed tools; do not install dependencies, add implicit selections or relax filters. These are documented runtime wrinkles, not OLS-2715 admission blockers.
- No arbitrary nested fields or global/provider settings belong in these blocks. Generate selection only, without unregistered TOML config tables; Collector schemas use the server's embedded defaults rather than exposing `SchemaFS`.
- Keep `read_only=false` and `disable_destructive=true` for every selection form, including omitted/default and explicit-empty selection. No new security-policy controls are introduced. Existing denied resources, compatibility filtering, disabled tools, restricted MCP pod context and caller authorization remain in force.

#### Proxy Configuration (spec.ols.proxyConfig)

32. `spec.ols.proxyConfig.proxyURL` -- `string`, optional. Pattern: `^https?://.*$`. If unset, cluster-wide proxy is used via `https_proxy` env var.
33. `spec.ols.proxyConfig.proxyCACertificate` -- `*ProxyCACertConfigMapRef`, optional. Struct type `atomic`.

`ProxyCACertConfigMapRef` fields:
- Inline `corev1.LocalObjectReference` (provides `name` field for the ConfigMap name)
- `key` -- `string`. Default: `"proxy-ca.crt"`. Key within the ConfigMap holding the proxy CA certificate.

#### RAG Configuration (spec.ols.rag)

34. Type: `[]RAGSpec`, optional.

Field | JSON key | Go type | Required | Default
---|---|---|---|---
`image` | `image` | `string` | Yes | (none)
`indexPath` | `indexPath` | `string` | No | `"/rag/vector_db"`
`indexID` | `indexID` | `string` | No | `""`

#### Quota Handlers (spec.ols.quotaHandlersConfig)

35. `spec.ols.quotaHandlersConfig` -- `*QuotaHandlersConfig`, optional.
36. `spec.ols.quotaHandlersConfig.limitersConfig` -- `[]LimiterConfig`.
37. `spec.ols.quotaHandlersConfig.enableTokenHistory` -- `bool`, optional.

`LimiterConfig` fields:

Field | JSON key | Go type | Required | Validation
---|---|---|---|---
`name` | `name` | `string` | Yes (by convention) | None
`type` | `type` | `string` | Yes (by convention) | Enum: `cluster_limiter`, `user_limiter`
`initialQuota` | `initialQuota` | `int` | Yes (by convention) | Minimum=0
`quotaIncrease` | `quotaIncrease` | `int` | Yes (by convention) | Minimum=0
`period` | `period` | `string` | Yes (by convention) | Pattern: `^(1\s+(second\|minute\|hour\|day\|month\|year\|s\|min\|h\|d\|m\|y)\|([2-9][0-9]*\|[1-9][0-9]{2,})\s+(seconds\|minutes\|hours\|days\|months\|years\|s\|min\|h\|d\|m\|y))$`

38. Period pattern explanation: quantity 1 requires singular unit name or abbreviation; quantities >= 2 require plural unit name or abbreviation. Abbreviations (`s`, `min`, `h`, `d`, `m`, `y`) are accepted with any quantity.

#### Storage (spec.ols.storage)

39. `spec.ols.storage.size` -- `resource.Quantity`, optional. Size of the requested persistent volume.
40. `spec.ols.storage.class` -- `string`, optional. Storage class name.

#### Credential Hot-Reload (spec.ols.credentialHotReload) [OLS-3450]

40a. `spec.ols.credentialHotReload` -- `bool`, optional. Default: `false`. When `true`:
  - The operator does **not** annotate LLM credential secrets with `ols.openshift.io/watcher: cluster` (and removes existing annotations). This prevents the watcher from triggering pod restarts on LLM secret `.data` changes.
  - The operator writes `credential_hot_reload: true` into the generated `olsconfig.yaml`.
  - The service re-reads LLM credentials from disk on each request (instead of using startup-cached values). On read failure, the last good credential is retained.
  - A warning log is emitted during reconciliation when the flag is enabled.
  - Non-LLM secrets (TLS, MCP headers, system) are unaffected — they are always annotated and always trigger restarts.
  - Changing `credentialsSecretRef.name` in the CR still triggers a standard reconciliation and restart (volume mount change).

#### Boolean/String Fields

41. `spec.ols.byokRAGOnly` -- `bool`, optional. When true, only BYOK RAG sources are used: the operator does not deploy the standalone RHOKP operand, does not write `solr_hybrid` into `olsconfig.yaml`, and does not set `OCP_CLUSTER_VERSION` on the app-server pod.

#### Operator-managed OKP (not on CR)

OKP / Solr hybrid RAG has no `spec.ols.solrHybrid` (or similar) field. It is enabled by default and turned off only via `byokRAGOnly`. When active, the operator:
- deploys the standalone RHOKP Deployment/Service (`lightspeed-rhokp`) and writes `ols_config.solr_hybrid` with operator defaults (`https://lightspeed-rhokp.<ns>.svc:8443`, hybrid tuning);
- sets `OCP_CLUSTER_VERSION` on the app-server container for Solr version filtering;
- serves OCP product documentation via Solr hybrid only; `reference_content.indexes` lists BYOK FAISS indexes from `spec.ols.rag` only.

RHOKP standalone Deployment resources are overridable via `spec.ols.deployment.rhokp` (`Config`: replicas forced to 1, resources, tolerations, nodeSelector). Default resource requests: 2 CPU, 2 GiB memory. Storage: 75 GiB EmptyDir with sizeLimit.
42. `spec.ols.querySystemPrompt` -- `string`, optional. Custom system prompt for LLM queries. If unset, the default OpenShift Lightspeed prompt is used.
43. `spec.ols.maxIterations` -- `int`. Default: `5`. Minimum=1. Maximum number of iterations for agent execution.
44. `spec.ols.imagePullSecrets` -- `[]corev1.LocalObjectReference`, optional. Pull secrets for BYOK RAG images.

#### Tool Filtering (spec.ols.toolFilteringConfig)

45. `spec.ols.toolFilteringConfig` -- `*ToolFilteringConfig`, optional. Presence enables tool filtering; absence means all tools are used.

Field | JSON key | Go type | Default | Validation
---|---|---|---|---
`alpha` | `alpha` | `float64` | `0.8` | XValidation: must be >= 0.0 and <= 1.0. Weight for dense vs sparse retrieval (1.0 = full dense, 0.0 = full sparse)
`topK` | `topK` | `int` | `10` | Minimum=1, Maximum=50. Number of tools to retrieve
`threshold` | `threshold` | `float64` | `0.01` | XValidation: must be >= 0.0 and <= 1.0. Minimum similarity threshold

46. Tool filtering requires the `ToolFiltering` feature gate to be enabled in `spec.featureGates`.

#### Tools Approval (spec.ols.toolsApprovalConfig)

47. `spec.ols.toolsApprovalConfig` -- `*ToolsApprovalConfig`, optional.

Field | JSON key | Go type | Default | Validation
---|---|---|---|---
`approvalType` | `approvalType` | `ApprovalType` | `tool_annotations` | Enum: `never`, `always`, `tool_annotations`
`approvalTimeout` | `approvalTimeout` | `int` | `600` | Minimum=1. Timeout in seconds for user approval

48. `never`: all tools execute without approval. `always`: all tool calls require approval. `tool_annotations`: approval decision is per-tool based on annotations.

### Data Collector Configuration (spec.olsDataCollector)

49. `spec.olsDataCollector.logLevel` -- `LogLevel` enum. Default: `INFO`. Same enum as `spec.ols.logLevel`.

### MCP Server Configuration (spec.mcpServers)

50. Array of `MCPServerConfig`. MaxItems=20.

Field | JSON key | Go type | Required | Default | Validation
---|---|---|---|---|---
`name` | `name` | `string` | Yes | (none) | None
`url` | `url` | `string` | Yes | (none) | Pattern: `^https?://.*$`
`timeout` | `timeout` | `int` | No | `5` | None (no min/max markers)
`headers` | `headers` | `[]MCPHeader` | No | (none) | MaxItems=20

#### MCPHeader Fields

Field | JSON key | Go type | Required | Validation
---|---|---|---|---
`name` | `name` | `string` | Yes | MinLength=1, Pattern: `^[A-Za-z0-9-]+$`
`valueFrom` | `valueFrom` | `MCPHeaderValueSource` | Yes | Discriminated union (see below)

#### MCPHeaderValueSource Fields (discriminated union)

Field | JSON key | Go type | Required | Validation
---|---|---|---|---
`type` | `type` | `MCPHeaderSourceType` | Yes | Enum: `secret`, `kubernetes`, `client`. Union discriminator
`secretRef` | `secretRef` | `*corev1.LocalObjectReference` | Conditional | Required with non-empty `name` when `type == "secret"`. Must not be set when `type != "secret"`

51. XValidation: when `type == "secret"`, `secretRef` must be present with a non-empty `name`.
52. XValidation: when `type != "secret"` (i.e., `kubernetes` or `client`), `secretRef` must not be set.

### Status (status)

#### Conditions (status.conditions)

53. Type: `[]metav1.Condition`. Populated after first reconciliation.

Condition types used by the operator:
- `ApiReady` -- API server deployment health
- `CacheReady` -- PostgreSQL cache deployment health
- `ConsolePluginReady` -- Console UI plugin deployment health
- `AgenticConsolePluginReady` -- Agentic console plugin deployment health
- `OtelCollectorReady` -- OTEL Collector deployment health
- `MCPServerReady` -- Standalone OpenShift MCP server deployment health (`False`, `Reason=Disabled` when `introspectionEnabled` is false; does not block `OverallStatus=Ready`)
- `RHOKPReady` -- Standalone RHOKP deployment health (`Disabled` when `byokRAGOnly` is true; does not block `OverallStatus=Ready`)
- `AlertsAdapterReady` -- Agentic alerts adapter deployment health
- `ResourceReconciliation` -- Overall resource reconciliation status (set directly, not deployment-based)

#### Overall Status (status.overallStatus)

54. `status.overallStatus` -- `OverallStatus` enum. Values: `Ready`, `NotReady`. Aggregation of all component conditions. `Ready` only when all components are healthy.

#### Diagnostic Info (status.diagnosticInfo)

55. Type: `[]PodDiagnostic`, optional. Auto-populated during deployment failures, cleared on recovery.

`PodDiagnostic` fields:

Field | JSON key | Go type | Required | Description
---|---|---|---|---
`failedComponent` | `failedComponent` | `string` | Yes | Matches condition type (e.g., `"ApiReady"`, `"CacheReady"`)
`podName` | `podName` | `string` | Yes | Name of the failing pod
`containerName` | `containerName` | `string` | No | Container within the pod (empty for pod-level issues)
`reason` | `reason` | `string` | Yes | Failure reason (e.g., `ImagePullBackOff`, `CrashLoopBackOff`, `Unschedulable`, `OOMKilled`)
`message` | `message` | `string` | Yes | Detailed error from Kubernetes
`exitCode` | `exitCode` | `*int32` | No | Exit code for terminated containers only
`type` | `type` | `DiagnosticType` | Yes | Enum: `ContainerWaiting`, `ContainerTerminated`, `PodScheduling`, `PodCondition`
`lastUpdated` | `lastUpdated` | `metav1.Time` | Yes | Timestamp of diagnostic collection

## Configuration Surface

Complete field reference. All paths are relative to the OLSConfig object.

Path | Type | Default | Required | Validation | Description
---|---|---|---|---|---
`spec` | `OLSConfigSpec` | -- | Yes | -- | Top-level spec
`spec.llm` | `LLMSpec` | -- | Yes | -- | LLM settings
`spec.llm.providers` | `[]ProviderSpec` | -- | Yes | MaxItems=10 | LLM providers
`spec.llm.providers[].name` | `string` | -- | Yes | -- | Provider name
`spec.llm.providers[].url` | `string` | -- | No | Pattern `^https?://.*$` | Provider API URL
`spec.llm.providers[].credentialsSecretRef` | `LocalObjectReference` | -- | Yes | -- | Secret with credentials
`spec.llm.providers[].models` | `[]ModelSpec` | -- | Yes | MaxItems=50 | Models
`spec.llm.providers[].models[].name` | `string` | -- | Yes | -- | Model name
`spec.llm.providers[].models[].url` | `string` | -- | No | Pattern `^https?://.*$` | Model API URL
`spec.llm.providers[].models[].contextWindowSize` | `uint` | -- | No | Min=1024 | Context window (tokens)
`spec.llm.providers[].models[].parameters` | `ModelParametersSpec` | -- | No | -- | Model parameters
`spec.llm.providers[].models[].parameters.maxTokensForResponse` | `int` | -- | No | -- | Max response tokens
`spec.llm.providers[].models[].parameters.toolBudgetRatio` | `float64` | `0.25` | No | Min=0.1, Max=0.5 | Tool token budget ratio
`spec.llm.providers[].models[].parameters.reasoningConfig` | `map[string]runtime.RawExtension` | -- | No | -- | Provider-specific reasoning/thinking params
`spec.llm.providers[].type` | `string` | -- | Yes | Enum (see rule 7; includes `bedrock`) | Provider type
`spec.llm.providers[].deploymentName` | `string` | -- | No | XValidation (rule 8) | Azure deployment name
`spec.llm.providers[].apiVersion` | `string` | -- | No | -- | Azure API version
`spec.llm.providers[].projectID` | `string` | -- | No | XValidation (rule 9) | Watsonx project ID
`spec.llm.providers[].googleVertexConfig` | `*VertexConfig` | -- | No | XValidation (rules 11, 13) | Google Vertex config
`spec.llm.providers[].googleVertexConfig.projectID` | `string` | -- | No | -- | Google Cloud project ID
`spec.llm.providers[].googleVertexConfig.location` | `string` | -- | No | -- | Server region location
`spec.llm.providers[].googleVertexAnthropicConfig` | `*VertexConfig` | -- | No | XValidation (rules 12, 14) | Google Vertex Anthropic config
`spec.llm.providers[].googleVertexAnthropicConfig.projectID` | `string` | -- | No | -- | Google Cloud project ID
`spec.llm.providers[].googleVertexAnthropicConfig.location` | `string` | -- | No | -- | Server region location
`spec.llm.providers[].fakeProviderMCPToolCall` | `bool` | -- | No | -- | Fake provider MCP flag
`spec.llm.providers[].tlsSecurityProfile` | `*TLSSecurityProfile` | -- | No | -- | Provider TLS profile
`spec.llm.providers[].credentialKey` | `string` | -- | No | XValidation (rule 10) | Secret key name
`spec.ols` | `OLSSpec` | -- | Yes | -- | OLS settings
`spec.ols.defaultModel` | `string` | -- | Yes | -- | Default model name
`spec.ols.defaultProvider` | `string` | -- | Yes | -- | Default provider name
`spec.ols.logLevel` | `LogLevel` | `INFO` | No | Enum: DEBUG/INFO/WARNING/ERROR/CRITICAL | Log level
`spec.ols.guardrails` | `GuardrailsSpec` | -- | No | -- | Cluster-wide guardrail configuration
`spec.ols.guardrails.toolResultInspection` | `ToolResultInspectionSpec` | -- | No | -- | Tool-result inspection configuration
`spec.ols.guardrails.toolResultInspection.enabled` | `*bool` | `true` | No | -- | Enable LLM inspection in Classic OLS and DeepAgents
`spec.ols.conversationCache` | `ConversationCacheSpec` | -- | No | -- | Cache config
`spec.ols.conversationCache.type` | `CacheType` | `postgres` | No | Enum: `postgres` | Cache type
`spec.ols.conversationCache.postgres` | `PostgresSpec` | -- | No | -- | Postgres settings
`spec.ols.conversationCache.postgres.sharedBuffers` | `string` | `"256MB"` | No | XIntOrString | Shared buffers
`spec.ols.conversationCache.postgres.maxConnections` | `int` | `2000` | No | Min=1, Max=262143 | Max connections
`spec.ols.deployment` | `DeploymentConfig` | -- | No | -- | Deployment overrides
`spec.ols.deployment.api` | `Config` | -- | No | -- | API container
`spec.ols.deployment.api.replicas` | `*int32` | `1` | No | Min=0 | API replicas (user-configurable)
`spec.ols.deployment.api.resources` | `*ResourceRequirements` | -- | No | -- | API resources
`spec.ols.deployment.api.tolerations` | `[]Toleration` | -- | No | -- | API tolerations
`spec.ols.deployment.api.nodeSelector` | `map[string]string` | -- | No | -- | API node selector
`spec.ols.deployment.dataCollector` | `ContainerConfig` | -- | No | -- | Data collector container
`spec.ols.deployment.dataCollector.resources` | `*ResourceRequirements` | -- | No | -- | Data collector resources
`spec.ols.deployment.mcpServer` | `Config` | -- | No | -- | Standalone OpenShift MCP server Deployment
`spec.ols.deployment.mcpServer.resources` | `*ResourceRequirements` | -- | No | -- | MCP server resources
`spec.ols.deployment.rhokp` | `Config` | -- | No | -- | Standalone RHOKP Deployment
`spec.ols.deployment.rhokp.replicas` | `*int32` | `1` | No | Min=0 | RHOKP replicas (operator forces 1)
`spec.ols.deployment.rhokp.resources` | `*ResourceRequirements` | -- | No | -- | RHOKP resources (default requests: 2 CPU, 2 GiB memory)
`spec.ols.deployment.rhokp.tolerations` | `[]Toleration` | -- | No | -- | RHOKP tolerations
`spec.ols.deployment.rhokp.nodeSelector` | `map[string]string` | -- | No | -- | RHOKP node selector
`spec.ols.deployment.console` | `Config` | -- | No | -- | Console container
`spec.ols.deployment.console.replicas` | `*int32` | `1` | No | Min=0 | Console replicas (operator forces 1)
`spec.ols.deployment.console.resources` | `*ResourceRequirements` | -- | No | -- | Console resources
`spec.ols.deployment.console.tolerations` | `[]Toleration` | -- | No | -- | Console tolerations
`spec.ols.deployment.console.nodeSelector` | `map[string]string` | -- | No | -- | Console node selector
`spec.ols.deployment.database` | `Config` | -- | No | -- | Database container
`spec.ols.deployment.database.replicas` | `*int32` | `1` | No | Min=0 | Database replicas (operator forces 1)
`spec.ols.deployment.database.resources` | `*ResourceRequirements` | -- | No | -- | Database resources
`spec.ols.deployment.database.tolerations` | `[]Toleration` | -- | No | -- | Database tolerations
`spec.ols.deployment.database.nodeSelector` | `map[string]string` | -- | No | -- | Database node selector
`spec.ols.deployment.alertsAdapter` | `AlertsAdapterSpec` | -- | No | -- | Alerts adapter deployment and config reference
`spec.ols.deployment.alertsAdapter.configMapRef` | `LocalObjectReference` | (none) | No | -- | Opt-in switch and runtime config reference: ConfigMap name in operator namespace; mounted at `/etc/alerts-adapter` when present (adapter reads `config.yaml`)
`spec.ols.deployment.alertsAdapter.replicas` | `*int32` | `1` | No | Min=0 | Alerts adapter replicas (operator forces 1)
`spec.ols.deployment.alertsAdapter.resources` | `*ResourceRequirements` | -- | No | -- | Alerts adapter resources
`spec.ols.deployment.alertsAdapter.tolerations` | `[]Toleration` | -- | No | -- | Alerts adapter tolerations
`spec.ols.deployment.alertsAdapter.nodeSelector` | `map[string]string` | -- | No | -- | Alerts adapter node selector
`spec.ols.deployment.agenticConsole` | `Config` | -- | No | -- | Agentic console deployment
`spec.ols.deployment.agenticConsole.replicas` | `*int32` | `1` | No | Min=0 | Agentic console replicas (operator forces 1)
`spec.ols.deployment.agenticConsole.resources` | `*ResourceRequirements` | -- | No | -- | Agentic console resources
`spec.ols.deployment.agenticConsole.tolerations` | `[]Toleration` | -- | No | -- | Agentic console tolerations
`spec.ols.deployment.agenticConsole.nodeSelector` | `map[string]string` | -- | No | -- | Agentic console node selector
`spec.ols.deployment.otelCollector` | `Config` | -- | No | -- | OTEL Collector deployment ([OLS-3510](https://redhat.atlassian.net/browse/OLS-3510))
`spec.ols.deployment.otelCollector.replicas` | `*int32` | `1` | No | Min=0 | Collector replicas (operator forces 1)
`spec.ols.deployment.otelCollector.resources` | `*ResourceRequirements` | -- | No | -- | Collector resources
`spec.ols.deployment.otelCollector.tolerations` | `[]Toleration` | -- | No | -- | Collector tolerations
`spec.ols.deployment.otelCollector.nodeSelector` | `map[string]string` | -- | No | -- | Collector node selector
`spec.ols.queryFilters` | `[]QueryFiltersSpec` | -- | No | -- | Query filters
`spec.ols.queryFilters[].name` | `string` | -- | No | -- | Filter name
`spec.ols.queryFilters[].pattern` | `string` | -- | No | -- | Regex pattern
`spec.ols.queryFilters[].replaceWith` | `string` | -- | No | -- | Replacement text
`spec.ols.userDataCollection` | `UserDataCollectionSpec` | -- | No | -- | Data collection switches
`spec.ols.userDataCollection.feedbackDisabled` | `bool` | -- | No | -- | Disable feedback
`spec.ols.userDataCollection.transcriptsDisabled` | `bool` | -- | No | -- | Disable transcripts and, [PLANNED: OLS-3569], Agentic collection
`spec.ols.tlsConfig` | `*TLSConfig` | -- | No | -- | Backend HTTPS TLS config
`spec.ols.tlsConfig.keyCertSecretRef` | `LocalObjectReference` | -- | No | -- | Secret with tls.crt, tls.key, ca.crt
`spec.ols.additionalCAConfigMapRef` | `*LocalObjectReference` | -- | No | -- | Extra CA certs for LLM TLS
`spec.ols.tlsSecurityProfile` | `*TLSSecurityProfile` | -- | No | -- | API endpoint TLS profile
`spec.ols.introspectionEnabled` | `*bool` | `true` | No | -- | Enable introspection
`spec.ols.auditEventsEnabled` | `*bool` | `true` | No | -- | Stdout compliance audit JSON events
`spec.ols.mcpKubeServerConfig` | `*MCPKubeServerConfiguration` | -- | No | -- | Built-in MCP kube server config
`spec.ols.mcpKubeServerConfig.timeout` | `int` | `60` | No | Min=5 | Timeout (seconds)
`spec.ols.mcpKubeServerConfig.toolsets` | `*[]MCPToolsetSelection` | Runtime operator defaults only when absent | No | MaxItems=17; singleton recognized key; unique toolsets; typed validation; unknown-field pruning caveat above | Full replacement selection; empty means none
`spec.ols.mcpKubeServerConfig.caBundleRefs` | `[]MCPCAReference` | No user bundles | No | MaxItems=64; exactly one source; explicit local name/key | Augments shared MCP system/cluster baseline trust; empty does not remove baseline
`spec.ols.proxyConfig` | `*ProxyConfig` | -- | No | -- | Proxy settings
`spec.ols.proxyConfig.proxyURL` | `string` | -- | No | Pattern `^https?://.*$` | Proxy URL
`spec.ols.proxyConfig.proxyCACertificate` | `*ProxyCACertConfigMapRef` | -- | No | -- | Proxy CA cert ref
`spec.ols.proxyConfig.proxyCACertificate.name` | `string` | -- | Yes (inline) | -- | ConfigMap name
`spec.ols.proxyConfig.proxyCACertificate.key` | `string` | `"proxy-ca.crt"` | No | -- | Key in ConfigMap
`spec.ols.rag` | `[]RAGSpec` | -- | No | -- | RAG databases
`spec.ols.rag[].image` | `string` | -- | Yes | -- | Container image URL
`spec.ols.rag[].indexPath` | `string` | `"/rag/vector_db"` | No | -- | Path in container
`spec.ols.rag[].indexID` | `string` | `""` | No | -- | Index ID
`spec.ols.quotaHandlersConfig` | `*QuotaHandlersConfig` | -- | No | -- | Token quota config
`spec.ols.quotaHandlersConfig.limitersConfig` | `[]LimiterConfig` | -- | No | -- | Limiter definitions
`spec.ols.quotaHandlersConfig.limitersConfig[].name` | `string` | -- | Yes | -- | Limiter name
`spec.ols.quotaHandlersConfig.limitersConfig[].type` | `string` | -- | Yes | Enum: cluster_limiter, user_limiter | Limiter type
`spec.ols.quotaHandlersConfig.limitersConfig[].initialQuota` | `int` | -- | Yes | Min=0 | Initial token quota
`spec.ols.quotaHandlersConfig.limitersConfig[].quotaIncrease` | `int` | -- | Yes | Min=0 | Quota increase step
`spec.ols.quotaHandlersConfig.limitersConfig[].period` | `string` | -- | Yes | Pattern (rule 38) | Time period
`spec.ols.quotaHandlersConfig.enableTokenHistory` | `bool` | -- | No | -- | Enable token history
`spec.ols.storage` | `*Storage` | -- | No | -- | Persistent storage
`spec.ols.storage.size` | `resource.Quantity` | -- | No | -- | Volume size
`spec.ols.storage.class` | `string` | -- | No | -- | Storage class
`spec.ols.credentialHotReload` | `bool` | `false` | No | -- | Opt-in LLM credential hot-reload: skip secret watching/restart, service re-reads per request (OLS-3450)
`spec.ols.byokRAGOnly` | `bool` | -- | No | -- | Disable operator-managed OKP; BYOK FAISS only
`spec.ols.querySystemPrompt` | `string` | -- | No | -- | Custom system prompt
`spec.ols.maxIterations` | `int` | `5` | No | Min=1 | Max agent iterations
`spec.ols.imagePullSecrets` | `[]LocalObjectReference` | -- | No | -- | Image pull secrets
`spec.ols.toolFilteringConfig` | `*ToolFilteringConfig` | -- | No | -- | Tool filtering config
`spec.ols.toolFilteringConfig.alpha` | `float64` | `0.8` | No | XValidation: 0.0-1.0 | Dense/sparse weight
`spec.ols.toolFilteringConfig.topK` | `int` | `10` | No | Min=1, Max=50 | Tools to retrieve
`spec.ols.toolFilteringConfig.threshold` | `float64` | `0.01` | No | XValidation: 0.0-1.0 | Similarity threshold
`spec.ols.toolsApprovalConfig` | `*ToolsApprovalConfig` | -- | No | -- | Tool approval config
`spec.ols.toolsApprovalConfig.approvalType` | `ApprovalType` | `tool_annotations` | No | Enum: never/always/tool_annotations | Approval strategy
`spec.ols.toolsApprovalConfig.approvalTimeout` | `int` | `600` | No | Min=1 | Approval timeout (seconds)
`spec.agenticOLS.terminalTTL` | `*int32` | -- | No | Min=1 | Admin terminal-run ceiling in whole days; omission selects agentic-operator fallback via absent handoff key
`spec.olsDataCollector` | `OLSDataCollectorSpec` | -- | No | -- | Data collector settings
`spec.olsDataCollector.logLevel` | `LogLevel` | `INFO` | No | Enum: DEBUG/INFO/WARNING/ERROR/CRITICAL | Data collector log level
`spec.mcpServers` | `[]MCPServerConfig` | -- | No | MaxItems=20 | External MCP servers
`spec.mcpServers[].name` | `string` | -- | Yes | -- | Server name
`spec.mcpServers[].url` | `string` | -- | Yes | Pattern `^https?://.*$` | Server URL
`spec.mcpServers[].timeout` | `int` | `5` | No | -- | Timeout (seconds)
`spec.mcpServers[].headers` | `[]MCPHeader` | -- | No | MaxItems=20 | HTTP headers
`spec.mcpServers[].headers[].name` | `string` | -- | Yes | MinLen=1, Pattern `^[A-Za-z0-9-]+$` | Header name
`spec.mcpServers[].headers[].valueFrom` | `MCPHeaderValueSource` | -- | Yes | -- | Value source
`spec.mcpServers[].headers[].valueFrom.type` | `MCPHeaderSourceType` | -- | Yes | Enum: secret/kubernetes/client | Source type
`spec.mcpServers[].headers[].valueFrom.secretRef` | `*LocalObjectReference` | -- | Conditional | XValidation (rules 49-50) | Secret reference
`spec.featureGates` | `[]FeatureGate` | -- | No | Enum per item: MCPServer/ToolFiltering | Feature gates
`spec.audit` | `AuditConfig` | -- | No | -- | Collector audit log storage and trace forwarding
`spec.audit.logging` | `*bool` | `true` | No | Optional | Collector Postgres logs pipeline
`spec.audit.tracingEndpoint` | `string` | -- | No | MaxLen=253 | Collector external OTLP trace export (TLS)
`status.conditions` | `[]metav1.Condition` | -- | -- | -- | Component conditions
`status.overallStatus` | `OverallStatus` | -- | -- | Enum: Ready/NotReady | Aggregate health
`status.diagnosticInfo` | `[]PodDiagnostic` | -- | -- | -- | Pod failure diagnostics
`status.diagnosticInfo[].failedComponent` | `string` | -- | -- | -- | Component name
`status.diagnosticInfo[].podName` | `string` | -- | -- | -- | Pod name
`status.diagnosticInfo[].containerName` | `string` | -- | -- | -- | Container name
`status.diagnosticInfo[].reason` | `string` | -- | -- | -- | Failure reason
`status.diagnosticInfo[].message` | `string` | -- | -- | -- | Error message
`status.diagnosticInfo[].exitCode` | `*int32` | -- | -- | -- | Container exit code
`status.diagnosticInfo[].type` | `DiagnosticType` | -- | -- | Enum (see rule 53) | Diagnostic category
`status.diagnosticInfo[].lastUpdated` | `metav1.Time` | -- | -- | -- | Collection timestamp

## Constraints

1. `.metadata.name` must be `"cluster"` (XValidation on OLSConfig type).
2. Only `azure_openai` provider type uses `deploymentName`; it is required for that type and forbidden (by convention) for others.
3. Only `watsonx` provider type uses `projectID`; it is required for that type.
4. Replicas are user-configurable for the API container (`spec.ols.deployment.api`). Console, database, alerts adapter, agentic console, and otel collector always run with 1 replica enforced by the operator.
5. Period format for quota limiters must match the regex pattern in rule 38, enforcing human-readable duration strings with correct singular/plural agreement.
6. `credentialKey` if set must contain at least one non-whitespace character.
7. Tool filtering requires the `ToolFiltering` feature gate in `spec.featureGates`.
8. User-defined MCP servers (`spec.mcpServers`) require the `MCPServer` feature gate in `spec.featureGates`. The built-in openshift MCP server is the standalone `ocpmcp` operand controlled exclusively by `spec.ols.introspectionEnabled` and does not require this gate.
9. There is exactly one allowed CacheType value: `postgres`.
10. `ToolFilteringConfig.alpha` and `ToolFilteringConfig.threshold` are validated via XValidation (not kubebuilder min/max) to enforce 0.0-1.0 range.
11. Bedrock credentials: `credentialsSecretRef` must contain either `apitoken` (Bearer) or both `aws_access_key_id` and `aws_secret_access_key` (IAM). Optional `role_arn` is passed through to the service when present.

## Verification

- OLS-2715 implemented verification: the full `make test` suite passes with default Kubernetes 1.27.1 envtest. Operator tests cover absent/empty/replacement selection and round trips/deepcopy; all 17 selections, singleton/duplicate/requiredness validation; Strict unknown-field rejection and non-Strict empty-map rejection; typed endpoint, registry, guardrail/cardinality, auth-mode and image validation; generated TOML defaults/mappings and retained security policy.
- CA/lifecycle tests cover both source kinds, missing/invalid keys and certificate-only PEM, canonical public snapshots, read-only key projections, content/reference-identity rollouts, invalid-update retention, Phase 2 snapshot matching, rotation/removal/recovery, baseline tracking independent of NetObserv, shared consumers, stale annotations, reserved references, ownership collisions, protected user sources and idempotent disable cleanup without readiness history.
- Actual pinned-image runtime evidence: `GenerateConfigTOML` inputs `null`, `{"toolsets":[]}` and seven typed selections passed startup/tools/list with 22, 0 and 39 tools (mock-discovery-dependent, not universal). Generated tables/security invariants remain intact. Actual static Prometheus/Loki/Tempo HTTPS/custom trust and all six negative CA/hostname calls passed; auth sourcing verified both upstream wrinkles, not backend authorization. First harness separately verifies singular OSSM/NetObserv trust, shared service/custom and REST CAData-only metric roots, Alertmanager shared service root, restart-based root rotation/removal and six Secret/Role/ClusterRole get/list denials (zero API requests) with allowed Pod list positive. See [runtime matrix](ocpmcp.md#verification-matrix) and its provenance/version caveats.
- Full-manager live cluster evidence: **93/93 unique assertions; 30/30 Strict server dry-run checks** on CRC OCP 4.22.14 / Kubernetes 1.35.6 with actual production `bin/manager` and full `SetupWithManager` watches, not a scoped harness. Admission comprises 25 configuration-specific invalid rejections and five valid-config controls rejected **only** for the separate wrong singleton name, not successful admission/create. Live default/empty/core replacement, real Service CA injection, public selected-key projection/checksums/private-key nonprojection, CA update/create/delete/binaryData/recovery, reference-identity rolls and invalid-source snapshot/pod retention passed. Twice disable after invalid-source status erased readiness history cleaned MCP while preserving user CAs; fix/re-enable restored Ready. Actual namespace Python HTTPS metric 2715 custom-root removal/rotation and Service CA-only baseline passed. Caller Pod access succeeds without operand-SA read RBAC; Secret/Role/RoleBinding policy denials differ from allowed ConfigMap caller-RBAC forbidden. No live audit proof of zero upstream API requests. All five deployed operands reached Ready in local-dev mode (operator ServiceMonitor/metrics reader skipped), not production-deployment proof. Normal finalization preserved user CAs/restored console; subsequent explicitly approved main cleanup removed all test-created cluster resources. See [live matrix, transients and cleanup](ocpmcp.md#full-manager-live-cluster-verification).
- [PLANNED: OLS-2715] Remaining external public-root endpoint HTTPS; live authenticated Prometheus/Loki/Tempo backend auth/RBAC and negative authorization; Route-based endpoint discovery/real stacks; all-tool functionality, compatibility/prompt/provider limitations and diagnostic helper image/command/kernel/RBAC/SCC prerequisites. Earlier local synthetic logs/traces/auth success and preserved root files do not establish these proofs; restricted-v2/non-root MCP readiness did not exercise CNI/helper functionality. Recovered mount/order and metadata-RV recovery-roll wrinkles were documented, not source-fixed.
- [OLS-4290] Tests cover `terminalTTL` validation, absent-field semantics, and publication/removal of `terminal-ttl-days` in the handoff ConfigMap.
- [PLANNED: OLS-3928] Operator tests cover explicit values, the default value, generated Classic configuration, and the `tool-output-inspection-enabled` handoff key.

## Planned Changes

- [PLANNED: OLS-2715] Complete remaining live authenticated Prometheus/Loki/Tempo backend auth/RBAC, Route discovery/real stacks, all-tool/helper-prerequisite and external public-root HTTPS proofs listed in Verification above. Full-manager live CA/controller/lifecycle and bounded Kubernetes caller-RBAC checks passed; earlier local actual pinned-image synthetic logs/traces/auth-sourcing evidence remains separate; see [ocpmcp.md](ocpmcp.md#runtime-verification).

- [OLS-3450] Added `spec.ols.credentialHotReload` boolean field. When enabled, the operator skips annotating LLM credential secrets (no restart on rotation) and writes `credential_hot_reload: true` into `olsconfig.yaml`. See design spec `docs/superpowers/specs/2026-09-01-credential-hot-reload-design.md`.
-  Added `reasoningConfig` field (`map[string]runtime.RawExtension`) to `ModelParametersSpec`. Freeform map passed through to the service as `reasoning_config` for provider-specific reasoning/thinking parameters. Includes release notes and user-facing documentation for valid keys per provider.
- [DONE: OLS-3683 / OLS-3684] `spec.agenticOLS` (`sandboxMode`, `agenticSandboxConfig`), appserver-owned client CA Secrets, and handoff ConfigMap (`lightspeed-agentic-configuration`). See `agentic-sandbox-profile.md`.
- [DONE: OLS-3697] Change `spec.ols.deployment.rhokp` from `ContainerConfig` to `Config`. RHOKP becomes a standalone Deployment with replicas (forced to 1), resources, tolerations, and nodeSelector. See `rhokp.md`.
- [PLANNED: OLS-3594] Optional agentic auto-injection of MCP into agent runs (deferred).
- [PLANNED: OLS-3685+] Agentic-operator consumption of the handoff ConfigMap/Secrets.
- [PLANNED: OLS-3569] Reuse `spec.ols.userDataCollection.transcriptsDisabled` for the credential-gated Agentic Collector resources; add no CRD field. See `agentic-data-collection.md`.
- [PLANNED: OLS-3928] Add `spec.ols.guardrails.toolResultInspection.enabled`, default it to `true`, and publish one effective value to both guarded paths.
