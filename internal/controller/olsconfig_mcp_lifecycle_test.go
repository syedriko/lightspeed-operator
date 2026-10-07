package controller

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	consolev1 "github.com/openshift/api/console/v1"
	imagev1 "github.com/openshift/api/image/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	olsv1alpha1 "github.com/openshift/lightspeed-operator/api/v1alpha1"
	"github.com/openshift/lightspeed-operator/internal/controller/utils"
)

func TestIndependentResourcesRemovesDisabledMCPWithoutReadinessHistory(t *testing.T) {
	for _, ownedTrust := range []bool{true, false} {
		name := "owned trust"
		if !ownedTrust {
			name = "unowned trust"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			// Phase 1 invokes all components, not just MCP. Register their APIs
			// so missing scheme entries cannot masquerade as operand failures.
			for _, add := range []func(*runtime.Scheme) error{
				clientgoscheme.AddToScheme, olsv1alpha1.AddToScheme,
				configv1.AddToScheme, consolev1.AddToScheme, imagev1.AddToScheme,
				operatorv1.AddToScheme, monitoringv1.AddToScheme,
			} {
				if err := add(scheme); err != nil {
					t.Fatal(err)
				}
			}

			cr := utils.GetDefaultOLSConfigCR()
			cr.UID = "6d8fa747-3bfb-4b38-91b0-f30de2f64b73"
			cr.Generation = 2
			cr.Spec.OLSConfig.IntrospectionEnabled = utils.BoolPtr(false)
			cr.Spec.OLSConfig.ByokRAGOnly = true
			cr.Spec.OLSConfig.MCPKubeServerConfig = &olsv1alpha1.MCPKubeServerConfiguration{
				CABundleRefs: []olsv1alpha1.MCPCAReference{{
					ConfigMap: &olsv1alpha1.MCPCAKeyReference{Name: "user-invalid-mcp-ca", Key: "ca.crt"},
				}},
			}
			// A previous validation/resource failure replaced all readiness
			// conditions. The old wasComponentEnabled guard would skip cleanup.
			cr.Status = olsv1alpha1.OLSConfigStatus{
				OverallStatus: olsv1alpha1.OverallStatusNotReady,
				Conditions: []metav1.Condition{{
					Type: "ResourceReconciliation", Status: metav1.ConditionFalse,
					Reason: "Failed", Message: "MCP CA validation failed",
					ObservedGeneration: cr.Generation, LastTransitionTime: metav1.Now(),
				}},
			}
			if wasComponentEnabled(cr, utils.TypeMCPServerReady) {
				t.Fatal("fixture unexpectedly retains MCP readiness history")
			}
			source := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "user-invalid-mcp-ca", Namespace: utils.OLSNamespaceDefault,
					Labels:      map[string]string{"user": "preserve"},
					Annotations: map[string]string{"user": "preserve"},
				},
				Data:       map[string]string{"ca.crt": "not a PEM certificate"},
				BinaryData: map[string][]byte{"unrelated": []byte("preserve")},
			}
			trust := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "openshift-mcp-server-trust", Namespace: utils.OLSNamespaceDefault},
				Data:       map[string]string{"service-ca.crt": utils.TestCACert},
			}
			managed := []client.Object{
				&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: utils.OpenShiftMCPServerDeploymentName}},
				&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: utils.OpenShiftMCPServerServiceName}},
				&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: utils.OpenShiftMCPServerNetworkPolicyName}},
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: utils.OpenShiftMCPServerConfigCmName}},
				trust,
				&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: utils.OpenShiftMCPServerServiceAccountName}},
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: utils.OpenShiftMCPServerCertsSecretName}},
				&monitoringv1.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: utils.OpenShiftMCPServerServiceMonitorName}},
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: utils.LegacyOpenShiftMCPServerCAConfigMapName}},
			}
			for _, obj := range managed {
				obj.SetNamespace(utils.OLSNamespaceDefault)
				if obj == trust && !ownedTrust {
					continue
				}
				if err := controllerutil.SetControllerReference(cr, obj, scheme); err != nil {
					t.Fatal(err)
				}
				if !metav1.IsControlledBy(obj, cr) {
					t.Fatalf("fixture %T %s is not controlled by the CR", obj, obj.GetName())
				}
			}
			objects := append([]client.Object{cr, source}, managed...)
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(cr).WithObjects(objects...).
				WithInterceptorFuncs(interceptor.Funcs{
					// Fail other operands deterministically while leaving Get,
					// Delete and status updates backed by the real fake store.
					Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
						return fmt.Errorf("injected operand create failure for %s", obj.GetName())
					},
				}).Build()
			options := getDefaultReconcilerOptions(utils.OLSNamespaceDefault)
			options.LightspeedServicePostgresImage = "postgres:test"
			options.OtelCollectorImage = "otel:test"
			options.OpenShiftMCPServerImage = "mcp:test"
			r := &OLSConfigReconciler{
				Client: c, Logger: logr.Discard(), Options: options,
				WatcherConfig: &utils.WatcherConfig{},
			}
			if r.GetScheme() != scheme {
				t.Fatal("root reconciler must use the fully registered client scheme")
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(source), source); err != nil {
				t.Fatal(err)
			}
			sourceBefore := source.DeepCopy()
			if err := c.Get(ctx, client.ObjectKeyFromObject(trust), trust); err != nil {
				t.Fatal(err)
			}
			trustBefore := trust.DeepCopy()
			// Assert fixtures exist before Phase 1 so deletion checks cannot
			// pass merely because setup omitted the resources.
			for _, obj := range managed {
				if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
					t.Fatal(err)
				}
			}

			for attempt := 1; attempt <= 3; attempt++ {
				if err := c.Get(ctx, client.ObjectKeyFromObject(cr), cr); err != nil {
					t.Fatal(err)
				}
				if wasComponentEnabled(cr, utils.TypeMCPServerReady) {
					t.Fatal("cleanup must be exercised without MCP readiness history")
				}
				// Call the root Phase 1 method, not ocpmcp.Remove directly.
				err := r.reconcileIndependentResources(ctx, cr)
				if err == nil || !strings.Contains(err.Error(), "console UI resources") || !strings.Contains(err.Error(), "postgres resources") {
					t.Fatalf("attempt %d: expected unrelated operand failures, got %v", attempt, err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(cr), cr); err != nil {
					t.Fatal(err)
				}
				foundInjectedFailure, foundCleanupFailure, foundOwnershipRejection := false, false, false
				for _, condition := range cr.Status.Conditions {
					if condition.Type != "ResourceReconciliation" || condition.Status != metav1.ConditionFalse {
						t.Fatalf("unexpected condition after resource failure: %+v", condition)
					}
					foundInjectedFailure = foundInjectedFailure || strings.Contains(condition.Message, "injected operand create failure")
					foundCleanupFailure = foundCleanupFailure || strings.Contains(condition.Message, "openshift-mcp-server cleanup")
					foundOwnershipRejection = foundOwnershipRejection || strings.Contains(condition.Message, "refusing to delete unowned trust ConfigMap")
				}
				if !foundInjectedFailure || foundCleanupFailure == ownedTrust {
					t.Fatalf("attempt %d: unexpected persisted failure conditions: %+v", attempt, cr.Status.Conditions)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(source), source); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(source, sourceBefore) {
					t.Fatalf("attempt %d: disabled cleanup mutated or adopted the invalid user CA", attempt)
				}
				if ownedTrust {
					for _, obj := range managed {
						if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); !apierrors.IsNotFound(err) {
							t.Fatalf("attempt %d: managed %T %s survived cleanup: %v", attempt, obj, obj.GetName(), err)
						}
					}
				} else {
					if err := c.Get(ctx, client.ObjectKeyFromObject(trust), trust); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(trust, trustBefore) {
						t.Fatalf("attempt %d: disabled cleanup mutated unowned trust", attempt)
					}
					if !foundOwnershipRejection {
						t.Fatalf("attempt %d: expected persisted trust ownership rejection: %+v", attempt, cr.Status.Conditions)
					}
				}
			}
		})
	}
}
