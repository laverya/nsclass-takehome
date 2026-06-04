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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	nsclassv1alpha1 "github.com/laverya/nsclass-controller/api/v1alpha1"
)

const (
	namespaceClassNameLabel           = "namespaceclass.akuity.io/name"
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
// +kubebuilder:rbac:groups=,resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=*,resources=*,verbs=get;create;update;patch

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

	namespaces := &corev1.NamespaceList{}
	if err := r.List(ctx, namespaces, client.MatchingLabels{namespaceClassNameLabel: namespaceClass.Name}); err != nil {
		return ctrl.Result{}, fmt.Errorf("list namespaces for NamespaceClass %q: %w", namespaceClass.Name, err)
	}

	for i := range namespaces.Items {
		namespace := &namespaces.Items[i]
		if err := r.applyResourcesToNamespace(ctx, namespaceClass, namespace); err != nil {
			return ctrl.Result{}, err
		}
	}

	log.Info("Applied NamespaceClass resources", "namespaceClass", namespaceClass.Name, "namespaceCount", len(namespaces.Items))

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceClassReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&nsclassv1alpha1.NamespaceClass{}).
		Watches(
			&corev1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(namespaceToNamespaceClassRequests),
			builder.WithPredicates(namespaceClassLabelChangedPredicate()),
		).
		Named("namespaceclass").
		Complete(r)
}

func namespaceToNamespaceClassRequests(_ context.Context, obj client.Object) []reconcile.Request {
	className := namespaceClassName(obj)
	if className == "" {
		return nil
	}

	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Name: className},
	}}
}

func namespaceClassLabelChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return namespaceClassName(e.Object) != ""
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return namespaceClassName(e.Object) != ""
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return namespaceClassName(e.ObjectOld) != namespaceClassName(e.ObjectNew)
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return namespaceClassName(e.Object) != ""
		},
	}
}

func namespaceClassName(obj client.Object) string {
	if obj == nil {
		return ""
	}

	return obj.GetLabels()[namespaceClassNameLabel]
}

func (r *NamespaceClassReconciler) applyResourcesToNamespace(
	ctx context.Context,
	namespaceClass *nsclassv1alpha1.NamespaceClass,
	namespace *corev1.Namespace,
) error {
	log := logf.FromContext(ctx)

	for i, resource := range namespaceClass.Spec.Resources {
		obj, err := r.resourceForNamespace(resource, namespaceClass.Name, namespace.Name)
		if err != nil {
			return fmt.Errorf("prepare spec.resources[%d] for NamespaceClass %q: %w", i, namespaceClass.Name, err)
		}

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
				namespace.Name,
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
	}

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
	annotations[namespaceClassNameLabel] = namespaceClassName
	obj.SetAnnotations(annotations)

	return obj, nil
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
