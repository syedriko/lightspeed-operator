package controller

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	olsv1alpha1 "github.com/openshift/lightspeed-operator/api/v1alpha1"
	"github.com/openshift/lightspeed-operator/internal/controller/utils"
)

func newMCPCAHelperReconciler(t *testing.T, objects ...client.Object) *OLSConfigReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := olsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return &OLSConfigReconciler{
		Client:        fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(),
		Options:       getDefaultReconcilerOptions(utils.OLSNamespaceDefault),
		WatcherConfig: &utils.WatcherConfig{},
	}
}

func TestMCPCAWatchPredicates(t *testing.T) {
	for _, secret := range []bool{false, true} {
		for _, enabled := range []*bool{nil, utils.BoolPtr(true), utils.BoolPtr(false)} {
			cr := &olsv1alpha1.OLSConfig{ObjectMeta: metav1.ObjectMeta{Name: utils.OLSConfigName}}
			cr.Spec.OLSConfig.IntrospectionEnabled = enabled
			ref := olsv1alpha1.MCPCAReference{}
			key := &olsv1alpha1.MCPCAKeyReference{Name: "missing-ca", Key: "ca.crt"}
			var source client.Object = &corev1.ConfigMap{}
			if secret {
				ref.Secret = key
				source = &corev1.Secret{}
			} else {
				ref.ConfigMap = key
			}
			// Exercise selected toolset refs, not only the shared list.
			toolsets := []olsv1alpha1.MCPToolsetSelection{{OSSM: &olsv1alpha1.MCPOSSMConfig{CABundleRef: &ref}}}
			cr.Spec.OLSConfig.MCPKubeServerConfig = &olsv1alpha1.MCPKubeServerConfiguration{Toolsets: &toolsets}
			r := newMCPCAHelperReconciler(t, cr) // Source missing, mappings unpublished.
			predicate := r.shouldWatchConfigMap
			if secret {
				predicate = r.shouldWatchSecret
			}
			source.SetName(key.Name)
			source.SetNamespace(utils.OLSNamespaceDefault)
			want := utils.BoolDeref(enabled, true)
			for _, annotated := range []bool{false, true} {
				if annotated {
					source.SetAnnotations(map[string]string{utils.WatcherAnnotationKey: utils.OLSConfigName})
				}
				if got := predicate(source); got != want {
					t.Fatalf("secret=%t enabled=%v annotated=%t: watch=%t, want %t", secret, enabled, annotated, got, want)
				}
				source.SetNamespace("other-namespace")
				if predicate(source) {
					t.Fatal("watched a same-name user CA in another namespace")
				}
				source.SetNamespace(utils.OLSNamespaceDefault)
			}
			// Disable preserves a shared non-MCP consumer.
			if !want {
				if secret {
					cr.Spec.LLMConfig.Providers = []olsv1alpha1.ProviderSpec{{Name: "test", CredentialsSecretRef: corev1.LocalObjectReference{Name: key.Name}}}
				} else {
					cr.Spec.OLSConfig.DeploymentConfig.AlertsAdapter.ConfigMapRef = &corev1.LocalObjectReference{Name: key.Name}
				}
				r = newMCPCAHelperReconciler(t, cr)
				if secret && !r.shouldWatchSecret(source) || !secret && !r.shouldWatchConfigMap(source) {
					t.Fatal("disabled MCP suppressed another consumer")
				}
			}
		}
	}
}

