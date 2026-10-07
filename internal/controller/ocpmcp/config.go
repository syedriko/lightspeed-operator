package ocpmcp

import (
	"bytes"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/distribution/reference"
	olsv1alpha1 "github.com/openshift/lightspeed-operator/api/v1alpha1"
	"github.com/openshift/lightspeed-operator/internal/controller/utils"
)

const (
	defaultPrometheusURL   = "https://thanos-querier.openshift-monitoring.svc.cluster.local:9091"
	defaultAlertmanagerURL = "https://alertmanager-main.openshift-monitoring.svc.cluster.local:9094"
	ossmCAPath             = "/etc/mcp-server/toolset-ca/ossm/ca.crt"
	netobservCAPath        = "/etc/mcp-server/toolset-ca/netobserv/ca.crt"
)

// mcpRuntimeConfig deliberately has no omitempty tags on security controls or
// Toolsets: false and an explicitly empty selection must reach the server.
type mcpRuntimeConfig struct {
	Port                 string              `toml:"port"`
	TLSCert              string              `toml:"tls_cert"`
	TLSKey               string              `toml:"tls_key"`
	ReadOnly             bool                `toml:"read_only"`
	DisableDestructive   bool                `toml:"disable_destructive"`
	Toolsets             []string            `toml:"toolsets"`
	CompatibilityFilters bool                `toml:"experimental_enable_target_compatibility_tool_filters"`
	DeniedResources      []mcpDeniedResource `toml:"denied_resources"`
	ToolsetConfigs       map[string]any      `toml:"toolset_configs,omitempty"`
}

type mcpDeniedResource struct {
	Group   string `toml:"group"`
	Version string `toml:"version"`
	Kind    string `toml:"kind,omitempty"`
}

type mcpBackendConfig struct {
	URL                  string `toml:"url"`
	CertificateAuthority string `toml:"certificate_authority"`
	Insecure             bool   `toml:"insecure"`
}

type mcpHelmConfig struct {
	StorageDriver     string   `toml:"storage_driver"`
	AllowedRegistries []string `toml:"allowed_registries,omitempty"`
}

type mcpMetricsConfig struct {
	PrometheusURL   string `toml:"prometheus_url"`
	AlertmanagerURL string `toml:"alertmanager_url"`
	Guardrails      string `toml:"guardrails"`
	// Legacy omission retains the previous table, including absent auth/TLS flags.
	AuthMode               string `toml:"auth_mode,omitempty"`
	Insecure               *bool  `toml:"insecure,omitempty"`
	MaxMetricCardinality   *int64 `toml:"max_metric_cardinality,omitempty"`
	MaxLabelCardinality    *int64 `toml:"max_label_cardinality,omitempty"`
	RangeQueryFullResponse *bool  `toml:"range_query_full_response,omitempty"`
}

type mcpLogsConfig struct {
	LokiURL  *string `toml:"loki_url,omitempty"`
	UseRoute *bool   `toml:"use_route,omitempty"`
	AuthMode string  `toml:"auth_mode"`
	Insecure bool    `toml:"insecure"`
}

type mcpTracesConfig struct {
	TempoURL *string `toml:"tempo_url,omitempty"`
	UseRoute *bool   `toml:"use_route,omitempty"`
	AuthMode string  `toml:"auth_mode"`
	Insecure bool    `toml:"insecure"`
}

type mcpCNIConfig struct {
	KernelDebugImage string `toml:"kernel_debug_image"`
	TCPDumpImage     string `toml:"tcpdump_image"`
	PWRUImage        string `toml:"pwru_image"`
}

// GenerateConfigTOML serializes only operator-owned runtime fields. CA source
// validation/projection is handled by reconciliation; the paths here are the
// contract with the MCP Deployment, not user-supplied filesystem paths.
func GenerateConfigTOML(config *olsv1alpha1.MCPKubeServerConfiguration) (string, error) {
	runtime, err := buildRuntimeConfig(config)
	if err != nil {
		return "", fmt.Errorf("invalid MCP server configuration: %w", err)
	}
	var output bytes.Buffer
	encoder := toml.NewEncoder(&output)
	encoder.Indent = ""
	if err := encoder.Encode(runtime); err != nil {
		return "", fmt.Errorf("failed to encode MCP server configuration: %w", err)
	}
	return output.String(), nil
}

