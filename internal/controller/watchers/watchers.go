package watchers

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	olsv1alpha1 "github.com/openshift/lightspeed-operator/api/v1alpha1"
	"github.com/openshift/lightspeed-operator/internal/controller/agenticconsole"
	"github.com/openshift/lightspeed-operator/internal/controller/agenticintegration"
	"github.com/openshift/lightspeed-operator/internal/controller/alertsadapter"
	"github.com/openshift/lightspeed-operator/internal/controller/appserver"
	"github.com/openshift/lightspeed-operator/internal/controller/console"
	"github.com/openshift/lightspeed-operator/internal/controller/ocpmcp"
	"github.com/openshift/lightspeed-operator/internal/controller/otelcollector"
	"github.com/openshift/lightspeed-operator/internal/controller/postgres"
	"github.com/openshift/lightspeed-operator/internal/controller/reconciler"
	"github.com/openshift/lightspeed-operator/internal/controller/rhokp"
	"github.com/openshift/lightspeed-operator/internal/controller/utils"
)

// isSecretReferencedInCR checks if a secret is referenced in the OLSConfig CR
func isSecretReferencedInCR(cr *olsv1alpha1.OLSConfig, secretName string) bool {
	found := false
	_ = utils.ForEachExternalSecret(cr, func(name, source string) error {
		if name == secretName {
			found = true
			return fmt.Errorf("stop") // Stop iteration
		}
		return nil
	})
	return found
}

// isConfigMapReferencedInCR checks if a configmap is referenced in the OLSConfig CR
func isConfigMapReferencedInCR(cr *olsv1alpha1.OLSConfig, cmName string) bool {
	found := false
	_ = utils.ForEachExternalConfigMap(cr, func(name, source string) error {
		if name == cmName {
			found = true
			return fmt.Errorf("stop") // Stop iteration
		}
		return nil
	})
	return found
}

func ownedByOLSConfig(obj client.Object) bool {
	for _, owner := range obj.GetOwnerReferences() {
		if owner.Kind == utils.OLSConfigKind && owner.APIVersion == utils.OLSConfigAPIVersion {
			return true
		}
	}
	return false
}

func isSystemSecret(r reconciler.Reconciler, obj client.Object) bool {
	watcherConfig, _ := r.GetWatcherConfig().(*utils.WatcherConfig)
	if watcherConfig == nil {
		return false
	}
	for _, systemSecret := range watcherConfig.Secrets.SystemResources {
		if obj.GetNamespace() == systemSecret.Namespace && obj.GetName() == systemSecret.Name {
			return watcherConfig.IsSystemSecretWatchEnabled(systemSecret)
		}
	}
	return false
}

func isSystemConfigMap(r reconciler.Reconciler, obj client.Object) bool {
	watcherConfig, _ := r.GetWatcherConfig().(*utils.WatcherConfig)
	if watcherConfig == nil {
		return false
	}
	for _, systemCM := range watcherConfig.ConfigMaps.SystemResources {
		if obj.GetNamespace() == systemCM.Namespace && obj.GetName() == systemCM.Name {
			return true
		}
	}
	return false
}

// An old annotation is not an active consumer. References can be removed before
// the next source event, and must not fall back to app-server restarts.
func hasCurrentExternalReference(r reconciler.Reconciler, ctx context.Context, name string, secret bool) bool {
	cr := &olsv1alpha1.OLSConfig{}
	if err := r.Get(ctx, types.NamespacedName{Name: utils.OLSConfigName}, cr); err != nil {
		return true // Preserve existing routing when the CR cannot be inspected.
	}
	enumerate := utils.ForEachExternalConfigMap
	if secret {
		enumerate = utils.ForEachExternalSecret
	}
	found := false
	_ = enumerate(cr, func(candidate, _ string) error {
		found = found || candidate == name
		return nil
	})
	return found
}

func enqueueOLSConfig(q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	if q == nil {
		return
	}
	q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: utils.OLSConfigName}})
}

