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

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestMCPToolsetsPresence(t *testing.T) {
	for _, input := range []string{`{}`, `{"timeout":60}`, `{"toolsets":[]}`, `{"toolsets":[{"core":{}}]}`} {
		t.Run(input, func(t *testing.T) {
			var in MCPKubeServerConfiguration
			if err := json.Unmarshal([]byte(input), &in); err != nil {
				t.Fatal(err)
			}
			for _, value := range []*MCPKubeServerConfiguration{&in, in.DeepCopy()} {
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var out MCPKubeServerConfiguration
				if err := json.Unmarshal(data, &out); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(in, out) {
					t.Fatalf("round trip changed presence: %s -> %s", input, data)
				}
				if strings.Contains(string(data), `"toolsets"`) != (in.Toolsets != nil) {
					t.Fatalf("wrong presence: %s", data)
				}
			}
		})
	}
}

func TestMCPTypedRoundTripAndDeepCopy(t *testing.T) {
	input := `{"toolsets":[{"ossm":{"url":"https://kiali.example.com","caBundleRef":{"configMap":{"name":"mesh-ca","key":"ca.crt"}}}},{"netobserv":{"namespace":"flows","service":"plugin","port":9001,"caBundleRef":{"secret":{"name":"flows-ca","key":"bundle"}}}},{"helm":{"storageDriver":"configmap","allowedRegistries":["oci://registry.example.com/charts"]}},{"observability/metrics":{"prometheusURL":"https://prom.example.com","alertmanagerURL":"https://alerts.example.com","authMode":"header","guardrails":"all","maxMetricCardinality":1,"maxLabelCardinality":0,"rangeQueryFullResponse":false}},{"observability/logs":{"useRoute":true,"authMode":"kubeconfig"}},{"observability/traces":{"tempoURL":"https://tempo.example.com/api"}},{"cni-diagnostics":{"kernelDebugImage":"mirror.example.com/netshoot:v0.16","tcpdumpImage":"netshoot:latest","pwruImage":"cilium/pwru:v1.0.10"}}],"caBundleRefs":[{"secret":{"name":"shared-ca","key":"ca.crt"}}]}`
	var in MCPKubeServerConfiguration
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out MCPKubeServerConfiguration
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip changed values: %s", data)
	}
	copy := in.DeepCopy()
	(*copy.Toolsets)[0].OSSM.CABundleRef.ConfigMap.Key = "other"
	*(*copy.Toolsets)[1].NetObserv.Namespace = "other"
	*(*copy.Toolsets)[1].NetObserv.Port = 1
	(*copy.Toolsets)[2].Helm.AllowedRegistries[0] = "https://other"
	*(*copy.Toolsets)[3].Metrics.MaxLabelCardinality = 100
	*(*copy.Toolsets)[3].Metrics.RangeQueryFullResponse = true
	*(*copy.Toolsets)[4].Logs.UseRoute = false
	*(*copy.Toolsets)[5].Traces.TempoURL = "https://other"
	*(*copy.Toolsets)[6].CNIDiagnostics.PWRUImage = "other:latest"
	copy.CABundleRefs[0].Secret.Name = "other"
	if !reflect.DeepEqual(in, out) {
		t.Fatal("deepcopy mutations aliased original")
	}
	refs := in.CAReferences()
	if len(refs) != 3 || refs[0].ConfigMap.Name != "mesh-ca" || refs[1].Secret.Name != "flows-ca" || refs[2].Secret.Name != "shared-ca" {
		t.Fatalf("wrong reference order: %+v", refs)
	}
	refs[0].ConfigMap.Key = "other"
	refs[2].Secret.Name = "other"
	if !reflect.DeepEqual(in, out) {
		t.Fatal("CAReferences aliases original")
	}
	var absent *MCPKubeServerConfiguration
	if absent.CAReferences() != nil || (&MCPKubeServerConfiguration{}).CAReferences() != nil {
		t.Fatal("unexpected implicit CA references")
	}
	if (MCPToolsetSelection{}).Name() != "" || (MCPToolsetSelection{Core: &MCPEmptyConfig{}, Config: &MCPEmptyConfig{}}).Name() != "" {
		t.Fatal("invalid union must have no name")
	}
	// Although admission rejects nonempty blocks, copying must not alias maps.
	selection := MCPToolsetSelection{Core: &MCPEmptyConfig{"unknown": "original"}}
	selectionCopy := selection.DeepCopy()
	(*selectionCopy.Core)["unknown"] = "changed"
	if (*selection.Core)["unknown"] != "original" {
		t.Fatal("empty-block deepcopy aliases original map")
	}
	var nilBlock MCPEmptyConfig
	if nilBlock.DeepCopy() != nil {
		t.Fatal("empty-block deepcopy changed nil presence")
	}
}

