/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	nsclassv1alpha1 "github.com/laverya/nsclass-controller/api/v1alpha1"
)

const (
	namespaceClassNameKey             = "namespaceclass.akuity.io/name"
	namespaceClassManagedByAnnotation = "namespaceclass.akuity.io/managed-by"
	namespaceClassManagedByValue      = "nsclass-controller"
	namespaceClassFieldOwner          = "nsclass-controller"
)

// NamespaceClassReconciler reconciles a NamespaceClass object
type NamespaceClassReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=nsclass.nsclass.laverya.com,resources=namespaceclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=nsclass.nsclass.laverya.com,resources=namespaceclasses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=nsclass.nsclass.laverya.com,resources=namespaceclasses/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=*,resources=*,verbs=get;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/reconcile
func (r *NamespaceClassReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	namespaceClass := &nsclassv1alpha1.NamespaceClass{}
	if err := r.Get(ctx, types.NamespacedName{Name: req.Name}, namespaceClass); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	desiredObjects, desiredResources, namespaceClassesByNamespace, err := r.desiredResources(ctx, namespaceClass)
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, stale := range staleManagedResources(namespaceClass.Status.ManagedResources, desiredResources) {
		if !shouldDeleteManagedResource(stale, namespaceClassesByNamespace, namespaceClassRemovalPolicy(namespaceClass)) {
			continue
		}
		if err := r.deleteManagedResource(ctx, namespaceClass.Name, stale); err != nil {
			return ctrl.Result{}, err
		}
	}

	for _, obj := range desiredObjects {
		if err := r.applyResource(ctx, obj); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.updateManagedResourcesStatus(ctx, namespaceClass.Name, desiredResources); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Applied NamespaceClass resources", "namespaceClass", namespaceClass.Name, "resourceCount", len(desiredResources))

	return ctrl.Result{}, nil
}

func (r *NamespaceClassReconciler) desiredResources(
	ctx context.Context,
	namespaceClass *nsclassv1alpha1.NamespaceClass,
) (
	[]*unstructured.Unstructured,
	[]nsclassv1alpha1.NamespaceClassManagedResource,
	map[string]string,
	error,
) {
	namespaces := &corev1.NamespaceList{}
	if err := r.List(ctx, namespaces); err != nil {
		return nil, nil, nil, fmt.Errorf("list namespaces for NamespaceClass %q: %w", namespaceClass.Name, err)
	}

	var objects []*unstructured.Unstructured
	var resources []nsclassv1alpha1.NamespaceClassManagedResource
	namespaceClassesByNamespace := map[string]string{}
	for i := range namespaces.Items {
		namespace := &namespaces.Items[i]
		namespaceClassesByNamespace[namespace.Name] = namespaceClassName(namespace)
		if namespaceClassesByNamespace[namespace.Name] != namespaceClass.Name {
			continue
		}

		for j, resource := range namespaceClass.Spec.Resources {
			obj, err := r.resourceForNamespace(resource, namespaceClass.Name, namespace.Name)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("prepare spec.resources[%d] for NamespaceClass %q: %w", j, namespaceClass.Name, err)
			}

			objects = append(objects, obj)
			resources = append(resources, managedResourceFromObject(obj))
		}
	}

	sortManagedResources(resources)

	return objects, resources, namespaceClassesByNamespace, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceClassReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&nsclassv1alpha1.NamespaceClass{}).
		Watches(
			&corev1.Namespace{},
			namespaceClassEventHandler(),
		).
		Named("namespaceclass").
		Complete(r)
}

func namespaceToNamespaceClassRequests(_ context.Context, obj client.Object) []reconcile.Request {
	className := namespaceClassName(obj)
	if className == "" {
		return nil
	}

	return []reconcile.Request{namespaceClassRequest(className)}
}