// handleMCPCAEvent queues validation before MCP can roll. User CA sources are
// never passed to the MCP restart callback. Other consumers of the same object
// still restart, even before annotations/mappings have been published.
// A true result means the user-reference event has been fully handled.
func handleMCPCAEvent(r reconciler.Reconciler, ctx context.Context, obj client.Object,
	q workqueue.TypedRateLimitingInterface[reconcile.Request], restart bool) bool {
	if obj.GetNamespace() != r.GetNamespace() {
		return false
	}
	_, secret := obj.(*v1.Secret)
	cr := &olsv1alpha1.OLSConfig{}
	if err := r.Get(ctx, types.NamespacedName{Name: utils.OLSConfigName}, cr); err != nil {
		return false
	}
	enabled := utils.BoolDeref(cr.Spec.OLSConfig.IntrospectionEnabled, true)
	baseline := !secret && obj.GetName() == utils.OLSCAConfigMap
	referenced := false
	// Inspect ungated refs as well to suppress stale annotations after disable.
	for _, ref := range cr.Spec.OLSConfig.MCPKubeServerConfig.CAReferences() {
		if secret && ref.Secret != nil && ref.Secret.Name == obj.GetName() ||
			!secret && ref.ConfigMap != nil && ref.ConfigMap.Name == obj.GetName() {
			referenced = true
			break
		}
	}
	if !baseline && !referenced {
		return false
	}
	if enabled {
		enqueueOLSConfig(q)
	}
	enumerate := utils.ForEachExternalConfigMap
	if secret {
		enumerate = utils.ForEachExternalSecret
	}
	targets := make(map[string]bool)
	_ = enumerate(cr, func(name, source string) error {
		if name != obj.GetName() || source == "mcp-ca" {
			return nil
		}
		if secret && strings.HasPrefix(source, "llm-provider-") &&
			utils.BoolDeref(cr.Spec.OLSConfig.CredentialHotReload, false) {
			return nil
		}
		if source == "alerts-adapter" {
			targets[utils.AlertsAdapterDeploymentName] = true
		} else {
			targets[utils.OLSAppServerDeploymentName] = true
			if source == "tls" {
				targets[utils.ConsoleUIDeploymentName] = true
			}
		}
		return nil
	})
	// Preserve system consumers as well as user consumers of the same source.
	// Keep system callback order, and never bypass validation with an MCP roll.
	var systemTargets []string
	system := baseline
	if wc, ok := r.GetWatcherConfig().(*utils.WatcherConfig); ok && wc != nil {
		if secret {
			for _, source := range wc.Secrets.SystemResources {
				if source.Name == obj.GetName() && source.Namespace == obj.GetNamespace() && wc.IsSystemSecretWatchEnabled(source) {
					system = true
					systemTargets = append(systemTargets, source.AffectedDeployments...)
				}
			}
		} else {
			for _, source := range wc.ConfigMaps.SystemResources {
				if source.Name == obj.GetName() && source.Namespace == obj.GetNamespace() {
					system = true
					systemTargets = append(systemTargets, source.AffectedDeployments...)
				}
			}
		}
	}
	if !restart && !enabled && (system || len(targets) > 0) {
		enqueueOLSConfig(q) // Deletion still revalidates non-MCP consumers.
	}
	if restart {
		for _, target := range systemTargets {
			if target != utils.OpenShiftMCPServerDeploymentName {
				restartDeployment(r, ctx, []string{target}, obj.GetNamespace(), obj.GetName())
			}
			delete(targets, target)
		}
		for target := range targets {
			restartDeployment(r, ctx, []string{target}, obj.GetNamespace(), obj.GetName())
		}
	}
	return true
}

// SecretUpdateHandler handles update events for Secrets and triggers deployment restarts when data changes.
type SecretUpdateHandler struct {
	Reconciler reconciler.Reconciler
}

