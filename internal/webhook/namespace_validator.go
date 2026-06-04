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

package webhook

import (
	"context"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	nsclassv1alpha1 "github.com/laverya/nsclass-controller/api/v1alpha1"
)

const (
	// NamespaceValidationPath is the admission path used for Namespace validation.
	NamespaceValidationPath = "/validate-v1-namespace"

	namespaceClassNameKey = "namespaceclass.akuity.io/name"
)

// NamespaceValidator validates Namespace namespace class references.
type NamespaceValidator struct {
	Client client.Client
}

// SetupWebhookWithManager registers the Namespace validating webhook.
func (v *NamespaceValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	if v.Client == nil {
		v.Client = mgr.GetClient()
	}
	mgr.GetWebhookServer().Register(NamespaceValidationPath, &admission.Webhook{Handler: v})
	return nil
}

// Handle validates Namespace create/update requests that reference a NamespaceClass.
func (v *NamespaceValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	if req.Operation != admissionv1.Create && req.Operation != admissionv1.Update {
		return admission.Allowed("operation does not require namespace class validation")
	}

	namespace := &corev1.Namespace{}
	if err := json.Unmarshal(req.Object.Raw, namespace); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decode Namespace: %w", err))
	}

	className := namespaceClassName(namespace)
	if className == "" {
		return admission.Allowed("Namespace does not reference a NamespaceClass")
	}

	namespaceClass := &nsclassv1alpha1.NamespaceClass{}
	if err := v.Client.Get(ctx, types.NamespacedName{Name: className}, namespaceClass); err != nil {
		if apierrors.IsNotFound(err) {
			return admission.Denied(fmt.Sprintf("NamespaceClass %q does not exist", className))
		}
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("get NamespaceClass %q: %w", className, err))
	}

	return admission.Allowed("NamespaceClass exists")
}

func namespaceClassName(namespace *corev1.Namespace) string {
	if className := namespace.Labels[namespaceClassNameKey]; className != "" {
		return className
	}
	return namespace.Annotations[namespaceClassNameKey]
}