func TestMCPCAAnnotationMappingsUnion(t *testing.T) {
	ctx := context.Background()
	cr := &olsv1alpha1.OLSConfig{ObjectMeta: metav1.ObjectMeta{Name: utils.OLSConfigName}}
	cr.Spec.OLSConfig.CredentialHotReload = utils.BoolPtr(true)
	cr.Spec.LLMConfig.Providers = []olsv1alpha1.ProviderSpec{{
		Name: "test", CredentialsSecretRef: corev1.LocalObjectReference{Name: "shared-secret"},
	}}
	cr.Spec.OLSConfig.AdditionalCAConfigMapRef = &corev1.LocalObjectReference{Name: "shared-cm"}
	cr.Spec.OLSConfig.ProxyConfig = &olsv1alpha1.ProxyConfig{
		ProxyCACertificateRef: &olsv1alpha1.ProxyCACertConfigMapRef{LocalObjectReference: corev1.LocalObjectReference{Name: "shared-cm"}},
	}
	cr.Spec.OLSConfig.DeploymentConfig.AlertsAdapter.ConfigMapRef = &corev1.LocalObjectReference{Name: "shared-cm"}
	cr.Spec.OLSConfig.MCPKubeServerConfig = &olsv1alpha1.MCPKubeServerConfiguration{
		CABundleRefs: []olsv1alpha1.MCPCAReference{
			{Secret: &olsv1alpha1.MCPCAKeyReference{Name: "shared-secret", Key: "ca.crt"}},
			{ConfigMap: &olsv1alpha1.MCPCAKeyReference{Name: "shared-cm", Key: "ca.crt"}},
			{Secret: &olsv1alpha1.MCPCAKeyReference{Name: "missing-secret", Key: "ca.crt"}},
			{ConfigMap: &olsv1alpha1.MCPCAKeyReference{Name: "missing-cm", Key: "ca.crt"}},
		},
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "shared-secret", Namespace: utils.OLSNamespaceDefault},
		Data: map[string][]byte{utils.DefaultCredentialKey: []byte("token")}}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "shared-cm", Namespace: utils.OLSNamespaceDefault}}
	r := newMCPCAHelperReconciler(t, cr, secret, cm)
	if err := r.annotateExternalResources(ctx, cr); err != nil {
		t.Fatal(err)
	}
	if targets, found := r.WatcherConfig.GetAnnotatedConfigMapDeployments(cm.Name); !found ||
		!reflect.DeepEqual(targets, []string{utils.OLSAppServerDeploymentName, utils.AlertsAdapterDeploymentName}) {
		t.Fatalf("shared ConfigMap targets=%v, found=%t", targets, found)
	}
	for _, name := range []string{"shared-secret", "missing-secret"} {
		if targets, found := r.WatcherConfig.GetAnnotatedSecretDeployments(name); !found || len(targets) != 0 {
			t.Fatalf("MCP-only/hot-reload secret %s targets=%v, found=%t", name, targets, found)
		}
	}
	if targets, found := r.WatcherConfig.GetAnnotatedConfigMapDeployments("missing-cm"); !found || len(targets) != 0 {
		t.Fatalf("MCP-only ConfigMap targets=%v, found=%t", targets, found)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil {
		t.Fatal(err)
	}
	if _, found := secret.Annotations[utils.WatcherAnnotationKey]; !found || len(secret.OwnerReferences) != 0 {
		t.Fatal("hot reload removed the shared CA watch or adopted the source")
	}
	// Turn hot reload off: the shared LLM consumer must add its restart target.
	cr.Spec.OLSConfig.CredentialHotReload = utils.BoolPtr(false)
	if err := r.annotateExternalResources(ctx, cr); err != nil {
		t.Fatal(err)
	}
	if targets, _ := r.WatcherConfig.GetAnnotatedSecretDeployments(secret.Name); !reflect.DeepEqual(targets, []string{utils.OLSAppServerDeploymentName}) {
		t.Fatalf("LLM/MCP shared targets=%v", targets)
	}
	cr.Spec.OLSConfig.IntrospectionEnabled = utils.BoolPtr(false)
	if err := r.annotateExternalResources(ctx, cr); err != nil {
		t.Fatal(err)
	}
	if _, found := r.WatcherConfig.GetAnnotatedSecretDeployments("missing-secret"); found {
		t.Fatal("disabled MCP retained its secret mapping")
	}
	if _, found := r.WatcherConfig.GetAnnotatedConfigMapDeployments("missing-cm"); found {
		t.Fatal("disabled MCP retained its ConfigMap mapping")
	}
	if targets, _ := r.WatcherConfig.GetAnnotatedConfigMapDeployments(cm.Name); len(targets) != 2 {
		t.Fatalf("disabled MCP lost shared consumers: %v", targets)
	}
}
