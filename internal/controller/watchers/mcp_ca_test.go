package watchers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	olsv1alpha1 "github.com/openshift/lightspeed-operator/api/v1alpha1"
	"github.com/openshift/lightspeed-operator/internal/controller/reconciler"
	"github.com/openshift/lightspeed-operator/internal/controller/utils"
)

var _ = Describe("MCP CA events", func() {
	ctx := context.Background()
	var calls map[string]int

	BeforeEach(func() {
		calls = make(map[string]int)
		original := restartFuncs
		restartFuncs = make(map[string]RestartFunc)
		for target := range original {
			target := target
			restartFuncs[target] = func(_ reconciler.Reconciler, _ context.Context, _ ...*appsv1.Deployment) error {
				calls[target]++
				return nil
			}
		}
		DeferCleanup(func() { restartFuncs = original })
	})

	// No watcher mappings are required: validation may fail before publishing them.
	newCR := func(secret bool) *olsv1alpha1.OLSConfig {
		cr := utils.GetDefaultOLSConfigCR()
		cr.Spec.OLSConfig.IntrospectionEnabled = nil // Omission defaults to enabled.
		ref := olsv1alpha1.MCPCAReference{}
		key := &olsv1alpha1.MCPCAKeyReference{Name: "backend-ca", Key: "ca.crt"}
		if secret {
			ref.Secret = key
		} else {
			ref.ConfigMap = key
		}
		cr.Spec.OLSConfig.MCPKubeServerConfig = &olsv1alpha1.MCPKubeServerConfiguration{
			CABundleRefs: []olsv1alpha1.MCPCAReference{ref},
		}
		return cr
	}
	newSource := func(secret bool) client.Object {
		meta := metav1.ObjectMeta{Name: "backend-ca", Namespace: utils.OLSNamespaceDefault}
		if secret {
			return &corev1.Secret{ObjectMeta: meta, Data: map[string][]byte{"ca.crt": []byte("invalid PEM")}}
		}
		return &corev1.ConfigMap{ObjectMeta: meta, Data: map[string]string{"ca.crt": "invalid PEM"}}
	}
	fire := func(r reconciler.Reconciler, obj client.Object, action string, q *recordingQueue) {
		if sec, ok := obj.(*corev1.Secret); ok {
			h := &SecretUpdateHandler{Reconciler: r}
			switch action {
			case "create", "recovery":
				h.Create(ctx, event.CreateEvent{Object: sec}, q)
			case "update":
				old := sec.DeepCopy()
				old.Data = map[string][]byte{"ca.crt": []byte("old")}
				h.Update(ctx, event.UpdateEvent{ObjectOld: old, ObjectNew: sec}, q)
			case "delete":
				h.Delete(ctx, event.DeleteEvent{Object: sec}, q)
			case "metadata":
				old := sec.DeepCopy()
				old.Annotations = map[string]string{"unrelated": "change"}
				h.Update(ctx, event.UpdateEvent{ObjectOld: old, ObjectNew: sec}, q)
			}
			return
		}
		cm := obj.(*corev1.ConfigMap)
		h := &ConfigMapUpdateHandler{Reconciler: r}
		switch action {
		case "create", "recovery":
			h.Create(ctx, event.CreateEvent{Object: cm}, q)
		case "update":
			old := cm.DeepCopy()
			old.Data = map[string]string{"ca.crt": "old"}
			h.Update(ctx, event.UpdateEvent{ObjectOld: old, ObjectNew: cm}, q)
		case "binary":
			old := cm.DeepCopy()
			old.BinaryData = map[string][]byte{"ca.crt": []byte("old")}
			h.Update(ctx, event.UpdateEvent{ObjectOld: old, ObjectNew: cm}, q)
		case "delete":
			h.Delete(ctx, event.DeleteEvent{Object: cm}, q)
		case "metadata":
			old := cm.DeepCopy()
			old.Annotations = map[string]string{"unrelated": "change"}
			h.Update(ctx, event.UpdateEvent{ObjectOld: old, ObjectNew: cm}, q)
		}
	}

	It("queues creation, data updates, deletion and recovery without any direct restart or source ownership", func() {
		for _, secret := range []bool{false, true} {
			for _, action := range []string{"create", "update", "delete", "recovery"} {
				cr, source := newCR(secret), newSource(secret)
				r := createTestReconciler(cr, source)
				q := &recordingQueue{}
				fire(r, source, action, q)
				Expect(q.items).To(Equal([]reconcile.Request{{NamespacedName: types.NamespacedName{Name: utils.OLSConfigName}}}))
				Expect(calls).To(BeEmpty())
				stored := source.DeepCopyObject().(client.Object)
				Expect(r.Get(ctx, client.ObjectKeyFromObject(source), stored)).To(Succeed())
				Expect(stored.GetOwnerReferences()).To(BeEmpty())
			}
		}
	})

	It("queues selected OSSM and NetObserv references as well as shared process refs", func() {
		for _, secret := range []bool{false, true} {
			cr, source := newCR(secret), newSource(secret)
			ref := cr.Spec.OLSConfig.MCPKubeServerConfig.CABundleRefs[0]
			toolsets := []olsv1alpha1.MCPToolsetSelection{
				{OSSM: &olsv1alpha1.MCPOSSMConfig{CABundleRef: &ref}},
				{NetObserv: &olsv1alpha1.MCPNetObservConfig{CABundleRef: &ref}},
			}
			cr.Spec.OLSConfig.MCPKubeServerConfig.CABundleRefs = nil
			cr.Spec.OLSConfig.MCPKubeServerConfig.Toolsets = &toolsets
			for _, action := range []string{"create", "update", "delete"} {
				q := &recordingQueue{}
				fire(createTestReconciler(cr, source), source, action, q)
				Expect(q.items).To(HaveLen(1)) // Duplicate refs do not duplicate the event.
				Expect(calls).To(BeEmpty())
			}
		}
	})

	It("ignores an owned generated trust ConfigMap that is not a user reference", func() {
		source := newSource(false)
		source.SetName("generated-mcp-trust")
		source.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: utils.OLSConfigAPIVersion, Kind: utils.OLSConfigKind, Name: utils.OLSConfigName}})
		for _, action := range []string{"create", "update", "delete"} {
			q := &recordingQueue{}
			fire(createTestReconciler(newCR(false), source), source, action, q)
			Expect(q.items).To(BeEmpty())
			Expect(calls).To(BeEmpty())
		}
	})

	It("queues binary data changes but ignores metadata-only changes", func() {
		source := newSource(false)
		r := createTestReconciler(newCR(false), source)
		q := &recordingQueue{}
		fire(r, source, "binary", q)
		Expect(q.items).To(HaveLen(1))
		for _, secret := range []bool{false, true} {
			source := newSource(secret)
			q := &recordingQueue{}
			fire(createTestReconciler(newCR(secret), source), source, "metadata", q)
			Expect(q.items).To(BeEmpty())
		}
		Expect(calls).To(BeEmpty())
	})

	It("preserves LLM, proxy/additional CA and alerts consumers, including after MCP disable", func() {
		for _, enabled := range []bool{true, false} {
			for _, secret := range []bool{false, true} {
				cr, source := newCR(secret), newSource(secret)
				cr.Spec.OLSConfig.IntrospectionEnabled = utils.BoolPtr(enabled)
				if secret {
					cr.Spec.LLMConfig.Providers[0].CredentialsSecretRef.Name = source.GetName()
				} else {
					cr.Spec.OLSConfig.AdditionalCAConfigMapRef = &corev1.LocalObjectReference{Name: source.GetName()}
					cr.Spec.OLSConfig.ProxyConfig = &olsv1alpha1.ProxyConfig{
						ProxyCACertificateRef: &olsv1alpha1.ProxyCACertConfigMapRef{
							LocalObjectReference: corev1.LocalObjectReference{Name: source.GetName()},
						},
					}
					cr.Spec.OLSConfig.DeploymentConfig.AlertsAdapter.ConfigMapRef = &corev1.LocalObjectReference{Name: source.GetName()}
				}
				for _, action := range []string{"create", "update", "delete"} {
					calls = make(map[string]int)
					q := &recordingQueue{}
					fire(createTestReconciler(cr, source), source, action, q)
					if enabled || action == "delete" {
						Expect(q.items).NotTo(BeEmpty())
					} else {
						Expect(q.items).To(BeEmpty())
					}
					Expect(calls[utils.OpenShiftMCPServerDeploymentName]).To(BeZero())
					if action == "delete" {
						Expect(calls).To(BeEmpty())
					} else {
						Expect(calls[utils.OLSAppServerDeploymentName]).To(Equal(1))
						if !secret {
							Expect(calls[utils.AlertsAdapterDeploymentName]).To(Equal(1))
						}
					}
				}
			}
		}
	})

	It("preserves MCP header consumers even for a custom server named ca and with LLM hot reload", func() {
		cr, source := newCR(true), newSource(true)
		cr.Spec.OLSConfig.CredentialHotReload = utils.BoolPtr(true)
		cr.Spec.LLMConfig.Providers[0].CredentialsSecretRef.Name = source.GetName()
		cr.Spec.MCPServers = []olsv1alpha1.MCPServerConfig{{
			Name: "ca", Headers: []olsv1alpha1.MCPHeader{{
				Name: "Authorization", ValueFrom: olsv1alpha1.MCPHeaderValueSource{
					Type:      olsv1alpha1.MCPHeaderSourceTypeSecret,
					SecretRef: &corev1.LocalObjectReference{Name: source.GetName()},
				},
			}},
		}}
		q := &recordingQueue{}
		fire(createTestReconciler(cr, source), source, "update", q)
		Expect(q.items).To(HaveLen(1))
		Expect(calls).To(Equal(map[string]int{utils.OLSAppServerDeploymentName: 1}))
	})

	It("ignores disabled MCP-only and same-name sources in other namespaces even with annotations", func() {
		for _, secret := range []bool{false, true} {
			for _, disabled := range []bool{false, true} {
				cr, source := newCR(secret), newSource(secret)
				source.SetAnnotations(map[string]string{utils.WatcherAnnotationKey: utils.OLSConfigName})
				if disabled {
					cr.Spec.OLSConfig.IntrospectionEnabled = utils.BoolPtr(false)
				} else {
					source.SetNamespace("another-namespace")
				}
				for _, action := range []string{"create", "update", "delete"} {
					q := &recordingQueue{}
					fire(createTestReconciler(cr, source), source, action, q)
					Expect(q.items).To(BeEmpty())
					Expect(calls).To(BeEmpty())
				}
			}
		}
	})

	It("ignores stale annotations and mappings after CA reference removal while preserving shared consumers", func() {
		for _, secret := range []bool{false, true} {
			for _, shared := range []bool{false, true} {
				for _, mapped := range []bool{false, true} {
					// Alerts routing needs its published deployment mapping; the
					// unmapped case exercises the app-server fallback instead.
					cr, source := newCR(secret), newSource(secret)
					source.SetAnnotations(map[string]string{utils.WatcherAnnotationKey: utils.OLSConfigName})
					if shared {
						if secret {
							cr.Spec.LLMConfig.Providers[0].CredentialsSecretRef.Name = source.GetName()
						} else {
							cr.Spec.OLSConfig.AdditionalCAConfigMapRef = &corev1.LocalObjectReference{Name: source.GetName()}
							cr.Spec.OLSConfig.ProxyConfig = &olsv1alpha1.ProxyConfig{
								ProxyCACertificateRef: &olsv1alpha1.ProxyCACertConfigMapRef{
									LocalObjectReference: corev1.LocalObjectReference{Name: source.GetName()},
								},
							}
							if mapped {
								cr.Spec.OLSConfig.DeploymentConfig.AlertsAdapter.ConfigMapRef = &corev1.LocalObjectReference{Name: source.GetName()}
							}
						}
					}
					r := createTestReconciler(cr, source)
					wc := r.GetWatcherConfig().(*utils.WatcherConfig)
					if mapped {
						if secret {
							wc.PublishAnnotatedSecrets(map[string][]string{source.GetName(): {utils.OLSAppServerDeploymentName}})
						} else {
							wc.PublishAnnotatedConfigMaps(map[string][]string{source.GetName(): {utils.OLSAppServerDeploymentName, utils.AlertsAdapterDeploymentName}})
						}
					}
					q := &recordingQueue{}
					fire(r, source, "update", q)
					Expect(q.items).To(HaveLen(1), "the original MCP reference must be active")
					Expect(r.Get(ctx, client.ObjectKeyFromObject(cr), cr)).To(Succeed())
					cr.Spec.OLSConfig.MCPKubeServerConfig = nil
					Expect(r.Update(ctx, cr)).To(Succeed())
					// Deliberately leave annotations and published mappings behind.
					for _, action := range []string{"create", "update", "delete", "metadata"} {
						calls = make(map[string]int)
						q := &recordingQueue{}
						fire(r, source, action, q)
						if shared && action == "delete" {
							Expect(q.items).To(HaveLen(1), "remaining consumers must still notice deletion")
						} else {
							Expect(q.items).To(BeEmpty(), "removed MCP refs must no longer enqueue validation")
						}
						if shared && (action == "create" || action == "update") {
							expected := map[string]int{utils.OLSAppServerDeploymentName: 1}
							if !secret && mapped {
								expected[utils.AlertsAdapterDeploymentName] = 1
							}
							Expect(calls).To(Equal(expected))
						} else {
							Expect(calls).To(BeEmpty(), "stale annotations must not restart any deployment")
						}
					}
					Expect(source.GetAnnotations()).To(HaveKey(utils.WatcherAnnotationKey))
				}
			}
		}
	})

	It("unions system and alerts consumers when the baseline is also an explicit user reference", func() {
		cr, source := newCR(false), newSource(false)
		source.SetName(utils.OLSCAConfigMap)
		cr.Spec.OLSConfig.MCPKubeServerConfig.CABundleRefs[0].ConfigMap.Name = source.GetName()
		cr.Spec.OLSConfig.DeploymentConfig.AlertsAdapter.ConfigMapRef = &corev1.LocalObjectReference{Name: source.GetName()}
		r := createTestReconciler(cr, source)
		wc := r.GetWatcherConfig().(*utils.WatcherConfig)
		wc.ConfigMaps.SystemResources = append(wc.ConfigMaps.SystemResources, utils.SystemConfigMap{
			Name: utils.OLSCAConfigMap, Namespace: utils.OLSNamespaceDefault,
			AffectedDeployments: []string{utils.OLSAppServerDeploymentName, utils.PostgresDeploymentName},
		})
		q := &recordingQueue{}
		fire(r, source, "update", q)
		Expect(q.items).To(HaveLen(1))
		Expect(calls).To(Equal(map[string]int{
			utils.OLSAppServerDeploymentName:  1,
			utils.PostgresDeploymentName:      1,
			utils.AlertsAdapterDeploymentName: 1,
		}))
	})

	It("queues the owned baseline service CA before the owned skip and retains system restarts", func() {
		cr := utils.GetDefaultOLSConfigCR() // No explicit MCP config or user refs.
		cr.Spec.OLSConfig.IntrospectionEnabled = nil
		source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: utils.OLSCAConfigMap, Namespace: utils.OLSNamespaceDefault,
			OwnerReferences: []metav1.OwnerReference{{APIVersion: utils.OLSConfigAPIVersion, Kind: utils.OLSConfigKind, Name: utils.OLSConfigName}},
		}, Data: map[string]string{"service-ca.crt": "invalid PEM"}}
		r := createTestReconciler(cr, source)
		wc := r.GetWatcherConfig().(*utils.WatcherConfig)
		wc.ConfigMaps.SystemResources = append(wc.ConfigMaps.SystemResources, utils.SystemConfigMap{
			Name: utils.OLSCAConfigMap, Namespace: utils.OLSNamespaceDefault,
			AffectedDeployments: []string{utils.OLSAppServerDeploymentName, utils.PostgresDeploymentName},
		})
		for _, action := range []string{"create", "update", "delete"} {
			calls = make(map[string]int)
			q := &recordingQueue{}
			fire(r, source, action, q)
			Expect(q.items).To(HaveLen(1))
			Expect(calls[utils.OpenShiftMCPServerDeploymentName]).To(BeZero())
			if action != "delete" {
				Expect(calls[utils.OLSAppServerDeploymentName]).To(Equal(1))
				Expect(calls[utils.PostgresDeploymentName]).To(Equal(1))
			}
		}
		Expect(r.Get(ctx, client.ObjectKeyFromObject(cr), cr)).To(Succeed())
		cr.Spec.OLSConfig.IntrospectionEnabled = utils.BoolPtr(false)
		Expect(r.Update(ctx, cr)).To(Succeed())
		q := &recordingQueue{}
		calls = make(map[string]int)
		fire(r, source, "update", q)
		Expect(q.items).To(BeEmpty())
		Expect(calls[utils.OLSAppServerDeploymentName]).To(Equal(1))
		Expect(calls[utils.PostgresDeploymentName]).To(Equal(1))
	})
})
