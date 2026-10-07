package ocpmcp

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	olsv1alpha1 "github.com/openshift/lightspeed-operator/api/v1alpha1"
	"github.com/openshift/lightspeed-operator/internal/controller/utils"
)

func configPtr[T any](value T) *T { return &value }

func configTestCA() *olsv1alpha1.MCPCAReference {
	return &olsv1alpha1.MCPCAReference{ConfigMap: &olsv1alpha1.MCPCAKeyReference{Name: "backend-ca", Key: "ca.crt"}}
}

func selectedConfig(selections ...olsv1alpha1.MCPToolsetSelection) *olsv1alpha1.MCPKubeServerConfiguration {
	return &olsv1alpha1.MCPKubeServerConfiguration{Toolsets: &selections}
}

func decodeConfig(t *testing.T, config *olsv1alpha1.MCPKubeServerConfiguration) map[string]any {
	t.Helper()
	encoded, err := GenerateConfigTOML(config)
	if err != nil {
		t.Fatalf("GenerateConfigTOML: %v", err)
	}
	var decoded map[string]any
	if _, err := toml.Decode(encoded, &decoded); err != nil {
		t.Fatalf("decode generated TOML: %v\n%s", err, encoded)
	}
	return decoded
}

func configTable(t *testing.T, decoded map[string]any, key string) map[string]any {
	t.Helper()
	tables, ok := decoded["toolset_configs"].(map[string]any)
	if !ok {
		t.Fatalf("missing toolset_configs: %#v", decoded)
	}
	table, ok := tables[key].(map[string]any)
	if !ok {
		t.Fatalf("missing table %q: %#v", key, tables)
	}
	return table
}

func requireValue(t *testing.T, values map[string]any, key string, expected any) {
	t.Helper()
	actual, present := values[key]
	if !present || !reflect.DeepEqual(actual, expected) {
		t.Errorf("%s = %#v (present %t), want %#v", key, actual, present, expected)
	}
}

func requireAbsent(t *testing.T, values map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if actual, present := values[key]; present {
			t.Errorf("unexpected %s = %#v", key, actual)
		}
	}
}

func requireSecurity(t *testing.T, decoded map[string]any) {
	t.Helper()
	requireValue(t, decoded, "port", "8443")
	requireValue(t, decoded, "tls_cert", "/etc/tls/tls.crt")
	requireValue(t, decoded, "tls_key", "/etc/tls/tls.key")
	requireValue(t, decoded, "read_only", false)
	requireValue(t, decoded, "disable_destructive", true)
	requireValue(t, decoded, "experimental_enable_target_compatibility_tool_filters", true)
	requireAbsent(t, decoded, "disabled_tools", "require_tls", "certificate_authority")
	requireValue(t, decoded, "denied_resources", []map[string]any{
		{"group": "", "version": "v1", "kind": "Secret"},
		{"group": "rbac.authorization.k8s.io", "version": "v1"},
	})
}

func TestConfigSelectionPresence(t *testing.T) {
	for name, config := range map[string]*olsv1alpha1.MCPKubeServerConfiguration{
		"absent parent":        nil,
		"absent field":         {},
		"timeout only":         {Timeout: 120},
		"shared CA only":       {CABundleRefs: []olsv1alpha1.MCPCAReference{*configTestCA()}},
		"empty shared CA list": {CABundleRefs: []olsv1alpha1.MCPCAReference{}},
	} {
		t.Run(name, func(t *testing.T) {
			decoded := decodeConfig(t, config)
			requireSecurity(t, decoded)
			requireValue(t, decoded, "toolsets", []any{"core", "config", "helm", "observability/metrics", "kubevirt"})
			table := configTable(t, decoded, "observability/metrics")
			requireValue(t, table, "prometheus_url", defaultPrometheusURL)
			requireValue(t, table, "alertmanager_url", defaultAlertmanagerURL)
			requireValue(t, table, "guardrails", "!tsdb")
			requireAbsent(t, table, "auth_mode", "insecure", "max_metric_cardinality", "max_label_cardinality", "range_query_full_response")
			if len(decoded["toolset_configs"].(map[string]any)) != 1 {
				t.Error("legacy config must not add Helm or other tables")
			}
		})
	}
	for name, selections := range map[string][]olsv1alpha1.MCPToolsetSelection{"empty slice": {}, "nil slice with present pointer": nil} {
		t.Run(name, func(t *testing.T) {
			decoded := decodeConfig(t, &olsv1alpha1.MCPKubeServerConfiguration{Toolsets: &selections})
			requireSecurity(t, decoded)
			requireValue(t, decoded, "toolsets", []any{})
			requireAbsent(t, decoded, "toolset_configs")
		})
	}
}

