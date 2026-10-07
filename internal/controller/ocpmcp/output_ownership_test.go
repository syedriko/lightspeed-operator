package ocpmcp

import (
	"testing"

	. "github.com/onsi/gomega"
	olsv1alpha1 "github.com/openshift/lightspeed-operator/api/v1alpha1"
	"github.com/openshift/lightspeed-operator/internal/controller/utils"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestMCPReservedCASourcesSurviveReferenceRemoval(t *testing.T) {
	for _, name := range []string{
		utils.OpenShiftMCPServerConfigCmName,
		utils.LegacyOpenShiftMCPServerCAConfigMapName,
		utils.OpenShiftMCPServerCertsSecretName,
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			f := newMCPTrustFixture(t)
			var source client.Object = &corev1.ConfigMap{}
			ref := mcpCMCA(name, "ca.crt")
			if name == utils.OpenShiftMCPServerCertsSecretName {
				source = &corev1.Secret{}
				ref = mcpSecretCA(name, "ca.crt")
			}
			key := client.ObjectKey{Name: name, Namespace: f.r.GetNamespace()}
			err := f.r.Get(f.ctx, key, source)
			if apierrors.IsNotFound(err) {
				source.SetName(name)
				source.SetNamespace(key.Namespace)
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			// These reserved identities belong to the user, not to this CR.
			source.SetOwnerReferences(nil)
			source.SetLabels(map[string]string{"user": "preserve"})
			source.SetAnnotations(map[string]string{"user": "preserve"})
			switch source := source.(type) {
			case *corev1.ConfigMap:
				source.Data = map[string]string{"ca.crt": f.ca}
				source.BinaryData = map[string][]byte{"private": f.key}
			case *corev1.Secret:
				source.Data = map[string][]byte{"ca.crt": []byte(f.ca), "private": f.key}
			}
			if apierrors.IsNotFound(err) {
				g.Expect(f.r.Create(f.ctx, source)).To(Succeed())
			} else {
				g.Expect(f.r.Update(f.ctx, source)).To(Succeed())
			}
			before := source.DeepCopyObject().(client.Object)
			g.Expect(f.r.Get(f.ctx, client.ObjectKeyFromObject(f.cr), f.cr)).To(Succeed())
			f.cr.Spec.OLSConfig.IntrospectionEnabled = utils.BoolPtr(false)
			f.cr.Spec.OLSConfig.MCPKubeServerConfig = &olsv1alpha1.MCPKubeServerConfiguration{
				CABundleRefs: []olsv1alpha1.MCPCAReference{ref},
			}
			g.Expect(f.r.Update(f.ctx, f.cr)).To(Succeed())
			g.Expect(Remove(f.r, f.ctx)).To(Succeed())
			g.Expect(f.r.Get(f.ctx, key, source)).To(Succeed())
			g.Expect(source).To(Equal(before))

			f.cr.Spec.OLSConfig.MCPKubeServerConfig = nil
			g.Expect(f.r.Update(f.ctx, f.cr)).To(Succeed())
			for range 2 {
				g.Expect(Remove(f.r, f.ctx)).To(MatchError(ContainSubstring("refusing to modify or delete unowned MCP output " + name)))
				g.Expect(f.r.Get(f.ctx, key, source)).To(Succeed())
				g.Expect(source).To(Equal(before), "removing the reference must not authorize cleanup of user data")
			}
			if name == utils.OpenShiftMCPServerConfigCmName {
				f.cr.Spec.OLSConfig.IntrospectionEnabled = utils.BoolPtr(true)
				g.Expect(f.r.Update(f.ctx, f.cr)).To(Succeed())
				g.Expect(reconcileConfigMap(f.r, f.ctx, f.cr)).To(MatchError(ContainSubstring("refusing to overwrite unowned ConfigMap " + name)))
				g.Expect(f.r.Get(f.ctx, key, source)).To(Succeed())
				g.Expect(source).To(Equal(before), "enabled reconciliation must not overwrite or adopt an unreferenced user source")
			}
		})
	}
}

func TestMCPRuntimeConfigMapRequiresCurrentControllerOwner(t *testing.T) {
	for _, ownership := range []string{"unowned", "previous CR", "non-controller"} {
		t.Run(ownership, func(t *testing.T) {
			g := NewWithT(t)
			f := newMCPTrustFixture(t)
			cm := &corev1.ConfigMap{}
			key := client.ObjectKey{Name: utils.OpenShiftMCPServerConfigCmName, Namespace: f.r.GetNamespace()}
			g.Expect(f.r.Get(f.ctx, key, cm)).To(Succeed())
			switch ownership {
			case "unowned":
				cm.OwnerReferences = nil
			case "previous CR":
				cm.OwnerReferences[0].UID = "previous-cr-uid"
			case "non-controller":
				cm.OwnerReferences[0].Controller = utils.BoolPtr(false)
			}
			// Even an unchanged desired payload must not bypass ownership checks.
			g.Expect(f.r.Update(f.ctx, cm)).To(Succeed())
			before := cm.DeepCopy()
			g.Expect(reconcileConfigMap(f.r, f.ctx, f.cr)).To(MatchError(ContainSubstring("refusing to overwrite unowned ConfigMap")))
			g.Expect(f.r.Get(f.ctx, key, cm)).To(Succeed())
			g.Expect(cm).To(Equal(before))
			g.Expect(deleteConfigMap(f.r, f.ctx)).To(MatchError(ContainSubstring("refusing to modify or delete unowned MCP output")))
			g.Expect(f.r.Get(f.ctx, key, cm)).To(Succeed())
			g.Expect(cm).To(Equal(before))
		})
	}
}

