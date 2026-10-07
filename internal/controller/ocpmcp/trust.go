package ocpmcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"path"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	olsv1alpha1 "github.com/openshift/lightspeed-operator/api/v1alpha1"
	"github.com/openshift/lightspeed-operator/internal/controller/reconciler"
	"github.com/openshift/lightspeed-operator/internal/controller/utils"
)

const (
	trustConfigMapName  = "openshift-mcp-server-trust"
	trustHashAnnotation = "ols.openshift.io/mcp-trust-hash"
	sharedTrustDir      = "/etc/mcp-server/ca"
	// Preserve Go's Linux default certificate directories and default certificate
	// files (SSL_CERT_FILE is intentionally not overridden). The supplemental
	// directory supplies service/custom roots even when REST CAData is populated.
	sharedCertDirectories = "/etc/ssl/certs:/etc/pki/tls/certs:" + sharedTrustDir
)

// canonicalCABundle strictly consumes each certificate block. pem.Decode alone
// is insufficient: it skips malformed blocks and intervening arbitrary bytes.
// Only reconstructed, validated certificate PEM is published in the snapshot.
func canonicalCABundle(bundle []byte) ([]byte, error) {
	const begin = "-----BEGIN CERTIFICATE-----"
	const end = "-----END CERTIFICATE-----"
	rest := bytes.TrimSpace(bundle)
	var canonical []byte
	for len(rest) > 0 {
		if !bytes.HasPrefix(rest, []byte(begin)) {
			return nil, fmt.Errorf("bundle must contain only PEM certificates")
		}
		body := rest[len(begin):]
		if len(body) == 0 || (body[0] != '\n' && body[0] != '\r') {
			return nil, fmt.Errorf("invalid PEM certificate header")
		}
		boundary := bytes.Index(body, []byte(end))
		if boundary < 0 {
			return nil, fmt.Errorf("missing PEM certificate footer")
		}
		der, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(body[:boundary])))
		if err != nil {
			return nil, fmt.Errorf("invalid PEM certificate data: %w", err)
		}
		if _, err := x509.ParseCertificate(der); err != nil {
			return nil, fmt.Errorf("invalid X.509 certificate: %w", err)
		}
		canonical = append(canonical, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
		rest = bytes.TrimSpace(body[boundary+len(end):])
	}
	if len(canonical) == 0 {
		return nil, fmt.Errorf("certificate bundle is empty")
	}
	return canonical, nil
}

func validateCABundle(bundle []byte) error {
	_, err := canonicalCABundle(bundle)
	return err
}

func readCABundle(r reconciler.Reconciler, ctx context.Context, ref olsv1alpha1.MCPCAReference) (string, error) {
	if (ref.ConfigMap == nil) == (ref.Secret == nil) {
		return "", fmt.Errorf("exactly one ConfigMap or Secret CA source is required")
	}
	var data []byte
	var found bool
	var kind, name, key string
	if ref.ConfigMap != nil {
		kind, name, key = "ConfigMap", ref.ConfigMap.Name, ref.ConfigMap.Key
		cm := &corev1.ConfigMap{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.GetNamespace(), Name: name}, cm); err != nil {
			return "", fmt.Errorf("get MCP CA ConfigMap %s/%s: %w", r.GetNamespace(), name, err)
		}
		if text, ok := cm.Data[key]; ok {
			data, found = []byte(text), true
		} else {
			data, found = cm.BinaryData[key]
		}
	} else {
		kind, name, key = "Secret", ref.Secret.Name, ref.Secret.Key
		secret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.GetNamespace(), Name: name}, secret); err != nil {
			return "", fmt.Errorf("get MCP CA Secret %s/%s: %w", r.GetNamespace(), name, err)
		}
		data, found = secret.Data[key]
	}
	if name == "" || key == "" || !found {
		return "", fmt.Errorf("MCP CA %s %s/%s is missing required key %q", kind, r.GetNamespace(), name, key)
	}
	canonical, err := canonicalCABundle(data)
	if err != nil {
		return "", fmt.Errorf("MCP CA %s %s/%s key %q: %w", kind, r.GetNamespace(), name, key, err)
	}
	return string(canonical), nil
}