func buildRuntimeConfig(config *olsv1alpha1.MCPKubeServerConfiguration) (*mcpRuntimeConfig, error) {
	runtime := &mcpRuntimeConfig{
		Port:                 strconv.Itoa(int(utils.OpenShiftMCPServerHTTPSPort)),
		TLSCert:              path.Join(utils.OpenShiftMCPServerTLSMountPath, "tls.crt"),
		TLSKey:               path.Join(utils.OpenShiftMCPServerTLSMountPath, "tls.key"),
		ReadOnly:             false,
		DisableDestructive:   true,
		CompatibilityFilters: true,
		Toolsets:             []string{},
		DeniedResources:      []mcpDeniedResource{{Group: "", Version: "v1", Kind: "Secret"}, {Group: "rbac.authorization.k8s.io", Version: "v1"}},
		ToolsetConfigs:       map[string]any{},
	}
	if config == nil || config.Toolsets == nil {
		runtime.Toolsets = []string{"core", "config", "helm", "observability/metrics", "kubevirt"}
		runtime.ToolsetConfigs["observability/metrics"] = mcpMetricsConfig{
			PrometheusURL: defaultPrometheusURL, AlertmanagerURL: defaultAlertmanagerURL, Guardrails: "!tsdb",
		}
		return runtime, nil
	}
	seen := map[string]bool{}
	for i, selection := range *config.Toolsets {
		// Name also checks singleton entries for direct callers/fake clients,
		// rather than silently picking the first nonnil key.
		name := selection.Name()
		if name == "" {
			return nil, fmt.Errorf("toolsets[%d] must select exactly one supported toolset (openshift/mustgather is unsupported)", i)
		}
		if seen[name] {
			return nil, fmt.Errorf("toolsets[%d]: duplicate toolset %q", i, name)
		}
		seen[name] = true
		key, block, err := selectedToolsetConfig(selection)
		if err != nil {
			return nil, fmt.Errorf("toolsets[%d] (%s): %w", i, name, err)
		}
		runtime.Toolsets = append(runtime.Toolsets, name)
		if block != nil {
			runtime.ToolsetConfigs[key] = block
		}
	}
	return runtime, nil
}