func TestAllToolsetSelections(t *testing.T) {
	// Each selection stands alone: even NetEdge must not implicitly select metrics.
	cases := []struct {
		name      string
		selection olsv1alpha1.MCPToolsetSelection
		table     string
	}{
		{"core", olsv1alpha1.MCPToolsetSelection{Core: &olsv1alpha1.MCPEmptyConfig{}}, ""},
		{"config", olsv1alpha1.MCPToolsetSelection{Config: &olsv1alpha1.MCPEmptyConfig{}}, ""},
		{"helm", olsv1alpha1.MCPToolsetSelection{Helm: &olsv1alpha1.MCPHelmConfig{}}, "helm"},
		{"kcp", olsv1alpha1.MCPToolsetSelection{KCP: &olsv1alpha1.MCPEmptyConfig{}}, ""},
		{"ossm", olsv1alpha1.MCPToolsetSelection{OSSM: &olsv1alpha1.MCPOSSMConfig{URL: "https://kiali.example.com", CABundleRef: configTestCA()}}, "kiali"},
		{"kubevirt", olsv1alpha1.MCPToolsetSelection{KubeVirt: &olsv1alpha1.MCPEmptyConfig{}}, ""},
		{"netobserv", olsv1alpha1.MCPToolsetSelection{NetObserv: &olsv1alpha1.MCPNetObservConfig{}}, "netobserv"},
		{"tekton", olsv1alpha1.MCPToolsetSelection{Tekton: &olsv1alpha1.MCPEmptyConfig{}}, ""},
		{"cluster-diagnostics", olsv1alpha1.MCPToolsetSelection{ClusterDiagnostics: &olsv1alpha1.MCPEmptyConfig{}}, ""},
		{"cni-diagnostics", olsv1alpha1.MCPToolsetSelection{CNIDiagnostics: &olsv1alpha1.MCPCNIDiagnosticsConfig{}}, "cni-diagnostics"},
		{"netedge", olsv1alpha1.MCPToolsetSelection{NetEdge: &olsv1alpha1.MCPEmptyConfig{}}, ""},
		{"oadp", olsv1alpha1.MCPToolsetSelection{OADP: &olsv1alpha1.MCPEmptyConfig{}}, ""},
		{"ovn-kubernetes", olsv1alpha1.MCPToolsetSelection{OVNKubernetes: &olsv1alpha1.MCPEmptyConfig{}}, ""},
		{"observability/metrics", olsv1alpha1.MCPToolsetSelection{Metrics: &olsv1alpha1.MCPMetricsConfig{}}, "observability/metrics"},
		{"observability/logs", olsv1alpha1.MCPToolsetSelection{Logs: &olsv1alpha1.MCPLogsConfig{}}, "observability/logs"},
		{"observability/traces", olsv1alpha1.MCPToolsetSelection{Traces: &olsv1alpha1.MCPTracesConfig{}}, "observability/traces"},
		{"observability/otelcol", olsv1alpha1.MCPToolsetSelection{OtelCol: &olsv1alpha1.MCPEmptyConfig{}}, ""},
	}
	var all []olsv1alpha1.MCPToolsetSelection
	var names []any
	for _, test := range cases {
		all = append(all, test.selection)
		names = append(names, test.name)
		t.Run(test.name, func(t *testing.T) {
			decoded := decodeConfig(t, selectedConfig(test.selection))
			requireSecurity(t, decoded)
			requireValue(t, decoded, "toolsets", []any{test.name})
			if test.table == "" {
				requireAbsent(t, decoded, "toolset_configs")
			} else {
				configTable(t, decoded, test.table)
				if len(decoded["toolset_configs"].(map[string]any)) != 1 {
					t.Error("selection emitted an unselected table")
				}
			}
		})
	}
	decoded := decodeConfig(t, selectedConfig(all...))
	requireValue(t, decoded, "toolsets", names)
	if len(decoded["toolset_configs"].(map[string]any)) != 7 {
		t.Error("all selections must emit exactly seven configurable tables")
	}
}