// GenerateTrustConfigMap snapshots validated public certificate data. Projecting
// the snapshot rather than user sources ensures an invalid later source update
// cannot bypass validation by changing mounted files in an existing MCP pod.
func GenerateTrustConfigMap(r reconciler.Reconciler, ctx context.Context, cr *olsv1alpha1.OLSConfig) (*corev1.ConfigMap, error) {
	if _, err := GenerateConfigTOML(cr.Spec.OLSConfig.MCPKubeServerConfig); err != nil {
		return nil, err
	}
	baseline := olsv1alpha1.MCPCAReference{ConfigMap: &olsv1alpha1.MCPCAKeyReference{Name: utils.OLSCAConfigMap, Key: utils.AppOpenShiftMCPServerCACertFile}}
	serviceCA, err := readCABundle(r, ctx, baseline)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", utils.ErrGenerateOpenShiftMCPServerTrust, err)
	}
	data := map[string]string{"service-ca.crt": serviceCA}
	config := cr.Spec.OLSConfig.MCPKubeServerConfig
	if config != nil {
		for _, ref := range config.CAReferences() {
			if ref.ConfigMap != nil && (ref.ConfigMap.Name == trustConfigMapName || ref.ConfigMap.Name == utils.OpenShiftMCPServerConfigCmName || ref.ConfigMap.Name == utils.LegacyOpenShiftMCPServerCAConfigMapName) ||
				ref.Secret != nil && ref.Secret.Name == utils.OpenShiftMCPServerCertsSecretName {
				return nil, fmt.Errorf("%s: CA references cannot select reserved MCP output resources", utils.ErrGenerateOpenShiftMCPServerTrust)
			}
		}
		for i, ref := range config.CABundleRefs {
			bundle, err := readCABundle(r, ctx, ref)
			if err != nil {
				return nil, fmt.Errorf("%s: caBundleRefs[%d]: %w", utils.ErrGenerateOpenShiftMCPServerTrust, i, err)
			}
			data[fmt.Sprintf("shared-%03d.crt", i)] = bundle
		}
		if config.Toolsets != nil {
			for _, selection := range *config.Toolsets {
				var ref *olsv1alpha1.MCPCAReference
				switch {
				case selection.OSSM != nil:
					ref = selection.OSSM.CABundleRef
				case selection.NetObserv != nil:
					ref = selection.NetObserv.CABundleRef
					if ref == nil {
						ref = &baseline
					}
				default:
					continue
				}
				if ref == nil {
					return nil, fmt.Errorf("%s: %s requires caBundleRef", utils.ErrGenerateOpenShiftMCPServerTrust, selection.Name())
				}
				bundle, err := readCABundle(r, ctx, *ref)
				if err != nil {
					return nil, fmt.Errorf("%s: %s: %w", utils.ErrGenerateOpenShiftMCPServerTrust, selection.Name(), err)
				}
				data[selection.Name()+".crt"] = bundle
			}
		}
	}
	// Include reference identity as well as contents: name/key-only changes must
	// be observable even when the selected certificate bytes are identical.
	identity := struct {
		Data       map[string]string
		References []olsv1alpha1.MCPCAReference
	}{data, config.CAReferences()}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", utils.ErrGenerateOpenShiftMCPServerTrust, err)
	}
	digest := sha256.Sum256(encoded)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: trustConfigMapName, Namespace: r.GetNamespace(), Labels: selectorLabels(),
			Annotations: map[string]string{trustHashAnnotation: hex.EncodeToString(digest[:])},
		},
		Data: data,
	}
	if err := controllerutil.SetControllerReference(cr, cm, r.GetScheme()); err != nil {
		return nil, fmt.Errorf("%s: %w", utils.ErrGenerateOpenShiftMCPServerTrust, err)
	}
	return cm, nil
}