func namespaceClassEventHandler() handler.EventHandler {
	return handler.Funcs{
		CreateFunc: func(_ context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueueNamespaceClassRequest(q, namespaceClassName(e.Object))
		},
		UpdateFunc: func(_ context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			oldClassName := namespaceClassName(e.ObjectOld)
			newClassName := namespaceClassName(e.ObjectNew)
			if oldClassName == newClassName {
				return
			}
			enqueueNamespaceClassRequest(q, oldClassName)
			if newClassName == "" {
				return
			}
			enqueueNamespaceClassRequest(q, newClassName)
		},
		DeleteFunc: func(_ context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueueNamespaceClassRequest(q, namespaceClassName(e.Object))
		},
		GenericFunc: func(_ context.Context, e event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueueNamespaceClassRequest(q, namespaceClassName(e.Object))
		},
	}
}

func enqueueNamespaceClassRequest(q workqueue.TypedRateLimitingInterface[reconcile.Request], className string) {
	if className == "" {
		return
	}
	q.Add(namespaceClassRequest(className))
}

func namespaceClassRequest(className string) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Name: className}}
}

func namespaceClassName(obj client.Object) string {
	if obj == nil {
		return ""
	}

	if className := obj.GetLabels()[namespaceClassNameKey]; className != "" {
		return className
	}

	return obj.GetAnnotations()[namespaceClassNameKey]
}

func (r *NamespaceClassReconciler) applyResource(ctx context.Context, obj *unstructured.Unstructured) error {
	log := logf.FromContext(ctx)

	if err := r.Apply(
		ctx,
		client.ApplyConfigurationFromUnstructured(obj),
		client.FieldOwner(namespaceClassFieldOwner),
		client.ForceOwnership,
	); err != nil {
		return fmt.Errorf(
			"apply %s %q to Namespace %q: %w",
			obj.GroupVersionKind().String(),
			obj.GetName(),
			obj.GetNamespace(),
			err,
		)
	}

	log.Info(
		"Applied resource",
		"apiVersion", obj.GetAPIVersion(),
		"kind", obj.GetKind(),
		"namespace", obj.GetNamespace(),
		"name", obj.GetName(),
	)

	return nil
}

func (r *NamespaceClassReconciler) resourceForNamespace(
	resource runtime.RawExtension,
	namespaceClassName string,
	namespaceName string,
) (*unstructured.Unstructured, error) {
	obj, err := rawExtensionToUnstructured(resource)
	if err != nil {
		return nil, err
	}

	gvk := obj.GroupVersionKind()
	if gvk.Empty() || gvk.Kind == "" || gvk.Version == "" {
		return nil, fmt.Errorf("resource must specify apiVersion and kind")
	}
	if obj.GetName() == "" {
		return nil, fmt.Errorf("%s resource must specify metadata.name", gvk.String())
	}

	namespaced, err := apiutil.IsGVKNamespaced(gvk, r.RESTMapper())
	if err != nil {
		return nil, fmt.Errorf("determine scope for %s: %w", gvk.String(), err)
	}
	if !namespaced {
		return nil, fmt.Errorf("%s %q is not namespace scoped", gvk.String(), obj.GetName())
	}

	obj.SetNamespace(namespaceName)
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[namespaceClassManagedByAnnotation] = namespaceClassManagedByValue
	annotations[namespaceClassNameKey] = namespaceClassName
	obj.SetAnnotations(annotations)

	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[namespaceClassManagedByAnnotation] = namespaceClassManagedByValue
	labels[namespaceClassNameKey] = namespaceClassName
	obj.SetLabels(labels)

	return obj, nil
}

func (r *NamespaceClassReconciler) deleteManagedResource(
	ctx context.Context,
	namespaceClassName string,
	resource nsclassv1alpha1.NamespaceClassManagedResource,
) error {
	gv, err := schema.ParseGroupVersion(resource.APIVersion)
	if err != nil {
		return fmt.Errorf("parse apiVersion for managed resource %s/%s: %w", resource.Namespace, resource.Name, err)
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   gv.Group,
		Version: gv.Version,
		Kind:    resource.Kind,
	})
	if err := r.Get(ctx, types.NamespacedName{Namespace: resource.Namespace, Name: resource.Name}, obj); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !isManagedByNamespaceClass(obj, namespaceClassName) {
		return nil
	}
	if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete managed %s %q from Namespace %q: %w", obj.GroupVersionKind().String(), obj.GetName(), obj.GetNamespace(), err)
	}

	return nil
}