func TestBackendAndHelmConfig(t *testing.T) {
	ca := configTestCA()
	decoded := decodeConfig(t, selectedConfig(
		olsv1alpha1.MCPToolsetSelection{OSSM: &olsv1alpha1.MCPOSSMConfig{URL: "https://kiali.example.com/mesh", CABundleRef: ca}},
		olsv1alpha1.MCPToolsetSelection{NetObserv: &olsv1alpha1.MCPNetObservConfig{}},
		olsv1alpha1.MCPToolsetSelection{Helm: &olsv1alpha1.MCPHelmConfig{}},
	))
	requireValue(t, configTable(t, decoded, "kiali"), "url", "https://kiali.example.com/mesh")
	requireValue(t, configTable(t, decoded, "kiali"), "certificate_authority", "/etc/mcp-server/toolset-ca/ossm/ca.crt")
	requireValue(t, configTable(t, decoded, "kiali"), "insecure", false)
	netobserv := configTable(t, decoded, "netobserv")
	requireValue(t, netobserv, "url", "https://netobserv-plugin.netobserv.svc.cluster.local:9001")
	requireValue(t, netobserv, "certificate_authority", "/etc/mcp-server/toolset-ca/netobserv/ca.crt")
	requireValue(t, netobserv, "insecure", false)
	requireAbsent(t, netobserv, "namespace", "service", "port")
	helm := configTable(t, decoded, "helm")
	requireValue(t, helm, "storage_driver", "configmap")
	requireAbsent(t, helm, "allowed_registries")

	for name, config := range map[string]*olsv1alpha1.MCPNetObservConfig{
		"service overrides": {Namespace: configPtr("flows"), Service: configPtr("plugin"), Port: configPtr(int32(9443)), CABundleRef: ca},
		"explicit url":      {URL: configPtr("https://flows.example.com/api"), CABundleRef: ca},
	} {
		t.Run(name, func(t *testing.T) {
			expected := "https://plugin.flows.svc.cluster.local:9443"
			if config.URL != nil {
				expected = *config.URL
			}
			table := configTable(t, decodeConfig(t, selectedConfig(olsv1alpha1.MCPToolsetSelection{NetObserv: config})), "netobserv")
			requireValue(t, table, "url", expected)
			requireValue(t, table, "certificate_authority", netobservCAPath)
			requireValue(t, table, "insecure", false)
		})
	}
	registries := []string{"oci://registry.example.com/charts", "https://charts.example.com/team"}
	helm = configTable(t, decodeConfig(t, selectedConfig(olsv1alpha1.MCPToolsetSelection{Helm: &olsv1alpha1.MCPHelmConfig{StorageDriver: "configmap", AllowedRegistries: registries}})), "helm")
	requireValue(t, helm, "allowed_registries", []any{registries[0], registries[1]})
}

func TestMetricsConfigFields(t *testing.T) {
	cases := []struct {
		name                                 string
		config                               olsv1alpha1.MCPMetricsConfig
		prometheus, alertmanager, guardrails string
	}{
		{"empty", olsv1alpha1.MCPMetricsConfig{}, defaultPrometheusURL, defaultAlertmanagerURL, "!tsdb"},
		{"prometheus presence even matching default", olsv1alpha1.MCPMetricsConfig{PrometheusURL: configPtr(defaultPrometheusURL)}, defaultPrometheusURL, defaultAlertmanagerURL, "all"},
		{"alertmanager only", olsv1alpha1.MCPMetricsConfig{AlertmanagerURL: configPtr("https://alerts.example.com")}, defaultPrometheusURL, "https://alerts.example.com", "!tsdb"},
		{"explicit guardrails", olsv1alpha1.MCPMetricsConfig{PrometheusURL: configPtr("https://metrics.example.com"), Guardrails: configPtr("none")}, "https://metrics.example.com", defaultAlertmanagerURL, "none"},
		{"empty guardrails preserved", olsv1alpha1.MCPMetricsConfig{Guardrails: configPtr("")}, defaultPrometheusURL, defaultAlertmanagerURL, ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			table := configTable(t, decodeConfig(t, selectedConfig(olsv1alpha1.MCPToolsetSelection{Metrics: &test.config})), "observability/metrics")
			requireValue(t, table, "prometheus_url", test.prometheus)
			requireValue(t, table, "alertmanager_url", test.alertmanager)
			requireValue(t, table, "guardrails", test.guardrails)
			requireValue(t, table, "auth_mode", "header")
			requireValue(t, table, "insecure", false)
			requireAbsent(t, table, "max_metric_cardinality", "max_label_cardinality", "range_query_full_response", "certificate_authority")
		})
	}
	for _, full := range []bool{false, true} {
		config := &olsv1alpha1.MCPMetricsConfig{Guardrails: configPtr("all"), AuthMode: "kubeconfig", MaxMetricCardinality: configPtr(int64(42)), MaxLabelCardinality: configPtr(int64(0)), RangeQueryFullResponse: &full}
		table := configTable(t, decodeConfig(t, selectedConfig(olsv1alpha1.MCPToolsetSelection{Metrics: config})), "observability/metrics")
		requireValue(t, table, "auth_mode", "kubeconfig")
		requireValue(t, table, "max_metric_cardinality", int64(42))
		requireValue(t, table, "max_label_cardinality", int64(0))
		requireValue(t, table, "range_query_full_response", full)
	}
}