func reconcileTrustConfigMap(r reconciler.Reconciler, ctx context.Context, cr *olsv1alpha1.OLSConfig) error {
	desired, err := GenerateTrustConfigMap(r, ctx, cr)
	if err != nil {
		return err
	}
	existing := &corev1.ConfigMap{}
	err = r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("%s: %w", utils.ErrReconcileOpenShiftMCPServerTrust, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: %w", utils.ErrReconcileOpenShiftMCPServerTrust, err)
	}
	if !metav1.IsControlledBy(existing, cr) {
		return fmt.Errorf("%s: ConfigMap %s is not owned by OLSConfig", utils.ErrReconcileOpenShiftMCPServerTrust, existing.Name)
	}
	if utils.ConfigMapEqual(existing, desired) && reflect.DeepEqual(existing.Labels, desired.Labels) &&
		existing.Annotations[trustHashAnnotation] == desired.Annotations[trustHashAnnotation] {
		return nil
	}
	existing.Data = desired.Data
	existing.BinaryData = nil
	existing.Labels = desired.Labels
	if existing.Annotations == nil {
		existing.Annotations = map[string]string{}
	}
	existing.Annotations[trustHashAnnotation] = desired.Annotations[trustHashAnnotation]
	if err := r.Update(ctx, existing); err != nil {
		return fmt.Errorf("%s: %w", utils.ErrReconcileOpenShiftMCPServerTrust, err)
	}
	return nil
}

func trustVolumes(config *olsv1alpha1.MCPKubeServerConfiguration) ([]corev1.Volume, []corev1.VolumeMount) {
	mode := int32(0444)
	sharedItems := []corev1.KeyToPath{{Key: "service-ca.crt", Path: "service-ca.crt"}}
	if config != nil {
		for i := range config.CABundleRefs {
			key := fmt.Sprintf("shared-%03d.crt", i)
			sharedItems = append(sharedItems, corev1.KeyToPath{Key: key, Path: key})
		}
	}
	volume := func(name string, items []corev1.KeyToPath) corev1.Volume {
		return corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: trustConfigMapName}, Items: items, DefaultMode: &mode,
		}}}
	}
	volumes := []corev1.Volume{volume("mcp-shared-ca", sharedItems)}
	mounts := []corev1.VolumeMount{{Name: "mcp-shared-ca", MountPath: sharedTrustDir, ReadOnly: true}}
	if config != nil && config.Toolsets != nil {
		for _, selection := range *config.Toolsets {
			var filename string
			switch {
			case selection.OSSM != nil:
				filename = ossmCAPath
			case selection.NetObserv != nil:
				filename = netobservCAPath
			default:
				continue
			}
			name := "mcp-" + selection.Name() + "-ca"
			volumes = append(volumes, volume(name, []corev1.KeyToPath{{Key: selection.Name() + ".crt", Path: "ca.crt"}}))
			mounts = append(mounts, corev1.VolumeMount{Name: name, MountPath: path.Dir(filename), ReadOnly: true})
		}
	}
	return volumes, mounts
}

func deleteTrustConfigMap(r reconciler.Reconciler, ctx context.Context) error {
	cm := &corev1.ConfigMap{}
	if referenced, err := isUserCASource(r, ctx, cm, trustConfigMapName); err != nil || referenced {
		return err
	}
	if err := r.Get(ctx, client.ObjectKey{Name: trustConfigMapName, Namespace: r.GetNamespace()}, cm); err != nil {
		return client.IgnoreNotFound(err)
	}
	cr := &olsv1alpha1.OLSConfig{}
	if err := r.Get(ctx, client.ObjectKey{Name: utils.OLSConfigName}, cr); err != nil {
		return fmt.Errorf("cannot determine trust snapshot owner: %w", err)
	}
	if !metav1.IsControlledBy(cm, cr) {
		return fmt.Errorf("refusing to delete unowned trust ConfigMap %s", cm.Name)
	}
	return client.IgnoreNotFound(r.Delete(ctx, cm))
}
