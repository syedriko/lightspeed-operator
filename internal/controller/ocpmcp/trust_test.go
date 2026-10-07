package ocpmcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	olsv1alpha1 "github.com/openshift/lightspeed-operator/api/v1alpha1"
	"github.com/openshift/lightspeed-operator/internal/controller/utils"
	monv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// generateMCPTestCA supplies genuine PEM data for both fake-client and envtest
// fixtures. The shared random TLS Secret fixture is not an X.509 certificate.
func generateMCPTestCA() ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	cert := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "MCP test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	privateDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateDER}), nil
}

type mcpTrustFixture struct {
	ctx context.Context
	r   *utils.TestReconciler
	cr  *olsv1alpha1.OLSConfig
	ca  string
	key []byte
}

func newMCPTrustFixture(t *testing.T) *mcpTrustFixture {
	t.Helper()
	g := NewWithT(t)
	certificate, key, err := generateMCPTestCA()
	g.Expect(err).NotTo(HaveOccurred())
	s := runtime.NewScheme()
	g.Expect(clientgoscheme.AddToScheme(s)).To(Succeed())
	g.Expect(olsv1alpha1.AddToScheme(s)).To(Succeed())
	g.Expect(monv1.AddToScheme(s)).To(Succeed())
	cr := utils.GetDefaultOLSConfigCR()
	cr.UID = types.UID("9e0f586d-590b-464c-b1b1-ce17f357484e")
	baseline := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: utils.OLSCAConfigMap, Namespace: utils.OLSNamespaceDefault},
		Data:       map[string]string{utils.AppOpenShiftMCPServerCACertFile: string(certificate)},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(cr, baseline).Build()
	r := utils.NewTestReconciler(c, logr.Discard(), s, utils.OLSNamespaceDefault)
	f := &mcpTrustFixture{ctx: context.Background(), r: r, cr: cr, ca: string(certificate), key: key}
	config, err := GenerateConfigMap(r, cr)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(c.Create(f.ctx, config)).To(Succeed())
	g.Expect(c.Create(f.ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: utils.OpenShiftMCPServerCertsSecretName, Namespace: r.GetNamespace(),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(cr, olsv1alpha1.GroupVersion.WithKind(utils.OLSConfigKind)),
			},
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{"tls.crt": certificate, "tls.key": key},
	})).To(Succeed())
	return f
}

func mcpCMCA(name, key string) olsv1alpha1.MCPCAReference {
	return olsv1alpha1.MCPCAReference{ConfigMap: &olsv1alpha1.MCPCAKeyReference{Name: name, Key: key}}
}

func mcpSecretCA(name, key string) olsv1alpha1.MCPCAReference {
	return olsv1alpha1.MCPCAReference{Secret: &olsv1alpha1.MCPCAKeyReference{Name: name, Key: key}}
}

func (f *mcpTrustFixture) snapshot(t *testing.T) *corev1.ConfigMap {
	t.Helper()
	cm := &corev1.ConfigMap{}
	NewWithT(t).Expect(f.r.Get(f.ctx, client.ObjectKey{Name: trustConfigMapName, Namespace: f.r.GetNamespace()}, cm)).To(Succeed())
	return cm
}

func (f *mcpTrustFixture) deployment(t *testing.T) *appsv1.Deployment {
	t.Helper()
	dep := &appsv1.Deployment{}
	NewWithT(t).Expect(f.r.Get(f.ctx, client.ObjectKey{Name: utils.OpenShiftMCPServerDeploymentName, Namespace: f.r.GetNamespace()}, dep)).To(Succeed())
	return dep
}