// Create implements handler.EventHandler - handle creation of watched secrets
// This handles the case where a watched secret is created or recreated.
// MCP CA sources enqueue reconciliation for validation. Other sources are
// annotated if referenced and restart their consumers directly.
func (h *SecretUpdateHandler) Create(ctx context.Context, evt event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	obj := evt.Object
	secret, ok := obj.(*v1.Secret)
	if !ok {
		return
	}

	if secret.Namespace != h.Reconciler.GetNamespace() {
		return
	}
	if handleMCPCAEvent(h.Reconciler, ctx, secret, q, true) {
		return
	}

	// Skip operator-owned secrets - they're managed via Owns() relationship
	if ownedByOLSConfig(secret) {
		return
	}

	// Fetch the OLSConfig CR to check if this secret should be watched
	cr := &olsv1alpha1.OLSConfig{}
	err := h.Reconciler.Get(ctx, types.NamespacedName{Name: utils.OLSConfigName}, cr)
	if err != nil {
		h.Reconciler.GetLogger().Info("failed to get OLSConfig CR for secret watcher",
			"secret", secret.Name, "error", err)
		return
	}

	// Check if this secret is referenced in the CR
	if !isSecretReferencedInCR(cr, secret.Name) {
		return
	}

	// Annotate the secret if not already annotated
	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}
	if _, exists := secret.Annotations[utils.WatcherAnnotationKey]; !exists {
		utils.AnnotateSecretWatcher(secret)
		err = h.Reconciler.Update(ctx, secret)
		if err != nil {
			h.Reconciler.GetLogger().Info("failed to annotate secret for watcher",
				"secret", secret.Name, "error", err)
			return
		}
	}

	// Trigger deployment restarts for this recreated/created secret
	SecretWatcherFilter(h.Reconciler, ctx, obj)
}

// Update implements handler.EventHandler - this is where we check if secret data changed
func (h *SecretUpdateHandler) Update(ctx context.Context, evt event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	oldSecret, oldOk := evt.ObjectOld.(*v1.Secret)
	newSecret, newOk := evt.ObjectNew.(*v1.Secret)

	if !oldOk || !newOk {
		return
	}

	// Check if the data actually changed (not just metadata/annotations)
	if apiequality.Semantic.DeepEqual(oldSecret.Data, newSecret.Data) {
		// Data hasn't changed, skip
		return
	}

	if newSecret.Namespace != h.Reconciler.GetNamespace() && !isSystemSecret(h.Reconciler, newSecret) {
		return
	}
	if handleMCPCAEvent(h.Reconciler, ctx, newSecret, q, true) {
		return
	}
	SecretWatcherFilter(h.Reconciler, ctx, newSecret)
}

// Delete implements handler.EventHandler. External/system secret deletes enqueue
// OLSConfig so an event-driven controller can re-validate credentials and update status.
// Owned secrets are skipped; Owns() already requeues those.
func (h *SecretUpdateHandler) Delete(ctx context.Context, evt event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	obj := evt.Object
	if obj == nil || obj.GetNamespace() != h.Reconciler.GetNamespace() && !isSystemSecret(h.Reconciler, obj) {
		return
	}
	if handleMCPCAEvent(h.Reconciler, ctx, obj, q, false) || ownedByOLSConfig(obj) {
		return
	}
	if isSystemSecret(h.Reconciler, obj) {
		enqueueOLSConfig(q)
		return
	}
	cr := &olsv1alpha1.OLSConfig{}
	if err := h.Reconciler.Get(ctx, types.NamespacedName{Name: utils.OLSConfigName}, cr); err != nil {
		return
	}
	if isSecretReferencedInCR(cr, obj.GetName()) {
		enqueueOLSConfig(q)
	}
}

// Generic implements handler.EventHandler - we don't use generic events
func (h *SecretUpdateHandler) Generic(ctx context.Context, evt event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// No-op
}

// ConfigMapUpdateHandler handles update events for ConfigMaps and triggers deployment restarts when data changes.
type ConfigMapUpdateHandler struct {
	Reconciler reconciler.Reconciler
}