func selectedToolsetConfig(selection olsv1alpha1.MCPToolsetSelection) (string, any, error) {
	switch selection.Name() {
	case "core", "config", "kcp", "kubevirt", "tekton", "cluster-diagnostics", "netedge", "oadp", "ovn-kubernetes", "observability/otelcol":
		return "", nil, nil
	case "ossm":
		config := selection.OSSM
		if err := validateHTTPSURL(config.URL); err != nil {
			return "", nil, err
		}
		if config.CABundleRef == nil {
			return "", nil, fmt.Errorf("caBundleRef is required")
		}
		return "kiali", mcpBackendConfig{URL: config.URL, CertificateAuthority: ossmCAPath}, nil
	case "netobserv":
		config := selection.NetObserv
		endpoint := ""
		if config.URL != nil {
			if config.Namespace != nil || config.Service != nil || config.Port != nil {
				return "", nil, fmt.Errorf("url cannot be combined with namespace, service or port")
			}
			if err := validateHTTPSURL(*config.URL); err != nil {
				return "", nil, err
			}
			if config.CABundleRef == nil {
				return "", nil, fmt.Errorf("caBundleRef is required with url")
			}
			endpoint = *config.URL
		} else {
			port := int32(9001)
			if config.Port != nil {
				port = *config.Port
			}
			if port < 1 || port > 65535 {
				return "", nil, fmt.Errorf("port must be between 1 and 65535")
			}
			endpoint = fmt.Sprintf("https://%s.%s.svc.cluster.local:%d", stringOrDefault(config.Service, "netobserv-plugin"), stringOrDefault(config.Namespace, "netobserv"), port)
		}
		return "netobserv", mcpBackendConfig{URL: endpoint, CertificateAuthority: netobservCAPath}, nil
	case "helm":
		config := selection.Helm
		if config.StorageDriver != "" && config.StorageDriver != "configmap" {
			return "", nil, fmt.Errorf("storageDriver must be configmap; Secret storage is unsupported")
		}
		for _, registry := range config.AllowedRegistries {
			u, err := url.Parse(registry)
			if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "oci") {
				return "", nil, fmt.Errorf("allowedRegistries entry %q must be an absolute HTTPS or OCI URL", registry)
			}
		}
		return "helm", mcpHelmConfig{StorageDriver: "configmap", AllowedRegistries: config.AllowedRegistries}, nil
	case "observability/metrics":
		config := selection.Metrics
		if err := ValidateMetricsConfig(config); err != nil {
			return "", nil, err
		}
		verified := false
		return "observability/metrics", mcpMetricsConfig{
			PrometheusURL:   stringOrDefault(config.PrometheusURL, defaultPrometheusURL),
			AlertmanagerURL: stringOrDefault(config.AlertmanagerURL, defaultAlertmanagerURL),
			Guardrails:      effectiveGuardrails(config), AuthMode: authMode(config.AuthMode), Insecure: &verified,
			MaxMetricCardinality: config.MaxMetricCardinality, MaxLabelCardinality: config.MaxLabelCardinality,
			RangeQueryFullResponse: config.RangeQueryFullResponse,
		}, nil
	case "observability/logs":
		config := selection.Logs
		if err := validateDiscoveryEndpoint(config.LokiURL, config.UseRoute, config.AuthMode); err != nil {
			return "", nil, err
		}
		var route *bool
		if config.LokiURL == nil {
			enabled := true
			route = &enabled
		}
		return "observability/logs", mcpLogsConfig{LokiURL: config.LokiURL, UseRoute: route, AuthMode: authMode(config.AuthMode)}, nil
	case "observability/traces":
		config := selection.Traces
		if err := validateDiscoveryEndpoint(config.TempoURL, config.UseRoute, config.AuthMode); err != nil {
			return "", nil, err
		}
		var route *bool
		if config.TempoURL == nil {
			enabled := true
			route = &enabled
		}
		return "observability/traces", mcpTracesConfig{TempoURL: config.TempoURL, UseRoute: route, AuthMode: authMode(config.AuthMode)}, nil
	case "cni-diagnostics":
		config := selection.CNIDiagnostics
		for _, image := range []*string{config.KernelDebugImage, config.TCPDumpImage, config.PWRUImage} {
			if image == nil {
				continue
			}
			ref, err := reference.ParseNormalizedNamed(*image)
			if err != nil {
				return "", nil, fmt.Errorf("invalid diagnostic image %q: %w", *image, err)
			}
			// Keep the SHA-256-only contract of the admission patterns, even
			// when other digest algorithms are available to the parser.
			if digested, ok := ref.(reference.Digested); ok && digested.Digest().Algorithm() != "sha256" {
				return "", nil, fmt.Errorf("diagnostic image %q must use a sha256 digest", *image)
			}
		}
		return "cni-diagnostics", mcpCNIConfig{
			KernelDebugImage: stringOrDefault(config.KernelDebugImage, "nicolaka/netshoot:v0.16"),
			TCPDumpImage:     stringOrDefault(config.TCPDumpImage, "nicolaka/netshoot:v0.16"),
			PWRUImage:        stringOrDefault(config.PWRUImage, "docker.io/cilium/pwru:v1.0.10"),
		}, nil
	default:
		return "", nil, fmt.Errorf("unsupported toolset %q (openshift/mustgather is unsupported)", selection.Name())
	}
}