func isManagedByNamespaceClass(obj client.Object, namespaceClassName string) bool {
	annotations := obj.GetAnnotations()
	if annotations[namespaceClassManagedByAnnotation] == namespaceClassManagedByValue &&
		annotations[namespaceClassNameKey] == namespaceClassName {
		return true
	}

	labels := obj.GetLabels()
	return labels[namespaceClassManagedByAnnotation] == namespaceClassManagedByValue &&
		labels[namespaceClassNameKey] == namespaceClassName
}

func (r *NamespaceClassReconciler) updateManagedResourcesStatus(
	ctx context.Context,
	namespaceClassName string,
	resources []nsclassv1alpha1.NamespaceClassManagedResource,
) error {
	namespaceClass := &nsclassv1alpha1.NamespaceClass{}
	if err := r.Get(ctx, types.NamespacedName{Name: namespaceClassName}, namespaceClass); err != nil {
		return client.IgnoreNotFound(err)
	}
	if reflect.DeepEqual(namespaceClass.Status.ManagedResources, resources) {
		return nil
	}

	namespaceClass.Status.ManagedResources = resources
	if err := r.Status().Update(ctx, namespaceClass); err != nil {
		return fmt.Errorf("update NamespaceClass %q status: %w", namespaceClassName, err)
	}

	return nil
}

func staleManagedResources(
	current []nsclassv1alpha1.NamespaceClassManagedResource,
	desired []nsclassv1alpha1.NamespaceClassManagedResource,
) []nsclassv1alpha1.NamespaceClassManagedResource {
	desiredSet := map[nsclassv1alpha1.NamespaceClassManagedResource]struct{}{}
	for _, resource := range desired {
		desiredSet[resource] = struct{}{}
	}

	var stale []nsclassv1alpha1.NamespaceClassManagedResource
	for _, resource := range current {
		if _, ok := desiredSet[resource]; !ok {
			stale = append(stale, resource)
		}
	}

	return stale
}

func shouldDeleteManagedResource(
	resource nsclassv1alpha1.NamespaceClassManagedResource,
	namespaceClassesByNamespace map[string]string,
	removalPolicy nsclassv1alpha1.NamespaceClassRemovalPolicy,
) bool {
	currentClassName, namespaceExists := namespaceClassesByNamespace[resource.Namespace]
	if !namespaceExists {
		return true
	}
	if currentClassName == "" {
		return removalPolicy == nsclassv1alpha1.NamespaceClassRemovalPolicyDelete
	}

	return true
}

func namespaceClassRemovalPolicy(namespaceClass *nsclassv1alpha1.NamespaceClass) nsclassv1alpha1.NamespaceClassRemovalPolicy {
	if namespaceClass.Spec.RemovalPolicy == "" {
		return nsclassv1alpha1.NamespaceClassRemovalPolicyRetain
	}
	return namespaceClass.Spec.RemovalPolicy
}

func managedResourceFromObject(obj *unstructured.Unstructured) nsclassv1alpha1.NamespaceClassManagedResource {
	return nsclassv1alpha1.NamespaceClassManagedResource{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
	}
}

func sortManagedResources(resources []nsclassv1alpha1.NamespaceClassManagedResource) {
	sort.Slice(resources, func(i, j int) bool {
		left := resources[i]
		right := resources[j]
		if left.APIVersion != right.APIVersion {
			return left.APIVersion < right.APIVersion
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Namespace != right.Namespace {
			return left.Namespace < right.Namespace
		}
		return left.Name < right.Name
	})
}

func rawExtensionToUnstructured(resource runtime.RawExtension) (*unstructured.Unstructured, error) {
	if resource.Object != nil {
		if obj, ok := resource.Object.(*unstructured.Unstructured); ok {
			return obj.DeepCopy(), nil
		}

		object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(resource.Object)
		if err != nil {
			return nil, fmt.Errorf("convert resource to unstructured: %w", err)
		}
		return &unstructured.Unstructured{Object: object}, nil
	}

	if len(resource.Raw) == 0 {
		return nil, fmt.Errorf("resource is empty")
	}

	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(resource.Raw); err != nil {
		return nil, fmt.Errorf("decode raw resource: %w", err)
	}

	return obj, nil
}