func TestValidateMCPTrustEntireBundle(t *testing.T) {
	g := NewWithT(t)
	cert, key, err := generateMCPTestCA()
	g.Expect(err).NotTo(HaveOccurred())
	second, _, err := generateMCPTestCA()
	g.Expect(err).NotTo(HaveOccurred())
	for _, tc := range []struct {
		name  string
		data  string
		valid bool
	}{
		{"single certificate", string(cert), true},
		{"multiple certificates and whitespace", "\n " + string(cert) + "\n\t" + string(second) + " \n", true},
		{"empty", " \n\t", false},
		{"trailing garbage", string(cert) + "garbage", false},
		{"leading garbage", "garbage\n" + string(cert), false},
		{"private key alone", string(key), false},
		{"private key after certificate", string(cert) + string(key), false},
		{"private key before certificate", string(key) + string(cert), false},
		{"invalid second certificate", string(cert) + string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not X.509")})), false},
		{"truncated second certificate", string(cert) + "-----BEGIN CERTIFICATE-----\n", false},
		{"malformed certificate before valid certificate", "-----BEGIN CERTIFICATE-----\n%%%garbage%%%\n" + string(cert), false},
		{"malformed certificate around indented private key before valid certificate", "-----BEGIN CERTIFICATE-----\ngarbage\n" + strings.ReplaceAll(string(key), "-----", "  -----") + string(cert), false},
		{"malformed certificate with footer before valid certificate", "-----BEGIN CERTIFICATE-----\n%%%garbage%%%\n-----END CERTIFICATE-----\n" + string(cert), false},
		{"indented private key before valid certificate", "  " + strings.ReplaceAll(string(key), "\n", "\n  ") + string(cert), false},
		{"garbage between certificates", string(cert) + "garbage\n" + string(second), false},
		{"PEM headers", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: map[string]string{"Comment": "not allowed"}, Bytes: []byte("bad")})), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCABundle([]byte(tc.data))
			if tc.valid {
				NewWithT(t).Expect(err).NotTo(HaveOccurred())
			} else {
				NewWithT(t).Expect(err).To(HaveOccurred())
			}
		})
	}
}

func TestMCPTrustSnapshotsOnlySelectedPublicData(t *testing.T) {
	for _, kind := range []string{"ConfigMap Data", "ConfigMap BinaryData", "Secret"} {
		t.Run(kind, func(t *testing.T) {
			g := NewWithT(t)
			f := newMCPTrustFixture(t)
			second, _, err := generateMCPTestCA()
			g.Expect(err).NotTo(HaveOccurred())
			bundle := f.ca + string(second)
			// Accepted whitespace and CRLF input must be reconstructed as public PEM,
			// not copied verbatim into the mounted snapshot.
			input := " \t\n" + strings.ReplaceAll(bundle, "\n", "\r\n") + " \t\n"
			meta := metav1.ObjectMeta{Name: "user-ca", Namespace: f.r.GetNamespace(), Labels: map[string]string{"user": "owned"}}
			var source client.Object
			ref := mcpCMCA(meta.Name, "selected.crt")
			switch kind {
			case "ConfigMap Data":
				source = &corev1.ConfigMap{ObjectMeta: meta, Data: map[string]string{"selected.crt": input, "private.key": string(f.key), "unselected": "invalid"}}
			case "ConfigMap BinaryData":
				source = &corev1.ConfigMap{ObjectMeta: meta, BinaryData: map[string][]byte{"selected.crt": []byte(input), "private.key": f.key}}
			case "Secret":
				ref = mcpSecretCA(meta.Name, "selected.crt")
				source = &corev1.Secret{ObjectMeta: meta, Data: map[string][]byte{"selected.crt": []byte(input), "tls.key": f.key, "token": []byte("private-token")}}
			}
			g.Expect(f.r.Create(f.ctx, source)).To(Succeed())
			before := source.DeepCopyObject()
			f.cr.Spec.OLSConfig.MCPKubeServerConfig = &olsv1alpha1.MCPKubeServerConfiguration{CABundleRefs: []olsv1alpha1.MCPCAReference{ref}}
			g.Expect(reconcileTrustConfigMap(f.r, f.ctx, f.cr)).To(Succeed())
			cm := f.snapshot(t)
			g.Expect(cm.Data).To(Equal(map[string]string{"service-ca.crt": f.ca, "shared-000.crt": bundle}))
			g.Expect(cm.BinaryData).To(BeEmpty())
			g.Expect(cm.OwnerReferences).To(HaveLen(1))
			g.Expect(cm.OwnerReferences[0].UID).To(Equal(f.cr.UID))
			g.Expect(cm.OwnerReferences[0].Controller).To(Equal(utils.BoolPtr(true)))
			g.Expect(cm.Annotations[trustHashAnnotation]).To(HaveLen(64))
			g.Expect(f.r.Get(f.ctx, client.ObjectKeyFromObject(source), source)).To(Succeed())
			g.Expect(source).To(Equal(before), "user sources must not be adopted, annotated, or altered")
			g.Expect(reconcileTrustConfigMap(f.r, f.ctx, f.cr)).To(Succeed())
			g.Expect(f.snapshot(t).ResourceVersion).To(Equal(cm.ResourceVersion), "unchanged trust must not be updated")
		})
	}
}

