package utils

import (
	"errors"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"

	olsv1alpha1 "github.com/openshift/lightspeed-operator/api/v1alpha1"
)

func TestExternalMCPCAReferences(t *testing.T) {
	toolsets := []olsv1alpha1.MCPToolsetSelection{
		{OSSM: &olsv1alpha1.MCPOSSMConfig{CABundleRef: &olsv1alpha1.MCPCAReference{
			Secret: &olsv1alpha1.MCPCAKeyReference{Name: "ossm-ca", Key: "ca.crt"},
		}}},
		{NetObserv: &olsv1alpha1.MCPNetObservConfig{CABundleRef: &olsv1alpha1.MCPCAReference{
			ConfigMap: &olsv1alpha1.MCPCAKeyReference{Name: "netobserv-ca", Key: "ca.crt"},
		}}},
	}
	for _, enabled := range []*bool{nil, BoolPtr(true), BoolPtr(false)} {
		cr := &olsv1alpha1.OLSConfig{}
		cr.Spec.OLSConfig.IntrospectionEnabled = enabled
		cr.Spec.LLMConfig.Providers = []olsv1alpha1.ProviderSpec{{
			Name: "test", CredentialsSecretRef: corev1.LocalObjectReference{Name: "shared-secret"},
		}}
		cr.Spec.OLSConfig.AdditionalCAConfigMapRef = &corev1.LocalObjectReference{Name: "shared-cm"}
		cr.Spec.OLSConfig.DeploymentConfig.AlertsAdapter.ConfigMapRef = &corev1.LocalObjectReference{Name: "shared-cm"}
		cr.Spec.OLSConfig.MCPKubeServerConfig = &olsv1alpha1.MCPKubeServerConfiguration{
			Toolsets: &toolsets,
			CABundleRefs: []olsv1alpha1.MCPCAReference{
				{Secret: &olsv1alpha1.MCPCAKeyReference{Name: "shared-secret", Key: "ca.crt"}},
				{ConfigMap: &olsv1alpha1.MCPCAKeyReference{Name: "shared-cm", Key: "ca.crt"}},
			},
		}
		secrets, cms := map[string][]string{}, map[string][]string{}
		if err := ForEachExternalSecret(cr, func(name, source string) error {
			secrets[name] = append(secrets[name], source)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := ForEachExternalConfigMap(cr, func(name, source string) error {
			cms[name] = append(cms[name], source)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		wantSecrets := map[string][]string{"shared-secret": {"llm-provider-test"}}
		wantCMs := map[string][]string{"shared-cm": {"additional-ca", "alerts-adapter"}}
		if BoolDeref(enabled, true) {
			wantSecrets["ossm-ca"] = []string{"mcp-ca"}
			wantSecrets["shared-secret"] = append(wantSecrets["shared-secret"], "mcp-ca")
			wantCMs["netobserv-ca"] = []string{"mcp-ca"}
			wantCMs["shared-cm"] = []string{"additional-ca", "mcp-ca", "alerts-adapter"}
		}
		if !reflect.DeepEqual(secrets, wantSecrets) || !reflect.DeepEqual(cms, wantCMs) {
			t.Fatalf("enabled=%v: secrets=%v, configmaps=%v", enabled, secrets, cms)
		}
	}
}

func TestExternalMCPCAReferencesStopOnCallbackError(t *testing.T) {
	for _, secret := range []bool{false, true} {
		cr := &olsv1alpha1.OLSConfig{}
		ref := olsv1alpha1.MCPCAReference{}
		key := &olsv1alpha1.MCPCAKeyReference{Name: "ca", Key: "ca.crt"}
		enumerate := ForEachExternalConfigMap
		if secret {
			ref.Secret = key
			enumerate = ForEachExternalSecret
		} else {
			ref.ConfigMap = key
		}
		cr.Spec.OLSConfig.MCPKubeServerConfig = &olsv1alpha1.MCPKubeServerConfiguration{
			CABundleRefs: []olsv1alpha1.MCPCAReference{ref, ref},
		}
		want := errors.New("stop")
		calls := 0
		err := enumerate(cr, func(_, _ string) error { calls++; return want })
		if !errors.Is(err, want) || calls != 1 {
			t.Fatalf("secret=%t: error=%v, calls=%d", secret, err, calls)
		}
	}
}