func TestLogsTracesConfigForms(t *testing.T) {
	for _, static := range []bool{false, true} {
		logs := &olsv1alpha1.MCPLogsConfig{}
		traces := &olsv1alpha1.MCPTracesConfig{}
		if static {
			logs.LokiURL = configPtr("https://loki.example.com/api/logs/v1")
			traces.TempoURL = configPtr("https://tempo.example.com/api/traces/v1/application/tempo")
			logs.AuthMode, traces.AuthMode = "kubeconfig", "kubeconfig"
		} else {
			logs.UseRoute, traces.UseRoute = configPtr(true), configPtr(true)
		}
		decoded := decodeConfig(t, selectedConfig(olsv1alpha1.MCPToolsetSelection{Logs: logs}, olsv1alpha1.MCPToolsetSelection{Traces: traces}))
		for _, key := range []string{"observability/logs", "observability/traces"} {
			table := configTable(t, decoded, key)
			requireValue(t, table, "insecure", false)
			requireAbsent(t, table, "certificate_authority")
			if static {
				requireAbsent(t, table, "use_route")
				requireValue(t, table, "auth_mode", "kubeconfig")
			} else {
				requireValue(t, table, "use_route", true)
				requireValue(t, table, "auth_mode", "header")
				requireAbsent(t, table, "loki_url", "tempo_url")
			}
		}
		if static {
			requireValue(t, configTable(t, decoded, "observability/logs"), "loki_url", *logs.LokiURL)
			requireValue(t, configTable(t, decoded, "observability/traces"), "tempo_url", *traces.TempoURL)
		}
	}
	// Omitting the optional discovery flag also explicitly enables route discovery.
	for key, selection := range map[string]olsv1alpha1.MCPToolsetSelection{
		"observability/logs":   {Logs: &olsv1alpha1.MCPLogsConfig{}},
		"observability/traces": {Traces: &olsv1alpha1.MCPTracesConfig{}},
	} {
		requireValue(t, configTable(t, decodeConfig(t, selectedConfig(selection)), key), "use_route", true)
	}
}

func TestCNIDiagnosticImages(t *testing.T) {
	defaults := configTable(t, decodeConfig(t, selectedConfig(olsv1alpha1.MCPToolsetSelection{CNIDiagnostics: &olsv1alpha1.MCPCNIDiagnosticsConfig{}})), "cni-diagnostics")
	requireValue(t, defaults, "kernel_debug_image", "nicolaka/netshoot:v0.16")
	requireValue(t, defaults, "tcpdump_image", "nicolaka/netshoot:v0.16")
	requireValue(t, defaults, "pwru_image", "docker.io/cilium/pwru:v1.0.10")
	config := &olsv1alpha1.MCPCNIDiagnosticsConfig{KernelDebugImage: configPtr("mirror.example.com/debug:v1"), TCPDumpImage: configPtr("mirror.example.com/capture:v2"), PWRUImage: configPtr("mirror.example.com/pwru:v3")}
	table := configTable(t, decodeConfig(t, selectedConfig(olsv1alpha1.MCPToolsetSelection{CNIDiagnostics: config})), "cni-diagnostics")
	requireValue(t, table, "kernel_debug_image", *config.KernelDebugImage)
	requireValue(t, table, "tcpdump_image", *config.TCPDumpImage)
	requireValue(t, table, "pwru_image", *config.PWRUImage)
	partial := configTable(t, decodeConfig(t, selectedConfig(olsv1alpha1.MCPToolsetSelection{CNIDiagnostics: &olsv1alpha1.MCPCNIDiagnosticsConfig{TCPDumpImage: config.TCPDumpImage}})), "cni-diagnostics")
	requireValue(t, partial, "kernel_debug_image", "nicolaka/netshoot:v0.16")
	requireValue(t, partial, "tcpdump_image", *config.TCPDumpImage)
	requireValue(t, partial, "pwru_image", "docker.io/cilium/pwru:v1.0.10")
}

