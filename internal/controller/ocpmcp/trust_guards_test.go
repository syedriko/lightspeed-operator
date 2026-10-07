package ocpmcp

import (
	"testing"

	. "github.com/onsi/gomega"
	olsv1alpha1 "github.com/openshift/lightspeed-operator/api/v1alpha1"
	"github.com/openshift/lightspeed-operator/internal/controller/utils"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestMCPReservedCASourcesAreNeverOverwrittenOrDeleted(t *testing.T) {
	for _, name := range []string{
		utils.OpenShiftMCPServerConfigCmName,
		trustConfigMapName,
		utils.LegacyOpenShiftMCPServerCAConfigMapName,
		utils.OpenShiftMCPServerCertsSecretName,
	} {
		for _, scope := range []string{"shared", "ossm", "netobserv"} {
			t.Run(name+"/"+scope, func(t *testing.T) {
				g := NewWithT(t)
				f := newMCPTrustFixture(t)
				g.Expect(f.r.Get(f.ctx, client.ObjectKeyFromObject(f.cr), f.cr)).To(Succeed())
				f.cr.Spec.OLSConfig.IntrospectionEnabled = utils.BoolPtr(true)
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
				// A valid selected certificate proves rejection is about the reserved
				// identity, not missing keys or malformed source contents.
				switch source := source.(type) {
				case *corev1.ConfigMap:
					if source.Data == nil {
						source.Data = map[string]string{}
					}
					source.Data["ca.crt"] = f.ca
				case *corev1.Secret:
					source.Data["ca.crt"] = []byte(f.ca)
				}
				source.SetAnnotations(map[string]string{"user": "preserve"})
				if apierrors.IsNotFound(err) {
					g.Expect(f.r.Create(f.ctx, source)).To(Succeed())
				} else {
					g.Expect(f.r.Update(f.ctx, source)).To(Succeed())
				}
				before := source.DeepCopyObject().(client.Object)
				config := &olsv1alpha1.MCPKubeServerConfiguration{}
				switch scope {
				case "shared":
					config.CABundleRefs = []olsv1alpha1.MCPCAReference{ref}
				case "ossm":
					toolsets := []olsv1alpha1.MCPToolsetSelection{{OSSM: &olsv1alpha1.MCPOSSMConfig{URL: "https://kiali.example.test", CABundleRef: &ref}}}
					config.Toolsets = &toolsets
				case "netobserv":
					toolsets := []olsv1alpha1.MCPToolsetSelection{{NetObserv: &olsv1alpha1.MCPNetObservConfig{CABundleRef: &ref}}}
					config.Toolsets = &toolsets
				}
				f.cr.Spec.OLSConfig.MCPKubeServerConfig = config
				g.Expect(f.r.Update(f.ctx, f.cr)).To(Succeed())
				_, err = GenerateTrustConfigMap(f.r, f.ctx, f.cr)
				g.Expect(err).To(MatchError(ContainSubstring("reserved MCP output resources")))
				g.Expect(ReconcileResources(f.r, f.ctx, f.cr)).NotTo(Succeed())
				g.Expect(f.r.Get(f.ctx, key, source)).To(Succeed())
				g.Expect(source).To(Equal(before), "enabled reconciliation must not overwrite or adopt the CA source")
				f.cr.Spec.OLSConfig.IntrospectionEnabled = utils.BoolPtr(false)
				g.Expect(f.r.Update(f.ctx, f.cr)).To(Succeed())
				g.Expect(Remove(f.r, f.ctx)).To(Succeed())
				g.Expect(Remove(f.r, f.ctx)).To(Succeed(), "cleanup must remain idempotent")
				g.Expect(f.r.Get(f.ctx, key, source)).To(Succeed())
				g.Expect(source).To(Equal(before), "disabled cleanup must retain every currently referenced CA source")
			})
		}
	}
}

func TestMCPTrustSnapshotOwnershipGuards(t *testing.T) {
	for _, ownership := range []string{"unowned", "previous CR", "non-controller", "current CR"} {
		t.Run(ownership, func(t *testing.T) {
			g := NewWithT(t)
			f := newMCPTrustFixture(t)
			cm, err := GenerateTrustConfigMap(f.r, f.ctx, f.cr)
			g.Expect(err).NotTo(HaveOccurred())
			switch ownership {
			case "unowned":
				cm.OwnerReferences = nil
			case "previous CR":
				cm.OwnerReferences[0].UID = "previous-cr-uid"
			case "non-controller":
				cm.OwnerReferences[0].Controller = utils.BoolPtr(false)
			}
			cm.Data = map[string]string{"user-data": "must remain untouched"}
			cm.BinaryData = map[string][]byte{"private": []byte("do not overwrite")}
			cm.Annotations = map[string]string{"user": "preserve"}
			g.Expect(f.r.Create(f.ctx, cm)).To(Succeed())
			before := f.snapshot(t)
			err = reconcileTrustConfigMap(f.r, f.ctx, f.cr)
			if ownership == "current CR" {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(f.snapshot(t).Data).To(Equal(map[string]string{"service-ca.crt": f.ca}))
				g.Expect(f.snapshot(t).BinaryData).To(BeEmpty())
				g.Expect(f.snapshot(t).Annotations).To(HaveKeyWithValue("user", "preserve"))
				g.Expect(deleteTrustConfigMap(f.r, f.ctx)).To(Succeed())
				g.Expect(apierrors.IsNotFound(f.r.Get(f.ctx, client.ObjectKeyFromObject(cm), &corev1.ConfigMap{}))).To(BeTrue())
				g.Expect(deleteTrustConfigMap(f.r, f.ctx)).To(Succeed())
			} else {
				g.Expect(err).To(MatchError(ContainSubstring("not owned by OLSConfig")))
				g.Expect(f.snapshot(t)).To(Equal(before))
				g.Expect(deleteTrustConfigMap(f.r, f.ctx)).To(MatchError(ContainSubstring("refusing to delete unowned trust ConfigMap")))
				g.Expect(f.snapshot(t)).To(Equal(before), "ownership checks must not mutate or delete foreign snapshots")
			}
		})
	}
}

func TestMCPTrustSnapshotDeletionRequiresExistingOwnerCR(t *testing.T) {
	g := NewWithT(t)
	f := newMCPTrustFixture(t)
	g.Expect(reconcileTrustConfigMap(f.r, f.ctx, f.cr)).To(Succeed())
	before := f.snapshot(t)
	// Without the current CR, even a matching historical owner reference is
	// insufficient authority to delete a snapshot.
	g.Expect(f.r.Get(f.ctx, client.ObjectKeyFromObject(f.cr), f.cr)).To(Succeed())
	f.cr.Finalizers = nil
	g.Expect(f.r.Update(f.ctx, f.cr)).To(Succeed())
	g.Expect(f.r.Delete(f.ctx, f.cr)).To(Succeed())
	g.Expect(deleteTrustConfigMap(f.r, f.ctx)).To(MatchError(ContainSubstring("cannot determine trust snapshot owner")))
	g.Expect(f.snapshot(t)).To(Equal(before))
}