func TestMCPTrustMissingOrInvalidSources(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ref      olsv1alpha1.MCPCAReference
		baseline bool
	}{
		{"missing ConfigMap", mcpCMCA("missing", "ca.crt"), false},
		{"missing Secret", mcpSecretCA("missing", "ca.crt"), false},
		{"missing ConfigMap key", mcpCMCA(utils.OLSCAConfigMap, "missing"), false},
		{"missing Secret key", mcpSecretCA(utils.OpenShiftMCPServerCertsSecretName, "missing"), false},
		{"private key selected", mcpSecretCA(utils.OpenShiftMCPServerCertsSecretName, "tls.key"), false},
		{"no source", olsv1alpha1.MCPCAReference{}, false},
		{"both sources", olsv1alpha1.MCPCAReference{ConfigMap: &olsv1alpha1.MCPCAKeyReference{Name: utils.OLSCAConfigMap, Key: "service-ca.crt"}, Secret: &olsv1alpha1.MCPCAKeyReference{Name: utils.OpenShiftMCPServerCertsSecretName, Key: "tls.crt"}}, false},
		{"missing baseline", olsv1alpha1.MCPCAReference{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			f := newMCPTrustFixture(t)
			if tc.baseline {
				g.Expect(f.r.Delete(f.ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: utils.OLSCAConfigMap, Namespace: f.r.GetNamespace()}})).To(Succeed())
			} else {
				f.cr.Spec.OLSConfig.MCPKubeServerConfig = &olsv1alpha1.MCPKubeServerConfiguration{CABundleRefs: []olsv1alpha1.MCPCAReference{tc.ref}}
			}
			g.Expect(ReconcileResources(f.r, f.ctx, f.cr)).NotTo(Succeed())
			g.Expect(apierrors.IsNotFound(f.r.Get(f.ctx, client.ObjectKey{Name: trustConfigMapName, Namespace: f.r.GetNamespace()}, &corev1.ConfigMap{}))).To(BeTrue())
		})
	}
}