func TestHTTPSURLSchemeCase(t *testing.T) {
	for _, target := range []struct {
		name      string
		selection func(string) olsv1alpha1.MCPToolsetSelection
	}{
		{"ossm/url", func(endpoint string) olsv1alpha1.MCPToolsetSelection {
			return olsv1alpha1.MCPToolsetSelection{OSSM: &olsv1alpha1.MCPOSSMConfig{URL: endpoint, CABundleRef: configTestCA()}}
		}},
		{"netobserv/url", func(endpoint string) olsv1alpha1.MCPToolsetSelection {
			return olsv1alpha1.MCPToolsetSelection{NetObserv: &olsv1alpha1.MCPNetObservConfig{URL: &endpoint, CABundleRef: configTestCA()}}
		}},
		{"metrics/prometheusURL", func(endpoint string) olsv1alpha1.MCPToolsetSelection {
			return olsv1alpha1.MCPToolsetSelection{Metrics: &olsv1alpha1.MCPMetricsConfig{PrometheusURL: &endpoint}}
		}},
		{"metrics/alertmanagerURL", func(endpoint string) olsv1alpha1.MCPToolsetSelection {
			return olsv1alpha1.MCPToolsetSelection{Metrics: &olsv1alpha1.MCPMetricsConfig{AlertmanagerURL: &endpoint}}
		}},
		{"logs/lokiURL", func(endpoint string) olsv1alpha1.MCPToolsetSelection {
			return olsv1alpha1.MCPToolsetSelection{Logs: &olsv1alpha1.MCPLogsConfig{LokiURL: &endpoint}}
		}},
		{"traces/tempoURL", func(endpoint string) olsv1alpha1.MCPToolsetSelection {
			return olsv1alpha1.MCPToolsetSelection{Traces: &olsv1alpha1.MCPTracesConfig{TempoURL: &endpoint}}
		}},
	} {
		for _, tc := range []struct {
			endpoint string
			valid    bool
		}{
			{"HTTPS://example.com/api", true},
			{"HTTPS://user@example.com", false},
			{"HTTPS://user:pass@example.com", false},
		} {
			t.Run(target.name+"/"+tc.endpoint, func(t *testing.T) {
				encoded, err := GenerateConfigTOML(selectedConfig(target.selection(tc.endpoint)))
				if tc.valid {
					if err != nil || !strings.Contains(encoded, tc.endpoint) {
						t.Fatalf("valid endpoint produced TOML %q, error %v", encoded, err)
					}
				} else if err == nil || encoded != "" {
					t.Fatalf("invalid endpoint produced TOML %q, error %v", encoded, err)
				}
			})
		}
	}
}

func TestCNIDiagnosticImageValidation(t *testing.T) {
	for _, field := range []struct {
		name string
		set  func(*olsv1alpha1.MCPCNIDiagnosticsConfig, *string)
	}{
		{"kernel_debug_image", func(config *olsv1alpha1.MCPCNIDiagnosticsConfig, image *string) { config.KernelDebugImage = image }},
		{"tcpdump_image", func(config *olsv1alpha1.MCPCNIDiagnosticsConfig, image *string) { config.TCPDumpImage = image }},
		{"pwru_image", func(config *olsv1alpha1.MCPCNIDiagnosticsConfig, image *string) { config.PWRUImage = image }},
	} {
		for _, tc := range []struct {
			image string
			valid bool
		}{
			{"netshoot", true},
			{"nicolaka/netshoot:v0.16", true},
			{"mirror.example.com:5000/cilium/pwru:v1.0.10", true},
			{"mirror.example.com/image@sha256:" + strings.Repeat("a0", 32), true},
			{"mirror.example.com/image:TAG@sha256:" + strings.Repeat("a0", 32), true},
			{"mirror.example.com/image@sha256:" + strings.Repeat("A", 64), false},
			{"mirror.example.com/image@sha256:" + strings.Repeat("aA", 32), false},
			{"mirror.example.com/image@sha512:" + strings.Repeat("a", 128), false},
			{"", false}, {" ", false}, {"bad image", false},
			{"repo:bad tag", false}, {"namespace/Uppercase", false},
			{"https://mirror.example.com/image", false}, {"repo@sha256:abc", false},
		} {
			t.Run(field.name+"/"+tc.image, func(t *testing.T) {
				config := &olsv1alpha1.MCPCNIDiagnosticsConfig{}
				field.set(config, &tc.image)
				selection := selectedConfig(olsv1alpha1.MCPToolsetSelection{CNIDiagnostics: config})
				if tc.valid {
					requireValue(t, configTable(t, decodeConfig(t, selection), "cni-diagnostics"), field.name, tc.image)
				} else if encoded, err := GenerateConfigTOML(selection); err == nil || encoded != "" {
					t.Fatalf("invalid image produced TOML %q, error %v", encoded, err)
				}
			})
		}
	}
}