func stringOrDefault(value *string, fallback string) string {
	if value != nil {
		return *value
	}
	return fallback
}

func authMode(mode string) string {
	if mode == "" {
		return "header"
	}
	return mode
}

func validateAuthMode(mode string) error {
	if mode != "" && mode != "header" && mode != "kubeconfig" {
		return fmt.Errorf("authMode must be header or kubeconfig")
	}
	return nil
}

func validateHTTPSURL(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("endpoint %q must be an absolute HTTPS URL with a nonempty host and no userinfo", endpoint)
	}
	return nil
}

func validateDiscoveryEndpoint(endpoint *string, route *bool, mode string) error {
	if err := validateAuthMode(mode); err != nil {
		return err
	}
	if endpoint != nil {
		if route != nil {
			return fmt.Errorf("static URL cannot be combined with useRoute")
		}
		return validateHTTPSURL(*endpoint)
	}
	if route != nil && !*route {
		return fmt.Errorf("useRoute must be true; Service/HTTP discovery is unsupported")
	}
	return nil
}

func effectiveGuardrails(config *olsv1alpha1.MCPMetricsConfig) string {
	if config.Guardrails != nil {
		return *config.Guardrails
	}
	if config.PrometheusURL != nil {
		return "all"
	}
	return "!tsdb"
}

// ValidateMetricsConfig validates the upstream guardrail grammar and the
// presence-dependent cardinality constraints that are awkward to express in CEL.
// It is also available to reconciliation/admission callers without encoding TOML.
func ValidateMetricsConfig(config *olsv1alpha1.MCPMetricsConfig) error {
	if config == nil {
		return fmt.Errorf("metrics configuration must not be nil")
	}
	if err := validateAuthMode(config.AuthMode); err != nil {
		return err
	}
	for _, endpoint := range []*string{config.PrometheusURL, config.AlertmanagerURL} {
		if endpoint != nil {
			if err := validateHTTPSURL(*endpoint); err != nil {
				return err
			}
		}
	}
	rules, err := parseGuardrails(effectiveGuardrails(config))
	if err != nil {
		return err
	}
	if limit := config.MaxMetricCardinality; limit != nil && (*limit <= 0 || !rules["max-metric-cardinality"]) {
		return fmt.Errorf("maxMetricCardinality must be positive and requires the max-metric-cardinality guardrail")
	}
	if limit := config.MaxLabelCardinality; limit != nil && (*limit < 0 || !rules["disallow-blanket-regex"]) {
		return fmt.Errorf("maxLabelCardinality must be nonnegative and requires the disallow-blanket-regex guardrail")
	}
	return nil
}

func parseGuardrails(input string) (map[string]bool, error) {
	names := []string{"disallow-explicit-name-label", "require-label-matcher", "disallow-blanket-regex", "max-metric-cardinality"}
	all := func(enabled bool) map[string]bool {
		rules := map[string]bool{}
		for _, name := range names {
			rules[name] = enabled
		}
		return rules
	}
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" || input == "all" {
		return all(true), nil
	}
	if input == "none" {
		return all(false), nil
	}
	var rules map[string]bool
	var negative bool
	for _, token := range strings.Split(input, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		disable := strings.HasPrefix(token, "!")
		if rules == nil {
			negative = disable
			rules = all(disable)
		} else if negative != disable {
			return nil, fmt.Errorf("guardrails cannot mix positive and negative rules")
		}
		name := strings.TrimPrefix(token, "!")
		if name == "tsdb" && disable {
			rules["disallow-blanket-regex"] = false
			rules["max-metric-cardinality"] = false
			continue
		}
		if _, known := rules[name]; !known {
			return nil, fmt.Errorf("unknown guardrail %q", token)
		}
		rules[name] = !disable
	}
	if rules == nil {
		// Unlike an empty whole value, a nonempty comma-only list is a
		// positive selection of zero rules in the pinned upstream parser.
		return all(false), nil
	}
	return rules, nil
}