func TestMCPInvalidSourceUpdatePreservesSnapshotAndDeployment(t *testing.T) {
	for _, tc := range []struct{ kind, attack string }{
		{"ConfigMap", "private key appended"},
		{"BinaryData", "private key appended"},
		{"Secret", "private key appended"},
		{"ConfigMap", "malformed certificate and garbage"},
		{"BinaryData", "malformed certificate and garbage"},
		{"Secret", "malformed certificate and garbage"},
		{"ConfigMap", "malformed certificate and indented private key"},
		{"BinaryData", "malformed certificate and indented private key"},
		{"Secret", "malformed certificate and indented private key"},
	} {
		t.Run(tc.kind+"/"+tc.attack, func(t *testing.T) {
			g := NewWithT(t)
			f := newMCPTrustFixture(t)
			meta := metav1.ObjectMeta{Name: "user-ca", Namespace: f.r.GetNamespace()}
			var source client.Object
			ref := mcpCMCA(meta.Name, "ca.crt")
			switch tc.kind {
			case "ConfigMap":
				source = &corev1.ConfigMap{ObjectMeta: meta, Data: map[string]string{"ca.crt": f.ca}}
			case "BinaryData":
				source = &corev1.ConfigMap{ObjectMeta: meta, BinaryData: map[string][]byte{"ca.crt": []byte(f.ca)}}
			case "Secret":
				source = &corev1.Secret{ObjectMeta: meta, Data: map[string][]byte{"ca.crt": []byte(f.ca)}}
				ref = mcpSecretCA(meta.Name, "ca.crt")
			}
			g.Expect(f.r.Create(f.ctx, source)).To(Succeed())
			f.cr.Spec.OLSConfig.MCPKubeServerConfig = &olsv1alpha1.MCPKubeServerConfiguration{CABundleRefs: []olsv1alpha1.MCPCAReference{ref}}
			g.Expect(ReconcileResources(f.r, f.ctx, f.cr)).To(Succeed())
			g.Expect(reconcileDeployment(f.r, f.ctx, f.cr)).To(Succeed())
			beforeTrust, beforeDeployment := f.snapshot(t), f.deployment(t)
			beforeConfig := &corev1.ConfigMap{}
			g.Expect(f.r.Get(f.ctx, client.ObjectKey{Name: utils.OpenShiftMCPServerConfigCmName, Namespace: f.r.GetNamespace()}, beforeConfig)).To(Succeed())
			invalid := f.ca + string(f.key)
			switch tc.attack {
			case "malformed certificate and garbage":
				invalid = "-----BEGIN CERTIFICATE-----\n%%%garbage%%%\n" + f.ca
			case "malformed certificate and indented private key":
				invalid = "-----BEGIN CERTIFICATE-----\n" + strings.ReplaceAll(string(f.key), "-----", "  -----") + f.ca
			}
			switch source := source.(type) {
			case *corev1.ConfigMap:
				if source.Data != nil {
					source.Data["ca.crt"] = invalid
				} else {
					source.BinaryData["ca.crt"] = []byte(invalid)
				}
			case *corev1.Secret:
				source.Data["ca.crt"] = []byte(invalid)
			}
			g.Expect(f.r.Update(f.ctx, source)).To(Succeed())
			// Also change runtime configuration to prove Phase 1 validates trust first.
			empty := []olsv1alpha1.MCPToolsetSelection{}
			f.cr.Spec.OLSConfig.MCPKubeServerConfig.Toolsets = &empty
			g.Expect(ReconcileResources(f.r, f.ctx, f.cr)).NotTo(Succeed())
			g.Expect(reconcileDeployment(f.r, f.ctx, f.cr)).NotTo(Succeed())
			g.Expect(f.snapshot(t)).To(Equal(beforeTrust))
			g.Expect(f.deployment(t)).To(Equal(beforeDeployment))
			afterConfig := &corev1.ConfigMap{}
			g.Expect(f.r.Get(f.ctx, client.ObjectKeyFromObject(beforeConfig), afterConfig)).To(Succeed())
			g.Expect(afterConfig).To(Equal(beforeConfig))
		})
	}
}