func TestTOMLEscaping(t *testing.T) {
	endpoint := "https://kiali.example.com/path/\"quoted\""
	decoded := decodeConfig(t, selectedConfig(olsv1alpha1.MCPToolsetSelection{OSSM: &olsv1alpha1.MCPOSSMConfig{URL: endpoint, CABundleRef: configTestCA()}}))
	requireValue(t, configTable(t, decoded, "kiali"), "url", endpoint)
	requireSecurity(t, decoded)
	// Exercise the serializer itself with characters that endpoint admission
	// rejects. They must remain string data, never become additional TOML keys.
	payload := "quote\" backslash\\ newline\n[toolset_configs.evil]\ninsecure = true\n#"
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(mcpBackendConfig{URL: payload, CertificateAuthority: payload}); err != nil {
		t.Fatalf("encode escaped fields: %v", err)
	}
	var roundTrip map[string]any
	if _, err := toml.Decode(encoded.String(), &roundTrip); err != nil {
		t.Fatalf("decode escaped fields: %v", err)
	}
	requireValue(t, roundTrip, "url", payload)
	requireValue(t, roundTrip, "certificate_authority", payload)
	requireValue(t, roundTrip, "insecure", false)
	requireAbsent(t, roundTrip, "toolset_configs")
	if encoded, err := GenerateConfigTOML(selectedConfig(olsv1alpha1.MCPToolsetSelection{OSSM: &olsv1alpha1.MCPOSSMConfig{URL: payload, CABundleRef: configTestCA()}})); err == nil || encoded != "" {
		t.Errorf("injected endpoint produced TOML %q, error %v", encoded, err)
	}
}