// TestMCPAdmission installs the generated CRD in envtest. Installation also
// catches CEL compilation and static cost-limit failures, not just Go decoding.
// Run through make test with its default Kubernetes 1.27.1 assets; newer CEL
// versions can hide static-cost and empty-object compatibility regressions.
func TestMCPAdmission(t *testing.T) {
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest/install CRD (including CEL costs): %v", err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	resource := client.Resource(schema.GroupVersionResource{Group: GroupVersion.Group, Version: GroupVersion.Version, Resource: "olsconfigs"})
	validate := func(t *testing.T, config string, invalid bool, strict bool) {
		t.Helper()
		input := fmt.Sprintf(`{"apiVersion":"ols.openshift.io/v1alpha1","kind":"OLSConfig","metadata":{"name":"cluster"},"spec":{"llm":{"providers":[]},"ols":{"defaultProvider":"","defaultModel":"","mcpKubeServerConfig":%s}}}`, config)
		var obj unstructured.Unstructured
		if err := json.Unmarshal([]byte(input), &obj.Object); err != nil {
			t.Fatal(err)
		}
		options := metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}
		if strict {
			options.FieldValidation = metav1.FieldValidationStrict
		} else {
			options.FieldValidation = metav1.FieldValidationIgnore
		}
		_, err := resource.Create(context.Background(), &obj, options)
		if (err != nil) != invalid {
			t.Fatalf("invalid=%t, error=%v, config=%s", invalid, err, config)
		}
	}
	ca := `{"configMap":{"name":"mesh-ca","key":"ca.crt"}}`
	blocks := map[string]string{
		"core": "{}", "config": "{}", "helm": "{}", "kcp": "{}", "ossm": `{"url":"https://kiali.example.com","caBundleRef":` + ca + `}`,
		"kubevirt": "{}", "netobserv": "{}", "tekton": "{}", "cluster-diagnostics": "{}", "cni-diagnostics": "{}", "netedge": "{}", "oadp": "{}", "ovn-kubernetes": "{}",
		"observability/metrics": "{}", "observability/logs": "{}", "observability/traces": "{}", "observability/otelcol": "{}",
	}
	var all []string
	for name, block := range blocks {
		entry := fmt.Sprintf(`{"%s":%s}`, name, block)
		all = append(all, entry)
		t.Run("supported/"+name, func(t *testing.T) {
			validate(t, `{"toolsets":[`+entry+`]}`, false, true)
			var selection MCPToolsetSelection
			if err := json.Unmarshal([]byte(entry), &selection); err != nil {
				t.Fatal(err)
			}
			if selection.Name() != name {
				t.Fatalf("Name()=%q, want %q", selection.Name(), name)
			}
			validate(t, `{"toolsets":[`+entry+`,`+entry+`]}`, true, true)
			if selection.Core != nil || selection.Config != nil || selection.KCP != nil || selection.KubeVirt != nil || selection.Tekton != nil || selection.ClusterDiagnostics != nil || selection.NetEdge != nil || selection.OADP != nil || selection.OVNKubernetes != nil || selection.OtelCol != nil {
				validate(t, fmt.Sprintf(`{"toolsets":[{"%s":{"unsupported":true}}]}`, name), true, true)
			}
		})
	}
	t.Run("all seventeen", func(t *testing.T) { validate(t, `{"toolsets":[`+strings.Join(all, ",")+`]}`, false, true) })
	cases := []struct {
		name, config string
		invalid      bool
	}{
		{"omitted", `{}`, false}, {"timeout only", `{"timeout":60}`, false}, {"empty", `{"toolsets":[]}`, false},
		{"empty entry", `{"toolsets":[{}]}`, true}, {"two keys", `{"toolsets":[{"core":{},"config":{}}]}`, true}, {"null block", `{"toolsets":[{"core":null}]}`, true},
		{"unsupported", `{"toolsets":[{"openshift/mustgather":{}}]}`, true}, {"unknown", `{"toolsets":[{"unknown":{}}]}`, true},
		{"too many", `{"toolsets":[` + strings.Join(append(all, `{"core":{}}`), ",") + `]}`, true},
		{"CA empty", `{"caBundleRefs":[{}]}`, true}, {"CA both", `{"caBundleRefs":[{"configMap":{"name":"ca","key":"ca.crt"},"secret":{"name":"ca","key":"ca.crt"}}]}`, true},
		{"CA missing name", `{"caBundleRefs":[{"secret":{"key":"ca.crt"}}]}`, true}, {"CA missing key", `{"caBundleRefs":[{"secret":{"name":"ca"}}]}`, true},
		{"CA invalid name", `{"caBundleRefs":[{"secret":{"name":"Bad_Name","key":"ca.crt"}}]}`, true}, {"CA invalid key", `{"caBundleRefs":[{"secret":{"name":"ca","key":"../ca"}}]}`, true},
		{"CA both kinds", `{"caBundleRefs":[` + ca + `,{"secret":{"name":"private-ca","key":"bundle.pem"}}]}`, false},
		{"OSSM empty", `{"toolsets":[{"ossm":{}}]}`, true}, {"OSSM missing CA", `{"toolsets":[{"ossm":{"url":"https://kiali.example.com"}}]}`, true},
		{"OSSM null CA", `{"toolsets":[{"ossm":{"url":"https://kiali.example.com","caBundleRef":null}}]}`, true},
		{"netobserv URL needs CA", `{"toolsets":[{"netobserv":{"url":"https://flows.example.com"}}]}`, true},
		{"netobserv URL CA", `{"toolsets":[{"netobserv":{"url":"https://flows.example.com","caBundleRef":` + ca + `}}]}`, false},
		{"netobserv service CA", `{"toolsets":[{"netobserv":{"namespace":"flows","service":"plugin","port":65535,"caBundleRef":` + ca + `}}]}`, false},
		{"netobserv invalid namespace", `{"toolsets":[{"netobserv":{"namespace":"a.b"}}]}`, true}, {"netobserv invalid service", `{"toolsets":[{"netobserv":{"service":"1plugin"}}]}`, true},
		{"netobserv port zero", `{"toolsets":[{"netobserv":{"port":0}}]}`, true}, {"netobserv port high", `{"toolsets":[{"netobserv":{"port":65536}}]}`, true},
		{"helm secret", `{"toolsets":[{"helm":{"storageDriver":"secret"}}]}`, true}, {"helm explicit empty storage", `{"toolsets":[{"helm":{"storageDriver":""}}]}`, true},
		{"helm registries", `{"toolsets":[{"helm":{"allowedRegistries":["oci://registry.example.com/charts","https://charts.example.com"]}}]}`, false},
		{"helm HTTP registry", `{"toolsets":[{"helm":{"allowedRegistries":["http://charts.example.com"]}}]}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { validate(t, tc.config, tc.invalid, true) })
	}
	for _, field := range []string{"namespace", "service", "port"} {
		value := `"netobserv"`
		if field == "port" {
			value = "9001"
		}
		t.Run("netobserv conflicting/"+field, func(t *testing.T) {
			validate(t, `{"toolsets":[{"netobserv":{"url":"https://flows.example.com","caBundleRef":`+ca+`,"`+field+`":`+value+`}}]}`, true, true)
		})
	}
	for _, target := range []struct{ name, field string }{
		{"ossm", "url"}, {"netobserv", "url"},
		{"observability/metrics", "prometheusURL"}, {"observability/metrics", "alertmanagerURL"},
		{"observability/logs", "lokiURL"}, {"observability/traces", "tempoURL"},
	} {
		name, field := target.name, target.field
		for _, endpoint := range []string{"https://example.com/api", "HTTPS://example.com/api", "https://[::1]:9443/api", "http://example.com", "https://", "relative/path", "https://a:b:c/", "https://user:pass@example.com", "HTTPS://user@example.com", "HTTPS://user:pass@example.com"} {
			t.Run(name+"/"+field+" URL/"+endpoint, func(t *testing.T) {
				block := fmt.Sprintf(`{"%s":%q`, field, endpoint)
				if name == "ossm" || name == "netobserv" {
					block += `,"caBundleRef":` + ca
				}
				block += "}"
				valid := endpoint == "https://example.com/api" || endpoint == "HTTPS://example.com/api" || endpoint == "https://[::1]:9443/api"
				validate(t, fmt.Sprintf(`{"toolsets":[{"%s":%s}]}`, name, block), !valid, true)
			})
		}
	}
	for _, name := range []string{"observability/metrics", "observability/logs", "observability/traces"} {
		for _, mode := range []string{"header", "kubeconfig", "", "unknown"} {
			t.Run(name+" auth/"+mode, func(t *testing.T) {
				validate(t, fmt.Sprintf(`{"toolsets":[{"%s":{"authMode":%q}}]}`, name, mode), mode != "header" && mode != "kubeconfig", true)
			})
		}
		if name == "observability/metrics" {
			continue
		}
		field := "lokiURL"
		if name == "observability/traces" {
			field = "tempoURL"
		}
		t.Run(name+" discovery", func(t *testing.T) {
			validate(t, fmt.Sprintf(`{"toolsets":[{"%s":{"useRoute":true}}]}`, name), false, true)
			validate(t, fmt.Sprintf(`{"toolsets":[{"%s":{"useRoute":false}}]}`, name), true, true)
			validate(t, fmt.Sprintf(`{"toolsets":[{"%s":{"useRoute":true,"%s":"https://example.com"}}]}`, name, field), true, true)
		})
	}
	for _, guard := range []struct {
		value                string
		valid, metric, label bool
	}{
		{"", true, true, true}, {" All ", true, true, true}, {"none", true, false, false}, {"!tsdb", true, false, false}, {"!require-label-matcher", true, true, true},
		{"max-metric-cardinality", true, true, false}, {"disallow-blanket-regex", true, false, true}, {" , MAX-METRIC-CARDINALITY, disallow-blanket-regex, ", true, true, true},
		{",,", true, false, false}, {"!tsdb,!max-metric-cardinality", true, false, false}, {"!disallow-blanket-regex", true, true, false},
		{"\u2003!tsdb\u2003, \u2003!require-label-matcher\u2003", true, false, false},
		{"\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000", true, true, true},
		{"\u0085MAX-METRIC-CARDINALITY\u2028,\u3000DISALLOW-BLANKET-REGEX\u00a0", true, true, true},
		{strings.Repeat("max-metric-cardinality,", 20), true, true, false},
		{strings.Repeat("!require-label-matcher,", 20), true, true, true},
		{strings.Repeat(" ", 509) + "all", true, true, true},
		{strings.Repeat(",", 512), true, false, false},
		{strings.Repeat(",", 513), false, false, false},
		{strings.Repeat(" ", 510) + "all", false, false, false},
		{"diſallow-blanket-regex", false, false, false},
		{"require-label-matcher\u200b", false, false, false},
		{"tsdb", false, false, false}, {"all,none", false, false, false}, {"require-label-matcher,!tsdb", false, false, false}, {"! tsdb", false, false, false}, {"unknown", false, false, false},
	} {
		t.Run("guardrails/"+guard.value, func(t *testing.T) {
			encoded, err := json.Marshal(guard.value)
			if err != nil {
				t.Fatal(err)
			}
			base := `"guardrails":` + string(encoded)
			validate(t, `{"toolsets":[{"observability/metrics":{`+base+`}}]}`, !guard.valid, true)
			validate(t, `{"toolsets":[{"observability/metrics":{`+base+`,"maxMetricCardinality":1}}]}`, !guard.valid || !guard.metric, true)
			validate(t, `{"toolsets":[{"observability/metrics":{`+base+`,"maxLabelCardinality":0}}]}`, !guard.valid || !guard.label, true)
		})
	}
	for _, limits := range []struct {
		fields  string
		invalid bool
	}{
		{`"maxMetricCardinality":1`, true}, {`"maxLabelCardinality":0`, true},
		{`"prometheusURL":"https://prom.example.com","maxMetricCardinality":1,"maxLabelCardinality":0,"rangeQueryFullResponse":false`, false},
		{`"alertmanagerURL":"https://alerts.example.com","maxMetricCardinality":1`, true},
		{`"guardrails":"all","maxMetricCardinality":0`, true}, {`"guardrails":"all","maxLabelCardinality":-1`, true},
	} {
		t.Run("limits/"+limits.fields, func(t *testing.T) {
			validate(t, `{"toolsets":[{"observability/metrics":{`+limits.fields+`}}]}`, limits.invalid, true)
		})
	}
	for _, field := range []string{"kernelDebugImage", "tcpdumpImage", "pwruImage"} {
		for _, tc := range []struct {
			image string
			valid bool
		}{
			{"nicolaka/netshoot:v0.16", true},
			{"mirror.example.com:5000/cilium/pwru:v1.0.10", true},
			{"mirror.example.com/image@sha256:" + strings.Repeat("a0", 32), true},
			{"mirror.example.com/image:TAG@sha256:" + strings.Repeat("a0", 32), true},
			{"mirror.example.com/image@sha256:" + strings.Repeat("A", 64), false},
			{"mirror.example.com/image@sha256:" + strings.Repeat("aA", 32), false},
			{"mirror.example.com/image@sha512:" + strings.Repeat("a", 128), false},
			{"", false}, {"https://mirror.example.com/image", false},
			{"bad image", false}, {"repo:bad tag", false}, {"repo@sha256:abc", false},
		} {
			t.Run(field+"/"+tc.image, func(t *testing.T) {
				validate(t, fmt.Sprintf(`{"toolsets":[{"cni-diagnostics":{"%s":%q}}]}`, field, tc.image), !tc.valid, true)
			})
		}
	}
	// Explicitly record the unavoidable pruning behavior, rather than claiming
	// typed CEL admission can reject data no longer visible to it.
	t.Run("unknown pruning limitation", func(t *testing.T) {
		// Empty blocks are zero-property maps, so their unknown settings are
		// rejected even without Strict; typed configurable blocks still prune.
		validate(t, `{"toolsets":[{"core":{"unsupported":true}}]}`, true, false)
		validate(t, `{"toolsets":[{"core":{"unsupported":"value"}}]}`, true, false)
		for _, config := range []string{`{"toolsets":[{"core":{},"openshift/mustgather":{}}]}`, `{"toolsets":[{"observability/metrics":{"insecure":true}}]}`} {
			validate(t, config, false, false)
			validate(t, config, true, true)
		}
		validate(t, `{"toolsets":[{"openshift/mustgather":{}}]}`, true, false)
	})
}
