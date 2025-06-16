package controller

import (
	"context"
	"fmt"
	"log"

	imagev1 "github.com/openshift/api/image/v1"
	olsv1alpha1 "github.com/openshift/lightspeed-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type ImageStreamTagReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *ImageStreamTagReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var ist imagev1.ImageStreamTag
	if err := r.Get(ctx, req.NamespacedName, &ist); err != nil {
		// ignore NotFound (deleted)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Printf("Detected update to ImageStreamTag: %s/%s", ist.Namespace, ist.Name)
	log.Printf("DockerImageReference: %s", ist.Image.DockerImageReference)
	log.Printf("Image SHA: %s", ist.Image.Name)

	olsconfig := &olsv1alpha1.OLSConfig{}
	err := r.Client.Get(ctx, client.ObjectKey{Name: "cluster", Namespace: "openshift-lightspeed"}, olsconfig)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ImageStreamTagReconciler, couldn't get olsconfig: %w", err)
	}
	olsconfig.Spec.OLSConfig.RAG[0].Image = ist.Image.DockerImageReference
	err = r.Update(ctx, olsconfig)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ImageStreamTagReconciler, couldn't update olsconfig: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *ImageStreamTagReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&imagev1.ImageStreamTag{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldIst, okOld := e.ObjectOld.(*imagev1.ImageStreamTag)
				newIst, okNew := e.ObjectNew.(*imagev1.ImageStreamTag)
				if !okOld || !okNew {
					return false
				}
				return oldIst.Image.Name != newIst.Image.Name // SHA change
			},
		}).
		Complete(r)
}
