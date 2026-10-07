/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

// MCPCAKeyReference selects one certificate-bundle key in a local object.
type MCPCAKeyReference struct {
	// Name is a ConfigMap or Secret DNS subdomain name in the operand namespace.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name"`
	// Key is an explicit data key; no default is applied.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[-._a-zA-Z0-9]+$`
	Key string `json:"key"`
}

// MCPCAReference references user-owned certificate data, never an arbitrary path.
// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:MaxProperties=1
// +kubebuilder:validation:XValidation:rule="has(self.configMap) != has(self.secret)",message="exactly one of configMap or secret is required"
type MCPCAReference struct {
	// +optional
	ConfigMap *MCPCAKeyReference `json:"configMap,omitempty"`
	// +optional
	Secret *MCPCAKeyReference `json:"secret,omitempty"`
}

// MCPEmptyConfig selects a toolset with no supported configuration.
// A zero-property map rejects unknown settings and remains CEL-compatible on
// Kubernetes 1.27, whose CEL object adapter cannot handle propertyless structs.
// Unknown settings in typed configurable blocks are still pruned unless the
// request uses fieldValidation=Strict; CEL cannot inspect pruned fields.
// +kubebuilder:validation:MaxProperties=0
type MCPEmptyConfig map[string]string

// DeepCopy preserves the named map type when copying pointer-valued selections.
// controller-gen otherwise allocates an unnamed map for these pointers.
func (in MCPEmptyConfig) DeepCopy() MCPEmptyConfig {
	if in == nil {
		return nil
	}
	out := make(MCPEmptyConfig, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// MCPToolsetSelection selects exactly one of the 17 supported toolsets.
// Unknown-only entries (including openshift/mustgather) become empty after
// pruning and are rejected; mixed recognized/unknown keys require Strict field
// validation to reject the unknown key rather than silently prune it.
// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:MaxProperties=1
// +kubebuilder:validation:XValidation:rule="[has(self.core), has(self.config), has(self.helm), has(self.kcp), has(self.ossm), has(self.kubevirt), has(self.netobserv), has(self.tekton), has(self.cluster__dash__diagnostics), has(self.cni__dash__diagnostics), has(self.netedge), has(self.oadp), has(self.ovn__dash__kubernetes), has(self.observability__slash__metrics), has(self.observability__slash__logs), has(self.observability__slash__traces), has(self.observability__slash__otelcol)].filter(selected, selected).size() == 1",message="select exactly one supported toolset; openshift/mustgather is unsupported"
type MCPToolsetSelection struct {
	// +optional
	Core *MCPEmptyConfig `json:"core,omitempty"`
	// +optional
	Config *MCPEmptyConfig `json:"config,omitempty"`
	// +optional
	Helm *MCPHelmConfig `json:"helm,omitempty"`
	// +optional
	KCP *MCPEmptyConfig `json:"kcp,omitempty"`
	// +optional
	OSSM *MCPOSSMConfig `json:"ossm,omitempty"`
	// +optional
	KubeVirt *MCPEmptyConfig `json:"kubevirt,omitempty"`
	// +optional
	NetObserv *MCPNetObservConfig `json:"netobserv,omitempty"`
	// +optional
	Tekton *MCPEmptyConfig `json:"tekton,omitempty"`
	// +optional
	ClusterDiagnostics *MCPEmptyConfig `json:"cluster-diagnostics,omitempty"`
	// +optional
	CNIDiagnostics *MCPCNIDiagnosticsConfig `json:"cni-diagnostics,omitempty"`
	// +optional
	NetEdge *MCPEmptyConfig `json:"netedge,omitempty"`
	// +optional
	OADP *MCPEmptyConfig `json:"oadp,omitempty"`
	// +optional
	OVNKubernetes *MCPEmptyConfig `json:"ovn-kubernetes,omitempty"`
	// +optional
	Metrics *MCPMetricsConfig `json:"observability/metrics,omitempty"`
	// +optional
	Logs *MCPLogsConfig `json:"observability/logs,omitempty"`
	// +optional
	Traces *MCPTracesConfig `json:"observability/traces,omitempty"`
	// +optional
	OtelCol *MCPEmptyConfig `json:"observability/otelcol,omitempty"`
}

// MCPOSSMConfig configures a verified HTTPS Kiali endpoint.
type MCPOSSMConfig struct {
	// URL must be an absolute HTTPS endpoint with a host and no userinfo.
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'https' && url(self).getHostname() != '' && !self.matches('^[^:]+://[^/?#]*@')",message="url must be an absolute HTTPS endpoint with a host and no userinfo"
	URL string `json:"url"`
	// CABundleRef is required; source existence and PEM are checked during reconciliation.
	// +required
	CABundleRef *MCPCAReference `json:"caBundleRef"`
}

// MCPNetObservConfig selects either an explicit URL with CA or a Service endpoint.
// Defaults are computed by generation, not admission, to preserve field presence.
// +kubebuilder:validation:XValidation:rule="!has(self.url) || (!has(self.__namespace__) && !has(self.service) && !has(self.port))",message="url conflicts with namespace, service and port"
// +kubebuilder:validation:XValidation:rule="!has(self.url) || has(self.caBundleRef)",message="an explicit url requires caBundleRef"
type MCPNetObservConfig struct {
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'https' && url(self).getHostname() != '' && !self.matches('^[^:]+://[^/?#]*@')",message="url must be an absolute HTTPS endpoint with a host and no userinfo"
	URL *string `json:"url,omitempty"`
	// Namespace defaults to netobserv in Service form only.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Namespace *string `json:"namespace,omitempty"`
	// Service defaults to netobserv-plugin in Service form only.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]([-a-z0-9]*[a-z0-9])?$`
	Service *string `json:"service,omitempty"`
	// Port defaults to 9001 in Service form only.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port *int32 `json:"port,omitempty"`
	// +optional
	CABundleRef *MCPCAReference `json:"caBundleRef,omitempty"`
}

// MCPHelmConfig configures ConfigMap-backed release storage and registry allowlisting.
type MCPHelmConfig struct {
	// StorageDriver defaults to configmap for explicit selection. Secret storage is unsupported.
	// +optional
	// +kubebuilder:validation:Enum=configmap
	StorageDriver string `json:"storageDriver,omitempty"`
	// AllowedRegistries uses upstream normalized scheme/host and path-prefix matching.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=2048
	// +kubebuilder:validation:items:XValidation:rule="isURL(self) && url(self).getScheme() in ['https', 'oci'] && url(self).getHostname() != '' && !self.matches('^[^:]+://[^/?#]*@')",message="registry must be an absolute HTTPS or OCI URL with a host and no userinfo"
	AllowedRegistries []string `json:"allowedRegistries,omitempty"`
}

// MCPMetricsConfig configures verified HTTPS observability metrics endpoints.
// Guardrail grammar follows the pinned upstream parser: case-insensitive,
// whitespace-trimmed, empty tokens ignored, and signs never mixed. Comma-only
// lists disable all configurable rules; an empty whole value enables all.
// Regex checks operate directly on bounded strings: Kubernetes 1.27's CEL cost
// estimator does not propagate string bounds through split/trim/lowerAscii.
// [[:space:]\p{Z}\x{0085}] matches Go TrimSpace's Unicode whitespace set.
// +kubebuilder:validation:XValidation:rule="!has(self.maxMetricCardinality) || (has(self.guardrails) ? (self.guardrails.matches(r'(?i)^[[:space:]\\p{Z}\\x{0085}]*(all)?[[:space:]\\p{Z}\\x{0085}]*$') || (self.guardrails.contains('!') ? !self.guardrails.matches(r'(?i)(^|,)[[:space:]\\p{Z}\\x{0085}]*!(tsdb|max-metric-cardinality)[[:space:]\\p{Z}\\x{0085}]*(,|$)') : self.guardrails.matches(r'(?i)(^|,)[[:space:]\\p{Z}\\x{0085}]*max-metric-cardinality[[:space:]\\p{Z}\\x{0085}]*(,|$)'))) : has(self.prometheusURL))",message="maxMetricCardinality requires the effective max-metric-cardinality guardrail (default !tsdb disables it)"
// +kubebuilder:validation:XValidation:rule="!has(self.maxLabelCardinality) || (has(self.guardrails) ? (self.guardrails.matches(r'(?i)^[[:space:]\\p{Z}\\x{0085}]*(all)?[[:space:]\\p{Z}\\x{0085}]*$') || (self.guardrails.contains('!') ? !self.guardrails.matches(r'(?i)(^|,)[[:space:]\\p{Z}\\x{0085}]*!(tsdb|disallow-blanket-regex)[[:space:]\\p{Z}\\x{0085}]*(,|$)') : self.guardrails.matches(r'(?i)(^|,)[[:space:]\\p{Z}\\x{0085}]*disallow-blanket-regex[[:space:]\\p{Z}\\x{0085}]*(,|$)'))) : has(self.prometheusURL))",message="maxLabelCardinality requires the effective disallow-blanket-regex guardrail (default !tsdb disables it)"
type MCPMetricsConfig struct {
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'https' && url(self).getHostname() != '' && !self.matches('^[^:]+://[^/?#]*@')",message="prometheusURL must be an absolute HTTPS endpoint with a host and no userinfo"
	PrometheusURL *string `json:"prometheusURL,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'https' && url(self).getHostname() != '' && !self.matches('^[^:]+://[^/?#]*@')",message="alertmanagerURL must be an absolute HTTPS endpoint with a host and no userinfo"
	AlertmanagerURL *string `json:"alertmanagerURL,omitempty"`
	// AuthMode defaults to header; kubeconfig uses derived caller credentials.
	// +optional
	// +kubebuilder:validation:Enum=header;kubeconfig
	AuthMode string `json:"authMode,omitempty"`
	// Guardrails defaults to !tsdb without prometheusURL, otherwise all.
	// +optional
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:XValidation:rule="self.matches(r'^[a-zA-Z!,[:space:]\\p{Z}\\x{0085}-]*$') && (self.matches(r'(?i)^[[:space:]\\p{Z}\\x{0085}]*(all|none)?[[:space:]\\p{Z}\\x{0085}]*$') || self.matches(r'(?i)^[[:space:]\\p{Z}\\x{0085}]*(disallow-explicit-name-label|require-label-matcher|disallow-blanket-regex|max-metric-cardinality)?[[:space:]\\p{Z}\\x{0085}]*(,[[:space:]\\p{Z}\\x{0085}]*(disallow-explicit-name-label|require-label-matcher|disallow-blanket-regex|max-metric-cardinality)?[[:space:]\\p{Z}\\x{0085}]*)*$') || self.matches(r'(?i)^[[:space:]\\p{Z}\\x{0085}]*(!(disallow-explicit-name-label|require-label-matcher|disallow-blanket-regex|max-metric-cardinality|tsdb))?[[:space:]\\p{Z}\\x{0085}]*(,[[:space:]\\p{Z}\\x{0085}]*(!(disallow-explicit-name-label|require-label-matcher|disallow-blanket-regex|max-metric-cardinality|tsdb))?[[:space:]\\p{Z}\\x{0085}]*)*$'))",message="invalid guardrails: use all, none, positive rule names, or negative rule names (including !tsdb), without mixing signs"
	Guardrails *string `json:"guardrails,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxMetricCardinality *int64 `json:"maxMetricCardinality,omitempty"`
	// Zero means always reject blanket regex.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxLabelCardinality *int64 `json:"maxLabelCardinality,omitempty"`
	// +optional
	RangeQueryFullResponse *bool `json:"rangeQueryFullResponse,omitempty"`
}

// MCPLogsConfig selects a static Loki endpoint or HTTPS Route discovery.
// +kubebuilder:validation:XValidation:rule="!has(self.lokiURL) || !has(self.useRoute)",message="lokiURL conflicts with an explicit useRoute"
type MCPLogsConfig struct {
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'https' && url(self).getHostname() != '' && !self.matches('^[^:]+://[^/?#]*@')",message="lokiURL must be an absolute HTTPS endpoint with a host and no userinfo"
	LokiURL *string `json:"lokiURL,omitempty"`
	// +optional
	// +kubebuilder:validation:XValidation:rule="self",message="useRoute must be true; HTTP Service discovery is unsupported"
	UseRoute *bool `json:"useRoute,omitempty"`
	// +optional
	// +kubebuilder:validation:Enum=header;kubeconfig
	AuthMode string `json:"authMode,omitempty"`
}

// MCPTracesConfig selects a static Tempo API base or HTTPS Route discovery.
// +kubebuilder:validation:XValidation:rule="!has(self.tempoURL) || !has(self.useRoute)",message="tempoURL conflicts with an explicit useRoute"
type MCPTracesConfig struct {
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'https' && url(self).getHostname() != '' && !self.matches('^[^:]+://[^/?#]*@')",message="tempoURL must be an absolute HTTPS endpoint with a host and no userinfo"
	TempoURL *string `json:"tempoURL,omitempty"`
	// +optional
	// +kubebuilder:validation:XValidation:rule="self",message="useRoute must be true; HTTP Service discovery is unsupported"
	UseRoute *bool `json:"useRoute,omitempty"`
	// +optional
	// +kubebuilder:validation:Enum=header;kubeconfig
	AuthMode string `json:"authMode,omitempty"`
}

// MCPCNIDiagnosticsConfig overrides diagnostic helper images, not MCP privileges.
// The bounded reference pattern accepts conventional repository names, optional
// registry/port, tag and sha256 digest. It is syntax-only, not pull/compatibility
// validation; exotic registry authorities and non-sha256 digests are unsupported.
type MCPCNIDiagnosticsConfig struct {
	// +optional
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^((localhost|[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+)(:[0-9]+)?/|[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?:[0-9]+/)?[a-z0-9]+(([._]|__|[-]+)[a-z0-9]+)*(/[a-z0-9]+(([._]|__|[-]+)[a-z0-9]+)*)*(:[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127})?(@sha256:[a-f0-9]{64})?$`
	KernelDebugImage *string `json:"kernelDebugImage,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^((localhost|[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+)(:[0-9]+)?/|[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?:[0-9]+/)?[a-z0-9]+(([._]|__|[-]+)[a-z0-9]+)*(/[a-z0-9]+(([._]|__|[-]+)[a-z0-9]+)*)*(:[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127})?(@sha256:[a-f0-9]{64})?$`
	TCPDumpImage *string `json:"tcpdumpImage,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^((localhost|[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+)(:[0-9]+)?/|[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?:[0-9]+/)?[a-z0-9]+(([._]|__|[-]+)[a-z0-9]+)*(/[a-z0-9]+(([._]|__|[-]+)[a-z0-9]+)*)*(:[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127})?(@sha256:[a-f0-9]{64})?$`
	PWRUImage *string `json:"pwruImage,omitempty"`
}

// Name returns the exact server selection name, or empty for an invalid union.
func (s MCPToolsetSelection) Name() string {
	selections := []struct {
		name     string
		selected bool
	}{
		{"core", s.Core != nil}, {"config", s.Config != nil},
		{"helm", s.Helm != nil}, {"kcp", s.KCP != nil},
		{"ossm", s.OSSM != nil}, {"kubevirt", s.KubeVirt != nil},
		{"netobserv", s.NetObserv != nil}, {"tekton", s.Tekton != nil},
		{"cluster-diagnostics", s.ClusterDiagnostics != nil},
		{"cni-diagnostics", s.CNIDiagnostics != nil}, {"netedge", s.NetEdge != nil},
		{"oadp", s.OADP != nil}, {"ovn-kubernetes", s.OVNKubernetes != nil},
		{"observability/metrics", s.Metrics != nil}, {"observability/logs", s.Logs != nil},
		{"observability/traces", s.Traces != nil}, {"observability/otelcol", s.OtelCol != nil},
	}
	name := ""
	for _, selection := range selections {
		if selection.selected {
			if name != "" {
				return ""
			}
			name = selection.name
		}
	}
	return name
}

// CAReferences enumerates user references only: selected OSSM/NetObserv blocks
// in selection order, followed by shared references in declared order. It does
// not resolve objects, inject baseline CAs, deduplicate, or alias the input.
func (c *MCPKubeServerConfiguration) CAReferences() []MCPCAReference {
	if c == nil {
		return nil
	}
	var refs []MCPCAReference
	if c.Toolsets != nil {
		for _, selection := range *c.Toolsets {
			if selection.OSSM != nil && selection.OSSM.CABundleRef != nil {
				refs = append(refs, *selection.OSSM.CABundleRef.DeepCopy())
			}
			if selection.NetObserv != nil && selection.NetObserv.CABundleRef != nil {
				refs = append(refs, *selection.NetObserv.CABundleRef.DeepCopy())
			}
		}
	}
	for _, ref := range c.CABundleRefs {
		refs = append(refs, *ref.DeepCopy())
	}
	return refs
}