func TestMCPTrustChangesRollDeployment(t *testing.T) {
	g := NewWithT(t)
	f := newMCPTrustFixture(t)
	source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "user-ca", Namespace: f.r.GetNamespace()}, Data: map[string]string{"first.crt": f.ca, "second.crt": f.ca}}
	g.Expect(f.r.Create(f.ctx, source)).To(Succeed())
	alias := source.DeepCopy()
	alias.Name, alias.ResourceVersion = "other-ca", ""
	g.Expect(f.r.Create(f.ctx, alias)).To(Succeed())
	config := &olsv1alpha1.MCPKubeServerConfiguration{CABundleRefs: []olsv1alpha1.MCPCAReference{mcpCMCA(source.Name, "first.crt")}}
	f.cr.Spec.OLSConfig.MCPKubeServerConfig = config
	g.Expect(reconcileTrustConfigMap(f.r, f.ctx, f.cr)).To(Succeed())
	g.Expect(reconcileDeployment(f.r, f.ctx, f.cr)).To(Succeed())
	for _, change := range []struct {
		name  string
		apply func(*WithT)
	}{
		{"key identity with identical contents", func(_ *WithT) { config.CABundleRefs[0].ConfigMap.Key = "second.crt" }},
		{"source identity with identical contents", func(_ *WithT) { config.CABundleRefs[0].ConfigMap.Name = alias.Name }},
		{"certificate rotation", func(g *WithT) {
			certificate, _, err := generateMCPTestCA()
			g.Expect(err).NotTo(HaveOccurred())
			alias.Data["second.crt"] = string(certificate)
			g.Expect(f.r.Update(f.ctx, alias)).To(Succeed())
		}},
		{"remove shared entries with empty selection", func(_ *WithT) {
			empty := []olsv1alpha1.MCPToolsetSelection{}
			config.Toolsets, config.CABundleRefs = &empty, nil
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			g := NewWithT(t)
			before := f.deployment(t)
			change.apply(g)
			_, err := GenerateDeployment(f.r, f.ctx, f.cr)
			g.Expect(err).To(HaveOccurred(), "changed sources require persisted snapshot reconciliation")
			g.Expect(reconcileTrustConfigMap(f.r, f.ctx, f.cr)).To(Succeed())
			g.Expect(reconcileDeployment(f.r, f.ctx, f.cr)).To(Succeed())
			after := f.deployment(t)
			g.Expect(after.Spec.Template.Annotations[trustHashAnnotation]).NotTo(Equal(before.Spec.Template.Annotations[trustHashAnnotation]))
			g.Expect(after.Spec.Template.Annotations[utils.ForceReloadAnnotationKey]).NotTo(BeEmpty())
			g.Expect(after.Spec.Template.Annotations[trustHashAnnotation]).To(Equal(f.snapshot(t).Annotations[trustHashAnnotation]))
		})
	}
	g.Expect(f.snapshot(t).Data).To(Equal(map[string]string{"service-ca.crt": f.ca}))
	g.Expect(f.deployment(t).Spec.Template.Spec.Volumes).To(ContainElement(HaveField("Name", "mcp-shared-ca")))
	g.Expect(Remove(f.r, f.ctx)).To(Succeed())
	g.Expect(apierrors.IsNotFound(f.r.Get(f.ctx, client.ObjectKey{Name: trustConfigMapName, Namespace: f.r.GetNamespace()}, &corev1.ConfigMap{}))).To(BeTrue())
	for _, cm := range []*corev1.ConfigMap{source, alias, {ObjectMeta: metav1.ObjectMeta{Name: utils.OLSCAConfigMap, Namespace: f.r.GetNamespace()}}} {
		g.Expect(f.r.Get(f.ctx, client.ObjectKeyFromObject(cm), &corev1.ConfigMap{})).To(Succeed(), "Remove must retain user and baseline sources")
	}
	g.Expect(Remove(f.r, f.ctx)).To(Succeed(), "cleanup must be idempotent")
}

func TestMCPBaselineCAValidationAndRotation(t *testing.T) {
	g := NewWithT(t)
	f := newMCPTrustFixture(t)
	g.Expect(reconcileTrustConfigMap(f.r, f.ctx, f.cr)).To(Succeed())
	g.Expect(reconcileDeployment(f.r, f.ctx, f.cr)).To(Succeed())
	beforeTrust, beforeDeployment := f.snapshot(t), f.deployment(t)
	baseline := &corev1.ConfigMap{}
	g.Expect(f.r.Get(f.ctx, client.ObjectKey{Name: utils.OLSCAConfigMap, Namespace: f.r.GetNamespace()}, baseline)).To(Succeed())
	for _, data := range []map[string]string{
		{},
		{utils.AppOpenShiftMCPServerCACertFile: ""},
		{utils.AppOpenShiftMCPServerCACertFile: f.ca + "trailing garbage"},
	} {
		baseline.Data = data
		g.Expect(f.r.Update(f.ctx, baseline)).To(Succeed())
		g.Expect(reconcileTrustConfigMap(f.r, f.ctx, f.cr)).NotTo(Succeed())
		g.Expect(reconcileDeployment(f.r, f.ctx, f.cr)).NotTo(Succeed())
		g.Expect(f.snapshot(t)).To(Equal(beforeTrust))
		g.Expect(f.deployment(t)).To(Equal(beforeDeployment))
	}
	rotated, _, err := generateMCPTestCA()
	g.Expect(err).NotTo(HaveOccurred())
	baseline.Data = map[string]string{utils.AppOpenShiftMCPServerCACertFile: string(rotated)}
	g.Expect(f.r.Update(f.ctx, baseline)).To(Succeed())
	g.Expect(reconcileTrustConfigMap(f.r, f.ctx, f.cr)).To(Succeed())
	g.Expect(reconcileDeployment(f.r, f.ctx, f.cr)).To(Succeed())
	g.Expect(f.snapshot(t).Data).To(Equal(map[string]string{"service-ca.crt": string(rotated)}))
	g.Expect(f.deployment(t).Spec.Template.Annotations[trustHashAnnotation]).NotTo(Equal(beforeDeployment.Spec.Template.Annotations[trustHashAnnotation]))
}