// Create implements handler.EventHandler - handle creation of watched configmaps
// This handles the case where a watched configmap is created or recreated.
// MCP CA sources enqueue reconciliation for validation. Other sources are
// annotated if referenced and restart their consumers directly.
func (h *ConfigMapUpdateHandler) Create(ctx context.Context, evt event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	obj := evt.Object
	cm, ok := obj.(*v1.ConfigMap)
	if !ok {
		return
	}

	if cm.Namespace != h.Reconciler.GetNamespace() {
		return
	}
	if handleMCPCAEvent(h.Reconciler, ctx, cm, q, true) {
		return
	}

	// Skip operator-owned configmaps - they're managed via Owns() relationship
	if ownedByOLSConfig(cm) {
		return
	}

	// Fetch the OLSConfig CR to check if this configmap should be watched
	cr := &olsv1alpha1.OLSConfig{}
	err := h.Reconciler.Get(ctx, types.NamespacedName{Name: utils.OLSConfigName}, cr)
	if err != nil {
		h.Reconciler.GetLogger().Info("failed to get OLSConfig CR for configmap watcher",
			"configmap", cm.Name, "error", err)
		return
	}

	// Check if this configmap is referenced in the CR
	if !isConfigMapReferencedInCR(cr, cm.Name) {
		return
	}

	// Annotate the configmap if not already annotated
	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}
	if _, exists := cm.Annotations[utils.WatcherAnnotationKey]; !exists {
		utils.AnnotateConfigMapWatcher(cm)
		err = h.Reconciler.Update(ctx, cm)
		if err != nil {
			h.Reconciler.GetLogger().Info("failed to annotate configmap for watcher",
				"configmap", cm.Name, "error", err)
			return
		}
	}

	// Trigger deployment restarts for this recreated/created configmap
	ConfigMapWatcherFilter(h.Reconciler, ctx, obj)
}

// Update implements handler.EventHandler - this is where we check if configmap data changed
func (h *ConfigMapUpdateHandler) Update(ctx context.Context, evt event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	oldCM, oldOk := evt.ObjectOld.(*v1.ConfigMap)
	newCM, newOk := evt.ObjectNew.(*v1.ConfigMap)

	if !oldOk || !newOk {
		return
	}

	// Check if the data actually changed (not just metadata/annotations)
	if apiequality.Semantic.DeepEqual(oldCM.Data, newCM.Data) &&
		apiequality.Semantic.DeepEqual(oldCM.BinaryData, newCM.BinaryData) {
		// Data hasn't changed, skip
		return
	}

	if newCM.Namespace != h.Reconciler.GetNamespace() && !isSystemConfigMap(h.Reconciler, newCM) {
		return
	}
	if handleMCPCAEvent(h.Reconciler, ctx, newCM, q, true) {
		return
	}
	ConfigMapWatcherFilter(h.Reconciler, ctx, newCM)
}

// Delete implements handler.EventHandler. External/system configmap deletes enqueue
// OLSConfig so an event-driven controller notices missing CA / alerts-adapter config.
// Owned configmaps are skipped; Owns() already requeues those.
func (h *ConfigMapUpdateHandler) Delete(ctx context.Context, evt event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	obj := evt.Object
	if obj == nil || obj.GetNamespace() != h.Reconciler.GetNamespace() && !isSystemConfigMap(h.Reconciler, obj) {
		return
	}
	if handleMCPCAEvent(h.Reconciler, ctx, obj, q, false) || ownedByOLSConfig(obj) {
		return
	}
	if isSystemConfigMap(h.Reconciler, obj) {
		enqueueOLSConfig(q)
		return
	}
	cr := &olsv1alpha1.OLSConfig{}
	if err := h.Reconciler.Get(ctx, types.NamespacedName{Name: utils.OLSConfigName}, cr); err != nil {
		return
	}
	if isConfigMapReferencedInCR(cr, obj.GetName()) {
		enqueueOLSConfig(q)
	}
}

// Generic implements handler.EventHandler - we don't use generic events
func (h *ConfigMapUpdateHandler) Generic(ctx context.Context, evt event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	// No-op
}

