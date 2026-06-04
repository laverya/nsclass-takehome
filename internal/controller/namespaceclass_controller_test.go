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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nsclassv1alpha1 "github.com/laverya/nsclass-controller/api/v1alpha1"
)

var _ = Describe("NamespaceClass Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: resourceName,
		}
		namespaceclass := &nsclassv1alpha1.NamespaceClass{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind NamespaceClass")
			err := k8sClient.Get(ctx, typeNamespacedName, namespaceclass)
			if err != nil && errors.IsNotFound(err) {
				resource := &nsclassv1alpha1.NamespaceClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: resourceName,
					},
					// TODO(user): Specify other spec details if needed.
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &nsclassv1alpha1.NamespaceClass{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance NamespaceClass")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &NamespaceClassReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})

	Context("When mapping Namespace events", func() {
		It("should reconcile the NamespaceClass named by the namespace label", func() {
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-namespace",
					Labels: map[string]string{
						namespaceClassNameKey: "gold",
					},
				},
			}

			requests := namespaceToNamespaceClassRequests(ctx, namespace)

			Expect(requests).To(Equal([]reconcile.Request{{
				NamespacedName: types.NamespacedName{Name: "gold"},
			}}))
		})

		It("should read the NamespaceClass name from labels or annotations", func() {
			Expect(namespaceClassName(namespaceWithClass("gold"))).To(Equal("gold"))
			Expect(namespaceClassName(&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-namespace",
					Annotations: map[string]string{
						namespaceClassNameKey: "silver",
					},
				},
			})).To(Equal("silver"))
		})
	})

	Context("When applying NamespaceClass resources", func() {
		const configMapName = "managed-config"

		var (
			className              string
			matchingNamespaceName  string
			unmatchedNamespaceName string
		)

		BeforeEach(func() {
			suffix := time.Now().UnixNano()
			className = fmt.Sprintf("apply-test-%d", suffix)
			matchingNamespaceName = fmt.Sprintf("apply-test-matching-%d", suffix)
			unmatchedNamespaceName = fmt.Sprintf("apply-test-unmatched-%d", suffix)

			Expect(k8sClient.Create(ctx, namespaceWithLabels(matchingNamespaceName, map[string]string{
				namespaceClassNameKey: className,
			}))).To(Succeed())
			Expect(k8sClient.Create(ctx, namespaceWithLabels(unmatchedNamespaceName, nil))).To(Succeed())
		})

		AfterEach(func() {
			deleteIfExists(ctx, &nsclassv1alpha1.NamespaceClass{ObjectMeta: metav1.ObjectMeta{Name: className}})
			deleteIfExists(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: matchingNamespaceName}})
			deleteIfExists(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: unmatchedNamespaceName}})
		})

		It("should apply namespaced resources to matching namespaces with controller annotations", func() {
			namespaceClass := &nsclassv1alpha1.NamespaceClass{
				ObjectMeta: metav1.ObjectMeta{Name: className},
				Spec: nsclassv1alpha1.NamespaceClassSpec{
					Resources: []runtime.RawExtension{{
						Raw: []byte(`{
							"apiVersion": "v1",
							"kind": "ConfigMap",
							"metadata": {
								"name": "managed-config",
								"annotations": {
									"example.com/existing": "kept"
								}
							},
							"data": {
								"key": "value"
							}
						}`),
					}},
				},
			}
			Expect(k8sClient.Create(ctx, namespaceClass)).To(Succeed())

			controllerReconciler := &NamespaceClassReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: className},
			})
			Expect(err).NotTo(HaveOccurred())

			configMap := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: matchingNamespaceName,
				Name:      configMapName,
			}, configMap)).To(Succeed())
			Expect(configMap.Data).To(HaveKeyWithValue("key", "value"))
			Expect(configMap.Annotations).To(HaveKeyWithValue("example.com/existing", "kept"))
			Expect(configMap.Annotations).To(HaveKeyWithValue(namespaceClassManagedByAnnotation, namespaceClassManagedByValue))
			Expect(configMap.Annotations).To(HaveKeyWithValue(namespaceClassNameKey, className))

			unmatchedConfigMap := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Namespace: unmatchedNamespaceName,
				Name:      configMapName,
			}, unmatchedConfigMap)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("should reject cluster-scoped resources", func() {
			namespaceClass := &nsclassv1alpha1.NamespaceClass{
				ObjectMeta: metav1.ObjectMeta{Name: className},
				Spec: nsclassv1alpha1.NamespaceClassSpec{
					Resources: []runtime.RawExtension{{
						Raw: []byte(`{
							"apiVersion": "v1",
							"kind": "Namespace",
							"metadata": {
								"name": "must-not-apply"
							}
						}`),
					}},
				},
			}
			Expect(k8sClient.Create(ctx, namespaceClass)).To(Succeed())

			controllerReconciler := &NamespaceClassReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: className},
			})
			Expect(err).To(MatchError(ContainSubstring("is not namespace scoped")))
		})
	})
})

func namespaceWithClass(className string) *corev1.Namespace {
	if className == "" {
		return namespaceWithLabels("test-namespace", nil)
	}
	return namespaceWithLabels("test-namespace", map[string]string{
		namespaceClassNameKey: className,
	})
}

func namespaceWithLabels(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
}

func deleteIfExists(ctx context.Context, obj client.Object) {
	err := k8sClient.Delete(ctx, obj)
	if err != nil && !errors.IsNotFound(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}