func TestMCPTrustSourcesAreNamespaceLocal(t *testing.T) {
	g := NewWithT(t)
	f := newMCPTrustFixture(t)
	g.Expect(f.r.Create(f.ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "user-ca", Namespace: "other-namespace"},
		Data:       map[string]string{"ca.crt": f.ca},
	})).To(Succeed())
	f.cr.Spec.OLSConfig.MCPKubeServerConfig = &olsv1alpha1.MCPKubeServerConfiguration{CABundleRefs: []olsv1alpha1.MCPCAReference{mcpCMCA("user-ca", "ca.crt")}}
	_, err := GenerateTrustConfigMap(f.r, f.ctx, f.cr)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring(f.r.GetNamespace() + "/user-ca"))
}

func TestMCPDeploymentRequiresMatchingTrustSnapshot(t *testing.T) {
	for _, mismatch := range []string{"missing snapshot", "data", "hash"} {
		t.Run(mismatch, func(t *testing.T) {
			g := NewWithT(t)
			f := newMCPTrustFixture(t)
			if mismatch != "missing snapshot" {
				g.Expect(reconcileTrustConfigMap(f.r, f.ctx, f.cr)).To(Succeed())
				cm := f.snapshot(t)
				if mismatch == "data" {
					cm.Data["service-ca.crt"] += "\n"
				} else {
					cm.Annotations[trustHashAnnotation] = "wrong"
				}
				g.Expect(f.r.Update(f.ctx, cm)).To(Succeed())
			}
			_, err := GenerateDeployment(f.r, f.ctx, f.cr)
			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(ContainSubstring(utils.ErrReconcileOpenShiftMCPServerTrust))
			g.Expect(reconcileTrustConfigMap(f.r, f.ctx, f.cr)).To(Succeed())
			_, err = GenerateDeployment(f.r, f.ctx, f.cr)
			g.Expect(err).NotTo(HaveOccurred())
		})
	}
}