func TestRejectInvalidDirectConfiguration(t *testing.T) {
	ca := configTestCA()
	cases := []struct {
		name      string
		selection olsv1alpha1.MCPToolsetSelection
	}{
		{"empty/unsupported entry", olsv1alpha1.MCPToolsetSelection{}},
		{"multiple keys", olsv1alpha1.MCPToolsetSelection{Core: &olsv1alpha1.MCPEmptyConfig{}, Helm: &olsv1alpha1.MCPHelmConfig{}}},
		{"ossm missing url", olsv1alpha1.MCPToolsetSelection{OSSM: &olsv1alpha1.MCPOSSMConfig{CABundleRef: ca}}},
		{"ossm HTTP", olsv1alpha1.MCPToolsetSelection{OSSM: &olsv1alpha1.MCPOSSMConfig{URL: "http://kiali.example.com", CABundleRef: ca}}},
		{"ossm missing CA", olsv1alpha1.MCPToolsetSelection{OSSM: &olsv1alpha1.MCPOSSMConfig{URL: "https://kiali.example.com"}}},
		{"netobserv mixed namespace", olsv1alpha1.MCPToolsetSelection{NetObserv: &olsv1alpha1.MCPNetObservConfig{URL: configPtr("https://flows.example.com"), Namespace: configPtr("netobserv"), CABundleRef: ca}}},
		{"netobserv mixed service", olsv1alpha1.MCPToolsetSelection{NetObserv: &olsv1alpha1.MCPNetObservConfig{URL: configPtr("https://flows.example.com"), Service: configPtr("netobserv-plugin"), CABundleRef: ca}}},
		{"netobserv mixed port", olsv1alpha1.MCPToolsetSelection{NetObserv: &olsv1alpha1.MCPNetObservConfig{URL: configPtr("https://flows.example.com"), Port: configPtr(int32(9001)), CABundleRef: ca}}},
		{"netobserv missing CA", olsv1alpha1.MCPToolsetSelection{NetObserv: &olsv1alpha1.MCPNetObservConfig{URL: configPtr("https://flows.example.com")}}},
		{"netobserv invalid port", olsv1alpha1.MCPToolsetSelection{NetObserv: &olsv1alpha1.MCPNetObservConfig{Port: configPtr(int32(0))}}},
		{"helm Secret storage", olsv1alpha1.MCPToolsetSelection{Helm: &olsv1alpha1.MCPHelmConfig{StorageDriver: "secret"}}},
		{"helm registry HTTP", olsv1alpha1.MCPToolsetSelection{Helm: &olsv1alpha1.MCPHelmConfig{AllowedRegistries: []string{"http://charts.example.com"}}}},
		{"logs false route", olsv1alpha1.MCPToolsetSelection{Logs: &olsv1alpha1.MCPLogsConfig{UseRoute: configPtr(false)}}},
		{"traces false route", olsv1alpha1.MCPToolsetSelection{Traces: &olsv1alpha1.MCPTracesConfig{UseRoute: configPtr(false)}}},
		{"logs mixed forms", olsv1alpha1.MCPToolsetSelection{Logs: &olsv1alpha1.MCPLogsConfig{LokiURL: configPtr("https://loki.example.com"), UseRoute: configPtr(true)}}},
		{"traces mixed forms", olsv1alpha1.MCPToolsetSelection{Traces: &olsv1alpha1.MCPTracesConfig{TempoURL: configPtr("https://tempo.example.com"), UseRoute: configPtr(true)}}},
		{"logs HTTP", olsv1alpha1.MCPToolsetSelection{Logs: &olsv1alpha1.MCPLogsConfig{LokiURL: configPtr("http://loki.example.com")}}},
		{"traces empty endpoint", olsv1alpha1.MCPToolsetSelection{Traces: &olsv1alpha1.MCPTracesConfig{TempoURL: configPtr("")}}},
		{"logs auth", olsv1alpha1.MCPToolsetSelection{Logs: &olsv1alpha1.MCPLogsConfig{AuthMode: "operator"}}},
		{"traces auth", olsv1alpha1.MCPToolsetSelection{Traces: &olsv1alpha1.MCPTracesConfig{AuthMode: "operator"}}},
		{"CNI empty image", olsv1alpha1.MCPToolsetSelection{CNIDiagnostics: &olsv1alpha1.MCPCNIDiagnosticsConfig{PWRUImage: configPtr("")}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if encoded, err := GenerateConfigTOML(selectedConfig(test.selection)); err == nil || encoded != "" {
				t.Errorf("invalid configuration produced %q, error %v", encoded, err)
			}
		})
	}
	core := olsv1alpha1.MCPToolsetSelection{Core: &olsv1alpha1.MCPEmptyConfig{}}
	if _, err := GenerateConfigTOML(selectedConfig(core, core)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate selection error = %v", err)
	}
	if _, err := GenerateConfigTOML(selectedConfig(olsv1alpha1.MCPToolsetSelection{})); err == nil || !strings.Contains(err.Error(), "openshift/mustgather is unsupported") {
		t.Errorf("unsupported selection error = %v", err)
	}
}