// SecretWatcherFilter is a data-driven filter function for watching Secrets.
// It uses the reconciler's WatcherConfig to determine which deployments to restart when a Secret changes.
//
// The filter handles two types of secrets:
// 1. System secrets (e.g., telemetry pull secret) - defined in WatcherConfig.Secrets.SystemResources
// 2. Annotated secrets (user-provided from CR) - marked with utils.WatcherAnnotationKey
//
// For system secrets, it uses the AffectedDeployments from WatcherConfig.
// For annotated secrets, it looks up the deployment mapping via WatcherConfig.GetAnnotatedSecretDeployments.
//
// This function directly restarts affected deployments and does not trigger reconciliation.
func SecretWatcherFilter(r reconciler.Reconciler, ctx context.Context, obj client.Object, inCluster ...bool) {

	// Set default value for inCluster
	inClusterValue := true
	if len(inCluster) > 0 {
		inClusterValue = inCluster[0]
	}

	// Get watcherConfig from reconciler
	watcherConfig, _ := r.GetWatcherConfig().(*utils.WatcherConfig)

	// Check 1: Check against configured system secrets (no hardcoded values!)
	if watcherConfig != nil {
		for _, systemSecret := range watcherConfig.Secrets.SystemResources {
			if obj.GetNamespace() == systemSecret.Namespace && obj.GetName() == systemSecret.Name {
				if !watcherConfig.IsSystemSecretWatchEnabled(systemSecret) {
					return
				}
				r.GetLogger().Info("Detected system secret change",
					"secret", systemSecret.Name,
					"namespace", systemSecret.Namespace,
					"description", systemSecret.Description)

				// Restart all affected deployments
				if inClusterValue {
					restartDeployment(r, ctx, systemSecret.AffectedDeployments, systemSecret.Namespace, systemSecret.Name)
				}
				return
			}
		}
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	// Check 2: Look for watcher annotation (user-provided secrets)
	if _, exist := annotations[utils.WatcherAnnotationKey]; exist {
		secretName := obj.GetName()
		if !hasCurrentExternalReference(r, ctx, secretName, true) {
			return
		}

		// For annotated secrets, determine affected deployments from mapping
		var affectedDeployments []string
		var found bool
		if watcherConfig != nil {
			affectedDeployments, found = watcherConfig.GetAnnotatedSecretDeployments(secretName)
		}
		if !found {
			// Default: affect only app-server (e.g., LLM provider secrets)
			affectedDeployments = []string{utils.OLSAppServerDeploymentName}
		}

		r.GetLogger().Info("Detected annotated secret change",
			"secret", secretName,
			"affectedDeployments", affectedDeployments)
		if inClusterValue {
			restartDeployment(r, ctx, affectedDeployments, obj.GetNamespace(), secretName)
		}
		return
	}
	// Not a watched secret - no reconciliation needed. Should never happen
}

// ConfigMapWatcherFilter is a data-driven filter function for watching ConfigMaps.
// It uses the reconciler's WatcherConfig to determine which deployments to restart when a ConfigMap changes.
//
// The filter handles two types of configmaps:
// 1. System configmaps (e.g., OpenShift CA bundle) - defined in WatcherConfig.ConfigMaps.SystemResources
// 2. Annotated configmaps (user-provided from CR) - marked with utils.WatcherAnnotationKey
//
// For system configmaps, it uses the AffectedDeployments from WatcherConfig.
// For annotated configmaps, it looks up the deployment mapping via WatcherConfig.GetAnnotatedConfigMapDeployments.
//
// This function directly restarts affected deployments and does not trigger reconciliation.
func ConfigMapWatcherFilter(r reconciler.Reconciler, ctx context.Context, obj client.Object, inCluster ...bool) {

	// Set default value for inCluster
	inClusterValue := true
	if len(inCluster) > 0 {
		inClusterValue = inCluster[0]
	}

	// Get watcherConfig from reconciler
	watcherConfig, _ := r.GetWatcherConfig().(*utils.WatcherConfig)

	// Check 1: Check against configured system configmaps (no hardcoded values!)
	if watcherConfig != nil {
		for _, systemCM := range watcherConfig.ConfigMaps.SystemResources {
			if obj.GetNamespace() == systemCM.Namespace && obj.GetName() == systemCM.Name {
				r.GetLogger().Info("Detected system configmap change",
					"configmap", systemCM.Name,
					"namespace", systemCM.Namespace,
					"description", systemCM.Description)

				// Restart all affected deployments
				if inClusterValue {
					restartDeployment(r, ctx, systemCM.AffectedDeployments, systemCM.Namespace, systemCM.Name)
				}
				return
			}
		}
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	// Check 2: Look for watcher annotation (user-provided configmaps)
	if _, exist := annotations[utils.WatcherAnnotationKey]; exist {
		if !hasCurrentExternalReference(r, ctx, obj.GetName(), false) {
			return
		}
		// For annotated configmaps, determine affected deployments from mapping
		configMapName := obj.GetName()
		var affectedDeployments []string
		var found bool
		if watcherConfig != nil {
			affectedDeployments, found = watcherConfig.GetAnnotatedConfigMapDeployments(configMapName)
		}
		if !found {
			// Default: affect only app-server (e.g., CA bundle configmaps)
			affectedDeployments = []string{utils.OLSAppServerDeploymentName}
		}

		r.GetLogger().Info("Detected annotated configmap change",
			"configmap", configMapName,
			"affectedDeployments", affectedDeployments)
		if inClusterValue {
			restartDeployment(r, ctx, affectedDeployments, obj.GetNamespace(), configMapName)
		}
		return
	}
	// Not a watched configmap - no reconciliation needed. Should never happen
}

// RestartFunc is a function that restarts a deployment
type RestartFunc func(reconciler.Reconciler, context.Context, ...*appsv1.Deployment) error

// restartFuncs maps deployment names (or pseudo-targets) to their restart functions.
var restartFuncs = map[string]RestartFunc{
	utils.OLSAppServerDeploymentName:       appserver.RestartAppServer,
	utils.PostgresDeploymentName:           postgres.RestartPostgres,
	utils.ConsoleUIDeploymentName:          console.RestartConsoleUI,
	utils.AgenticConsoleUIDeploymentName:   agenticconsole.RestartAgenticConsoleUI,
	utils.AlertsAdapterDeploymentName:      alertsadapter.RestartAlertsAdapter,
	utils.OtelCollectorDeploymentName:      otelcollector.RestartOtelCollector,
	utils.OpenShiftMCPServerDeploymentName: ocpmcp.Restart,
	utils.RHOKPDeploymentName:              rhokp.Restart,
	// Pseudo-target: touch the agentic handoff ConfigMap so agentic-operator reloads CA material.
	utils.AgenticConfigurationConfigMapName: touchAgenticConfigurationFunc,
}

// touchAgenticConfigurationFunc adapts TouchAgenticConfiguration to the RestartFunc signature.
func touchAgenticConfigurationFunc(r reconciler.Reconciler, ctx context.Context, _ ...*appsv1.Deployment) error {
	return agenticintegration.TouchAgenticConfiguration(r, ctx)
}

// restart corresponding deployment
func restartDeployment(r reconciler.Reconciler, ctx context.Context, affectedDeployments []string, namespace string, name string) {

	for _, depName := range affectedDeployments {
		// Restart the deployment using the appropriate function
		restartFunc, exists := restartFuncs[depName]
		if !exists {
			r.GetLogger().Info("unknown deployment name", "deployment", depName)
			continue
		}

		err := restartFunc(r, ctx)
		if err != nil {
			r.GetLogger().Error(err, "failed to restart deployment",
				"deployment", depName, "resource", name, "namespace", namespace)
			// Continue with other deployments
		} else {
			r.GetLogger().Info("restarted deployment",
				"deployment", depName, "resource", name, "namespace", namespace)
		}
	}
}