func TestMCPTrustPodProjectionAndSystemRoots(t *testing.T) {
	g := NewWithT(t)
	f := newMCPTrustFixture(t)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "user-ca", Namespace: f.r.GetNamespace()}, Data: map[string][]byte{"public.crt": []byte(f.ca), "private.key": f.key}}
	g.Expect(f.r.Create(f.ctx, secret)).To(Succeed())
	ref := mcpSecretCA(secret.Name, "public.crt")
	toolsets := []olsv1alpha1.MCPToolsetSelection{
		{OSSM: &olsv1alpha1.MCPOSSMConfig{URL: "https://kiali.example.test", CABundleRef: &ref}},
		{NetObserv: &olsv1alpha1.MCPNetObservConfig{}},
	}
	f.cr.Spec.OLSConfig.MCPKubeServerConfig = &olsv1alpha1.MCPKubeServerConfiguration{Toolsets: &toolsets, CABundleRefs: []olsv1alpha1.MCPCAReference{ref}}
	g.Expect(reconcileTrustConfigMap(f.r, f.ctx, f.cr)).To(Succeed())
	g.Expect(f.snapshot(t).Data).To(Equal(map[string]string{"service-ca.crt": f.ca, "shared-000.crt": f.ca, "ossm.crt": f.ca, "netobserv.crt": f.ca}))
	dep, err := GenerateDeployment(f.r, f.ctx, f.cr)
	g.Expect(err).NotTo(HaveOccurred())
	pod := dep.Spec.Template.Spec
	container := pod.Containers[0]
	g.Expect(container.Env).To(ContainElement(corev1.EnvVar{Name: "SSL_CERT_DIR", Value: "/etc/ssl/certs:/etc/pki/tls/certs:/etc/mcp-server/ca"}))
	for _, env := range container.Env {
		g.Expect(env.Name).NotTo(Equal("SSL_CERT_FILE"), "system default root files must remain available")
	}
	expectedItems := map[string][]corev1.KeyToPath{
		"mcp-shared-ca":    {{Key: "service-ca.crt", Path: "service-ca.crt"}, {Key: "shared-000.crt", Path: "shared-000.crt"}},
		"mcp-ossm-ca":      {{Key: "ossm.crt", Path: "ca.crt"}},
		"mcp-netobserv-ca": {{Key: "netobserv.crt", Path: "ca.crt"}},
	}
	for _, volume := range pod.Volumes {
		if items, ok := expectedItems[volume.Name]; ok {
			g.Expect(volume.ConfigMap).NotTo(BeNil())
			g.Expect(volume.ConfigMap.Name).To(Equal(trustConfigMapName))
			g.Expect(volume.ConfigMap.Items).To(Equal(items))
			g.Expect(volume.ConfigMap.DefaultMode).NotTo(BeNil())
			g.Expect(*volume.ConfigMap.DefaultMode).To(Equal(int32(0444)))
			delete(expectedItems, volume.Name)
		}
		if volume.Secret != nil {
			g.Expect(volume.Secret.SecretName).To(Equal(utils.OpenShiftMCPServerCertsSecretName), "never project user Secret fields")
		}
		if volume.ConfigMap != nil {
			g.Expect(volume.ConfigMap.Name).To(BeElementOf(trustConfigMapName, utils.OpenShiftMCPServerConfigCmName), "never project live user CA objects")
		}
	}
	g.Expect(expectedItems).To(BeEmpty())
	for name, directory := range map[string]string{"mcp-shared-ca": sharedTrustDir, "mcp-ossm-ca": path.Dir(ossmCAPath), "mcp-netobserv-ca": path.Dir(netobservCAPath)} {
		g.Expect(container.VolumeMounts).To(ContainElement(corev1.VolumeMount{Name: name, MountPath: directory, ReadOnly: true}))
		g.Expect(strings.HasPrefix(directory, "/etc/ssl")).To(BeFalse(), "supplemental roots must not hide system roots")
	}
	// TOML backend paths and secure verification must agree with mounted files.
	text, err := GenerateConfigTOML(f.cr.Spec.OLSConfig.MCPKubeServerConfig)
	g.Expect(err).NotTo(HaveOccurred())
	var decoded struct {
		ToolsetConfigs map[string]mcpBackendConfig `toml:"toolset_configs"`
	}
	_, err = toml.Decode(text, &decoded)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(decoded.ToolsetConfigs["kiali"].CertificateAuthority).To(Equal(ossmCAPath))
	g.Expect(decoded.ToolsetConfigs["netobserv"].CertificateAuthority).To(Equal(netobservCAPath))
	g.Expect(decoded.ToolsetConfigs["kiali"].Insecure).To(BeFalse())
	g.Expect(decoded.ToolsetConfigs["netobserv"].Insecure).To(BeFalse())
	// Deselect backends and shared roots: stale snapshot entries and mounts must
	// disappear, while the mandatory service CA remains even with no toolsets.
	empty := []olsv1alpha1.MCPToolsetSelection{}
	f.cr.Spec.OLSConfig.MCPKubeServerConfig = &olsv1alpha1.MCPKubeServerConfiguration{Toolsets: &empty}
	g.Expect(reconcileTrustConfigMap(f.r, f.ctx, f.cr)).To(Succeed())
	g.Expect(f.snapshot(t).Data).To(Equal(map[string]string{"service-ca.crt": f.ca}))
	dep, err = GenerateDeployment(f.r, f.ctx, f.cr)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(dep.Spec.Template.Spec.Volumes).NotTo(ContainElement(HaveField("Name", "mcp-ossm-ca")))
	g.Expect(dep.Spec.Template.Spec.Volumes).NotTo(ContainElement(HaveField("Name", "mcp-netobserv-ca")))
	g.Expect(Remove(f.r, f.ctx)).To(Succeed())
	g.Expect(f.r.Get(f.ctx, client.ObjectKeyFromObject(secret), &corev1.Secret{})).To(Succeed(), "Remove must not delete user CA Secrets")
}