func TestMetricsGuardrailValidation(t *testing.T) {
	for _, grammar := range []string{"", "all", "none", " ALL ", "!tsdb", "!tsdb,!require-label-matcher", "require-label-matcher,max-metric-cardinality", "!max-metric-cardinality", "disallow-blanket-regex", ",require-label-matcher,,", "!DISALLOW-BLANKET-REGEX", ", ,"} {
		t.Run("valid/"+grammar, func(t *testing.T) {
			if err := ValidateMetricsConfig(&olsv1alpha1.MCPMetricsConfig{Guardrails: &grammar}); err != nil {
				t.Errorf("valid grammar %q: %v", grammar, err)
			}
		})
	}
	for _, grammar := range []string{"tsdb", "unknown", "all,none", "!none", "all,require-label-matcher", "require-label-matcher,!max-metric-cardinality", "!tsdb,require-label-matcher", "!!tsdb"} {
		t.Run("invalid/"+grammar, func(t *testing.T) {
			if err := ValidateMetricsConfig(&olsv1alpha1.MCPMetricsConfig{Guardrails: &grammar}); err == nil {
				t.Errorf("invalid grammar %q accepted", grammar)
			}
		})
	}
	for name, config := range map[string]*olsv1alpha1.MCPMetricsConfig{
		"nil":                              nil,
		"auth":                             {AuthMode: "operator"},
		"HTTP prometheus":                  {PrometheusURL: configPtr("http://metrics.example.com")},
		"HTTP alertmanager":                {AlertmanagerURL: configPtr("http://alerts.example.com")},
		"empty prometheus":                 {PrometheusURL: configPtr("")},
		"hostless":                         {PrometheusURL: configPtr("https:///metrics")},
		"relative":                         {AlertmanagerURL: configPtr("/alerts")},
		"metric limit inactive by default": {MaxMetricCardinality: configPtr(int64(1))},
		"label limit inactive by default":  {MaxLabelCardinality: configPtr(int64(0))},
		"metric zero":                      {Guardrails: configPtr("all"), MaxMetricCardinality: configPtr(int64(0))},
		"metric negative":                  {Guardrails: configPtr("all"), MaxMetricCardinality: configPtr(int64(-1))},
		"label negative":                   {Guardrails: configPtr("all"), MaxLabelCardinality: configPtr(int64(-1))},
		"none metric limit":                {Guardrails: configPtr("none"), MaxMetricCardinality: configPtr(int64(1))},
		"disabled label limit":             {Guardrails: configPtr("!disallow-blanket-regex"), MaxLabelCardinality: configPtr(int64(2))},
		"comma-only list metric limit":     {Guardrails: configPtr(", ,"), MaxMetricCardinality: configPtr(int64(1))},
		"URL with embedded credentials":    {PrometheusURL: configPtr("https://user:password@metrics.example.com")},
		"URL with TOML injection":          {AlertmanagerURL: configPtr("https://alerts.example.com/\"\nread_only = true")},
		"malformed URL":                    {PrometheusURL: configPtr("https://[malformed")},
		"non-HTTPS scheme":                 {PrometheusURL: configPtr("ftp://metrics.example.com")},
		"empty alertmanager":               {AlertmanagerURL: configPtr("")},
		"tsdb disabled label limit":        {Guardrails: configPtr("!tsdb"), MaxLabelCardinality: configPtr(int64(0))},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateMetricsConfig(config); err == nil {
				t.Error("invalid metrics accepted")
			}
		})
	}
	for name, config := range map[string]*olsv1alpha1.MCPMetricsConfig{
		"explicit prometheus enables cardinality": {PrometheusURL: configPtr(defaultPrometheusURL), MaxMetricCardinality: configPtr(int64(1)), MaxLabelCardinality: configPtr(int64(0))},
		"positive metric rule":                    {Guardrails: configPtr("max-metric-cardinality"), MaxMetricCardinality: configPtr(int64(1))},
		"positive label rule":                     {Guardrails: configPtr("disallow-blanket-regex"), MaxLabelCardinality: configPtr(int64(0))},
		"negative unrelated rule":                 {Guardrails: configPtr("!require-label-matcher"), MaxMetricCardinality: configPtr(int64(1)), MaxLabelCardinality: configPtr(int64(5))},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateMetricsConfig(config); err != nil {
				t.Errorf("valid metrics: %v", err)
			}
		})
	}
}

var _ = Describe("Typed MCP ConfigMap generation", func() {
	It("should route explicit empty selection through TOML generation", func() {
		cr := utils.GetDefaultOLSConfigCR()
		cr.Spec.OLSConfig.MCPKubeServerConfig = selectedConfig()
		cm, err := GenerateConfigMap(testReconcilerInstance, cr)
		Expect(err).NotTo(HaveOccurred())
		var decoded map[string]any
		_, err = toml.Decode(cm.Data[utils.OpenShiftMCPServerConfigFilename], &decoded)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded["toolsets"]).To(BeEmpty())
		Expect(decoded).NotTo(HaveKey("toolset_configs"))
		Expect(cm.OwnerReferences).To(HaveLen(1))
	})
	It("should return an error instead of a ConfigMap for invalid selection", func() {
		cr := utils.GetDefaultOLSConfigCR()
		cr.Spec.OLSConfig.MCPKubeServerConfig = selectedConfig(olsv1alpha1.MCPToolsetSelection{})
		cm, err := GenerateConfigMap(testReconcilerInstance, cr)
		Expect(err).To(HaveOccurred())
		Expect(cm).To(BeNil())
	})
})