func TestMCPLegacyConfigMapCleanupRequiresCurrentControllerOwner(t *testing.T) {
	for _, ownership := range []string{"unowned", "previous CR", "non-controller", "current CR"} {
		t.Run(ownership, func(t *testing.T) {
			g := NewWithT(t)
			f := newMCPTrustFixture(t)
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: utils.LegacyOpenShiftMCPServerCAConfigMapName, Namespace: f.r.GetNamespace(),
					OwnerReferences: []metav1.OwnerReference{
						*metav1.NewControllerRef(f.cr, olsv1alpha1.GroupVersion.WithKind(utils.OLSConfigKind)),
					},
				},
				Data: map[string]string{"service-ca.crt": "preserve"},
			}
			switch ownership {
			case "unowned":
				cm.OwnerReferences = nil
			case "previous CR":
				cm.OwnerReferences[0].UID = "previous-cr-uid"
			case "non-controller":
				cm.OwnerReferences[0].Controller = utils.BoolPtr(false)
			}
			g.Expect(f.r.Create(f.ctx, cm)).To(Succeed())
			before := cm.DeepCopy()
			for range 2 {
				err := removeLegacyCAConfigMap(f.r, f.ctx, f.cr)
				if ownership == "current CR" {
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(apierrors.IsNotFound(f.r.Get(f.ctx, client.ObjectKeyFromObject(cm), &corev1.ConfigMap{}))).To(BeTrue())
				} else {
					g.Expect(err).To(MatchError(ContainSubstring("refusing to modify or delete unowned MCP output")))
					g.Expect(f.r.Get(f.ctx, client.ObjectKeyFromObject(cm), cm)).To(Succeed())
					g.Expect(cm).To(Equal(before))
				}
			}
		})
	}
}

func TestMCPTLSCleanupVerifiesOriginatingServiceOwnership(t *testing.T) {
	for _, ownership := range []string{"managed service", "wrong service UID", "unowned service", "previous CR service", "missing service"} {
		t.Run(ownership, func(t *testing.T) {
			g := NewWithT(t)
			f := newMCPTrustFixture(t)
			service, err := GenerateService(f.r, f.cr)
			g.Expect(err).NotTo(HaveOccurred())
			// Fake clients do not allocate UIDs; never test owner matching with empty UIDs.
			service.UID = "current-mcp-service-uid"
			g.Expect(service.UID).NotTo(BeEmpty())
			switch ownership {
			case "unowned service":
				service.OwnerReferences = nil
			case "previous CR service":
				service.OwnerReferences[0].UID = "previous-cr-uid"
			}
			if ownership != "missing service" {
				g.Expect(f.r.Create(f.ctx, service)).To(Succeed())
			}
			secret := &corev1.Secret{}
			key := client.ObjectKey{Name: utils.OpenShiftMCPServerCertsSecretName, Namespace: f.r.GetNamespace()}
			g.Expect(f.r.Get(f.ctx, key, secret)).To(Succeed())
			// service-ca uses a Service owner, not a direct OLSConfig owner.
			secret.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: "v1", Kind: "Service", Name: service.Name, UID: service.UID,
			}}
			if ownership == "wrong service UID" {
				secret.OwnerReferences[0].UID = "previous-service-uid"
			}
			g.Expect(f.r.Update(f.ctx, secret)).To(Succeed())
			before := secret.DeepCopy()
			err = Remove(f.r, f.ctx)
			if ownership == "managed service" {
				g.Expect(err).NotTo(HaveOccurred(), "TLS ownership must be checked before deleting its originating Service")
				g.Expect(apierrors.IsNotFound(f.r.Get(f.ctx, key, &corev1.Secret{}))).To(BeTrue())
				g.Expect(apierrors.IsNotFound(f.r.Get(f.ctx, client.ObjectKeyFromObject(service), &corev1.Service{}))).To(BeTrue())
				g.Expect(Remove(f.r, f.ctx)).To(Succeed())
			} else {
				g.Expect(err).To(MatchError(ContainSubstring("refusing to modify or delete unowned MCP output")))
				g.Expect(f.r.Get(f.ctx, key, secret)).To(Succeed())
				g.Expect(secret).To(Equal(before), "an unrelated Service cannot authorize serving Secret deletion")
				if ownership != "missing service" {
					g.Expect(f.r.Get(f.ctx, client.ObjectKeyFromObject(service), &corev1.Service{})).To(Succeed(), "cleanup must stop before deleting the Service")
				}
			}
		})
	}
}
