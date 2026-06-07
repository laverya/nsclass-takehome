//go:build e2e
// +build e2e

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

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/laverya/nsclass-controller/test/utils"
)

// namespace where the project is deployed in
const namespace = "nsclass-controller-system"

// serviceAccountName created for the project
const serviceAccountName = "nsclass-controller-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "nsclass-controller-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "nsclass-controller-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("deleting NamespaceClasses before undeploying the controller")
		deleteAllNamespaceClasses()

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				By("getting the name of the controller-manager pod")
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				By("validating the pod's status")
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=nsclass-controller-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("ensuring the controller pod is ready")
			verifyControllerPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", controllerPodName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Controller pod not ready")
			}
			Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Serving metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

			// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": [
								"for i in $(seq 1 30); do curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics && exit 0 || sleep 2; done; exit 1"
							],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccountName": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			verifyMetricsAvailable := func(g Gomega) {
				metricsOutput, err := getMetricsOutput()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
				g.Expect(metricsOutput).NotTo(BeEmpty())
				g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
			}
			Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks

		// TODO: Customize the e2e test suite with scenarios specific to your project.
		// Consider applying sample/CR(s) and check their status and/or verifying
		// the reconciliation by using the metrics, i.e.:
		// metricsOutput, err := getMetricsOutput()
		// Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
		// Expect(metricsOutput).To(ContainSubstring(
		//    fmt.Sprintf(`controller_runtime_reconcile_total{controller="%s",result="success"} 1`,
		//    strings.ToLower(<Kind>),
		// ))
	})

	Context("NamespaceClass resources", func() {
		BeforeAll(func() {
			waitForNamespaceWebhook()
		})

		It("should do nothing when only a NamespaceClass is applied", func() {
			className := uniqueName("class-only")
			configMapName := uniqueName("class-only-config")
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifest(className, configMapName))
			Expect(err).NotTo(HaveOccurred())

			Consistently(func(g Gomega) {
				output, err := utils.Run(exec.Command(
					"kubectl", "get", "configmaps", "-A",
					"-l", fmt.Sprintf("namespaceclass.akuity.io/name=%s", className),
					"-o", "jsonpath={.items[*].metadata.name}",
				))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(output)).To(BeEmpty())
			}, 10*time.Second, time.Second).Should(Succeed())
		})

		It("should block Namespace creation when the referenced NamespaceClass does not exist", func() {
			nsName := uniqueName("missing-class")
			className := uniqueName("does-not-exist")
			DeferCleanup(deleteNamespace, nsName)

			output, err := applyYAML(namespaceManifest(nsName, className))
			Expect(err).To(HaveOccurred())
			Expect(output).To(ContainSubstring(fmt.Sprintf("NamespaceClass %q does not exist", className)))

			_, err = utils.Run(exec.Command("kubectl", "get", "namespace", nsName))
			Expect(err).To(HaveOccurred())
		})

		It("should create resources when a Namespace references an existing NamespaceClass", func() {
			className := uniqueName("matching-class")
			nsName := uniqueName("matching-ns")
			configMapName := uniqueName("matching-config")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifest(className, configMapName))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(nsName, className))
			Expect(err).NotTo(HaveOccurred())

			expectConfigMap(nsName, configMapName, className)
		})

		It("should apply resources to every Namespace that references a NamespaceClass", func() {
			className := uniqueName("multi-ns-class")
			firstNamespaceName := uniqueName("multi-ns-first")
			secondNamespaceName := uniqueName("multi-ns-second")
			configMapName := uniqueName("multi-ns-config")
			DeferCleanup(deleteNamespace, firstNamespaceName)
			DeferCleanup(deleteNamespace, secondNamespaceName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifest(className, configMapName))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(firstNamespaceName, className))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(secondNamespaceName, className))
			Expect(err).NotTo(HaveOccurred())

			expectConfigMap(firstNamespaceName, configMapName, className)
			expectConfigMap(secondNamespaceName, configMapName, className)
			expectManagedResourceInNamespace(className, firstNamespaceName, configMapName)
			expectManagedResourceInNamespace(className, secondNamespaceName, configMapName)
			expectManagedResourceCount(className, 2)
		})

		It("should create resources when an existing Namespace starts using a NamespaceClass", func() {
			className := uniqueName("late-class")
			nsName := uniqueName("late-ns")
			configMapName := uniqueName("late-config")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceManifest(nsName, ""))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceClassManifest(className, configMapName))
			Expect(err).NotTo(HaveOccurred())
			expectNoConfigMapConsistently(nsName, configMapName)

			_, err = utils.Run(exec.Command(
				"kubectl", "annotate", "namespace", nsName,
				fmt.Sprintf("namespaceclass.akuity.io/name=%s", className),
				"--overwrite",
			))
			Expect(err).NotTo(HaveOccurred())

			expectConfigMap(nsName, configMapName, className)
		})

		It("should remove managed resources from status when a Namespace is deleted", func() {
			className := uniqueName("deleted-ns-class")
			nsName := uniqueName("deleted-ns")
			configMapName := uniqueName("deleted-ns-config")
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifest(className, configMapName))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(nsName, className))
			Expect(err).NotTo(HaveOccurred())
			expectConfigMap(nsName, configMapName, className)
			expectManagedResourceInNamespace(className, nsName, configMapName)

			deleteNamespace(nsName)

			expectNoManagedResourceInNamespace(className, nsName, configMapName)
			expectManagedResourceCount(className, 0)
		})

		It("should delete managed resources when a NamespaceClass is deleted", func() {
			className := uniqueName("deleted-class")
			nsName := uniqueName("deleted-class-ns")
			configMapName := uniqueName("deleted-class-config")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifest(className, configMapName))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(nsName, className))
			Expect(err).NotTo(HaveOccurred())
			expectConfigMap(nsName, configMapName, className)
			expectManagedResourceInNamespace(className, nsName, configMapName)

			deleteNamespaceClass(className)

			expectNoConfigMap(nsName, configMapName)
			expectNoNamespaceClass(className)
		})

		It("should create non-ConfigMap namespaced resources", func() {
			className := uniqueName("secret-class")
			nsName := uniqueName("secret-ns")
			secretName := uniqueName("managed-secret")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifestWithResources(className, fmt.Sprintf(`  - apiVersion: v1
    kind: Secret
    metadata:
      name: %s
    type: Opaque
    data:
      key: dmFsdWU=
`, secretName)))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(nsName, className))
			Expect(err).NotTo(HaveOccurred())

			expectSecret(nsName, secretName, className, "dmFsdWU=")
			expectManagedResourceInNamespace(className, nsName, secretName)
		})

		It("should not apply any resource when a NamespaceClass contains invalid resources", func() {
			cases := []struct {
				name            string
				invalidResource string
			}{
				{
					name: "missing-api-version",
					invalidResource: `  - kind: ConfigMap
    metadata:
      name: invalid-config
    data:
      key: invalid
`,
				},
				{
					name: "missing-kind",
					invalidResource: `  - apiVersion: v1
    metadata:
      name: invalid-config
    data:
      key: invalid
`,
				},
				{
					name: "missing-name",
					invalidResource: `  - apiVersion: v1
    kind: ConfigMap
    data:
      key: invalid
`,
				},
				{
					name: "cluster-scoped",
					invalidResource: `  - apiVersion: v1
    kind: Namespace
    metadata:
      name: invalid-namespace
`,
				},
			}
			created := make([]struct {
				namespaceName string
				configMapName string
			}, 0, len(cases))

			for _, tc := range cases {
				className := uniqueName(tc.name)
				nsName := uniqueName(tc.name + "-ns")
				blockedConfigMapName := uniqueName(tc.name + "-blocked")
				DeferCleanup(deleteNamespace, nsName)
				DeferCleanup(deleteNamespaceClass, className)

				_, err := applyYAML(namespaceClassManifestWithResources(
					className,
					tc.invalidResource,
					configMapResourceManifest(configMapResource{
						name:  blockedConfigMapName,
						value: blockedConfigMapName,
					}),
				))
				Expect(err).NotTo(HaveOccurred())
				_, err = applyYAML(namespaceManifest(nsName, className))
				Expect(err).NotTo(HaveOccurred())

				created = append(created, struct {
					namespaceName string
					configMapName string
				}{namespaceName: nsName, configMapName: blockedConfigMapName})
			}

			Consistently(func(g Gomega) {
				for _, resource := range created {
					_, err := utils.Run(exec.Command("kubectl", "get", "configmap", resource.configMapName, "-n", resource.namespaceName))
					g.Expect(err).To(HaveOccurred())
				}
			}, 10*time.Second, time.Second).Should(Succeed())
		})

		It("should adopt and update pre-existing resources with the same name", func() {
			className := uniqueName("existing-resource-class")
			nsName := uniqueName("existing-resource-ns")
			configMapName := uniqueName("existing-resource-config")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceManifest(nsName, ""))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  existing: kept
  key: old-value
`, configMapName, nsName))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceClassManifest(className, configMapName))
			Expect(err).NotTo(HaveOccurred())

			_, err = utils.Run(exec.Command(
				"kubectl", "annotate", "namespace", nsName,
				fmt.Sprintf("namespaceclass.akuity.io/name=%s", className),
				"--overwrite",
			))
			Expect(err).NotTo(HaveOccurred())

			expectConfigMapWithExistingData(nsName, configMapName, className, "kept")
		})

		It("should preserve resource metadata when adding management metadata", func() {
			className := uniqueName("metadata-class")
			nsName := uniqueName("metadata-ns")
			configMapName := uniqueName("metadata-config")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifestWithResources(className, fmt.Sprintf(`  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      annotations:
        example.com/existing-annotation: kept
      labels:
        example.com/existing-label: kept
    data:
      key: %s
`, configMapName, configMapName)))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(nsName, className))
			Expect(err).NotTo(HaveOccurred())

			expectConfigMapMetadata(nsName, configMapName, className)
		})

		It("should create resources when a Namespace uses the NamespaceClass label", func() {
			className := uniqueName("label-class")
			nsName := uniqueName("label-ns")
			configMapName := uniqueName("label-config")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifest(className, configMapName))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifestWithClassLabel(nsName, className))
			Expect(err).NotTo(HaveOccurred())

			expectConfigMap(nsName, configMapName, className)
		})

		It("should create and delete resources when a NamespaceClass is updated", func() {
			className := uniqueName("update-class")
			nsName := uniqueName("update-ns")
			oldConfigMapName := uniqueName("old-config")
			newConfigMapName := uniqueName("new-config")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifest(className, oldConfigMapName))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(nsName, className))
			Expect(err).NotTo(HaveOccurred())
			expectConfigMap(nsName, oldConfigMapName, className)
			expectManagedResource(className, oldConfigMapName)

			_, err = applyYAML(namespaceClassManifest(className, newConfigMapName))
			Expect(err).NotTo(HaveOccurred())

			expectConfigMap(nsName, newConfigMapName, className)
			expectNoConfigMap(nsName, oldConfigMapName)
		})

		It("should create additional resources when a NamespaceClass gains a resource", func() {
			className := uniqueName("add-resource-class")
			nsName := uniqueName("add-resource-ns")
			existingConfigMapName := uniqueName("existing-config")
			addedConfigMapName := uniqueName("added-config")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifest(className, existingConfigMapName))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(nsName, className))
			Expect(err).NotTo(HaveOccurred())
			expectConfigMap(nsName, existingConfigMapName, className)

			_, err = applyYAML(namespaceClassManifest(className, existingConfigMapName, addedConfigMapName))
			Expect(err).NotTo(HaveOccurred())

			expectConfigMap(nsName, existingConfigMapName, className)
			expectConfigMap(nsName, addedConfigMapName, className)
			expectManagedResource(className, addedConfigMapName)
		})

		It("should delete resources when a NamespaceClass loses a resource", func() {
			className := uniqueName("remove-resource-class")
			nsName := uniqueName("remove-resource-ns")
			keptConfigMapName := uniqueName("kept-config")
			removedConfigMapName := uniqueName("removed-config")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifest(className, keptConfigMapName, removedConfigMapName))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(nsName, className))
			Expect(err).NotTo(HaveOccurred())
			expectConfigMap(nsName, keptConfigMapName, className)
			expectConfigMap(nsName, removedConfigMapName, className)
			expectManagedResource(className, removedConfigMapName)

			_, err = applyYAML(namespaceClassManifest(className, keptConfigMapName))
			Expect(err).NotTo(HaveOccurred())

			expectConfigMap(nsName, keptConfigMapName, className)
			expectNoConfigMap(nsName, removedConfigMapName)
		})

		It("should update resources when a NamespaceClass resource changes", func() {
			className := uniqueName("update-resource-class")
			nsName := uniqueName("update-resource-ns")
			configMapName := uniqueName("updated-config")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifestWithConfigMaps(className, configMapResource{
				name:  configMapName,
				value: "old-value",
			}))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(nsName, className))
			Expect(err).NotTo(HaveOccurred())
			expectConfigMapData(nsName, configMapName, className, "old-value")

			_, err = applyYAML(namespaceClassManifestWithConfigMaps(className, configMapResource{
				name:  configMapName,
				value: "new-value",
			}))
			Expect(err).NotTo(HaveOccurred())

			expectConfigMapData(nsName, configMapName, className, "new-value")
		})

		It("should retain resources and remove them from status when a Namespace class annotation is removed", func() {
			className := uniqueName("retain-class")
			nsName := uniqueName("retain-ns")
			configMapName := uniqueName("retain-config")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifestWithRemovalPolicy(className, "Retain", configMapName))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(nsName, className))
			Expect(err).NotTo(HaveOccurred())
			expectConfigMap(nsName, configMapName, className)
			expectManagedResourceInNamespace(className, nsName, configMapName)

			_, err = utils.Run(exec.Command(
				"kubectl", "annotate", "namespace", nsName,
				"namespaceclass.akuity.io/name-",
				"--overwrite",
			))
			Expect(err).NotTo(HaveOccurred())

			Consistently(func(g Gomega) {
				_, err := utils.Run(exec.Command("kubectl", "get", "configmap", configMapName, "-n", nsName))
				g.Expect(err).NotTo(HaveOccurred())
			}, 10*time.Second, time.Second).Should(Succeed())
			expectNoManagedResourceInNamespace(className, nsName, configMapName)
			expectManagedResourceCount(className, 0)
		})

		It("should delete resources and remove them from status when a Namespace class annotation is removed", func() {
			className := uniqueName("delete-class")
			nsName := uniqueName("delete-ns")
			configMapName := uniqueName("delete-config")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, className)

			_, err := applyYAML(namespaceClassManifestWithRemovalPolicy(className, "Delete", configMapName))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(nsName, className))
			Expect(err).NotTo(HaveOccurred())
			expectConfigMap(nsName, configMapName, className)
			expectManagedResourceInNamespace(className, nsName, configMapName)

			_, err = utils.Run(exec.Command(
				"kubectl", "annotate", "namespace", nsName,
				"namespaceclass.akuity.io/name-",
				"--overwrite",
			))
			Expect(err).NotTo(HaveOccurred())

			expectNoConfigMap(nsName, configMapName)
			expectNoManagedResourceInNamespace(className, nsName, configMapName)
			expectManagedResourceCount(className, 0)
		})

		It("should replace resources when a Namespace changes to a different NamespaceClass", func() {
			oldClassName := uniqueName("old-class")
			newClassName := uniqueName("new-class")
			nsName := uniqueName("change-ns")
			oldConfigMapName := uniqueName("old-config")
			newConfigMapName := uniqueName("new-config")
			DeferCleanup(deleteNamespace, nsName)
			DeferCleanup(deleteNamespaceClass, oldClassName)
			DeferCleanup(deleteNamespaceClass, newClassName)

			_, err := applyYAML(namespaceClassManifest(oldClassName, oldConfigMapName))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceClassManifest(newClassName, newConfigMapName))
			Expect(err).NotTo(HaveOccurred())
			_, err = applyYAML(namespaceManifest(nsName, oldClassName))
			Expect(err).NotTo(HaveOccurred())
			expectConfigMap(nsName, oldConfigMapName, oldClassName)
			expectManagedResource(oldClassName, oldConfigMapName)

			_, err = utils.Run(exec.Command(
				"kubectl", "annotate", "namespace", nsName,
				fmt.Sprintf("namespaceclass.akuity.io/name=%s", newClassName),
				"--overwrite",
			))
			Expect(err).NotTo(HaveOccurred())

			expectConfigMap(nsName, newConfigMapName, newClassName)
			expectNoConfigMap(nsName, oldConfigMapName)
		})
	})
})

func waitForNamespaceWebhook() {
	nsName := uniqueName("webhook-ready")
	DeferCleanup(deleteNamespace, nsName)

	Eventually(func(g Gomega) {
		_, err := applyYAML(namespaceManifest(nsName, ""))
		g.Expect(err).NotTo(HaveOccurred())
	}, 2*time.Minute, time.Second).Should(Succeed())
}

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func applyYAML(manifest string) (string, error) {
	file, err := os.CreateTemp("", "nsclass-e2e-*.yaml")
	if err != nil {
		return "", err
	}
	defer func() {
		_ = os.Remove(file.Name())
	}()

	if _, err := file.WriteString(manifest); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}

	return utils.Run(exec.Command("kubectl", "apply", "-f", file.Name()))
}

type configMapResource struct {
	name  string
	value string
}

func namespaceClassManifest(name string, configMapNames ...string) string {
	return namespaceClassManifestWithRemovalPolicy(name, "", configMapNames...)
}

func namespaceClassManifestWithRemovalPolicy(name, removalPolicy string, configMapNames ...string) string {
	configMaps := make([]configMapResource, 0, len(configMapNames))
	for _, configMapName := range configMapNames {
		configMaps = append(configMaps, configMapResource{
			name:  configMapName,
			value: configMapName,
		})
	}

	return namespaceClassManifestWithRemovalPolicyAndConfigMaps(name, removalPolicy, configMaps...)
}

func namespaceClassManifestWithConfigMaps(name string, configMaps ...configMapResource) string {
	return namespaceClassManifestWithRemovalPolicyAndConfigMaps(name, "", configMaps...)
}

func namespaceClassManifestWithRemovalPolicyAndConfigMaps(
	name string,
	removalPolicy string,
	configMaps ...configMapResource,
) string {
	resources := make([]string, 0, len(configMaps))
	for _, configMap := range configMaps {
		resources = append(resources, configMapResourceManifest(configMap))
	}

	return namespaceClassManifestWithRemovalPolicyAndResources(name, removalPolicy, resources...)
}

func configMapResourceManifest(configMap configMapResource) string {
	return fmt.Sprintf(`  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
    data:
      key: %s
`, configMap.name, configMap.value)
}

func namespaceClassManifestWithResources(name string, resourceManifests ...string) string {
	return namespaceClassManifestWithRemovalPolicyAndResources(name, "", resourceManifests...)
}

func namespaceClassManifestWithRemovalPolicyAndResources(
	name string,
	removalPolicy string,
	resourceManifests ...string,
) string {
	resources := " []\n"
	if len(resourceManifests) > 0 {
		var builder strings.Builder
		builder.WriteString("\n")
		for _, resourceManifest := range resourceManifests {
			builder.WriteString(resourceManifest)
		}
		resources = builder.String()
	}

	policy := ""
	if removalPolicy != "" {
		policy = fmt.Sprintf("  removalPolicy: %s\n", removalPolicy)
	}

	return fmt.Sprintf(`apiVersion: nsclass.nsclass.laverya.com/v1alpha1
kind: NamespaceClass
metadata:
  name: %s
spec:
%s  resources:%s`, name, policy, resources)
}

func namespaceManifest(name, className string) string {
	annotations := ""
	if className != "" {
		annotations = fmt.Sprintf(`  annotations:
    namespaceclass.akuity.io/name: %s
`, className)
	}

	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
%s`, name, annotations)
}

func namespaceManifestWithClassLabel(name, className string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
  labels:
    namespaceclass.akuity.io/name: %s
`, name, className)
}

func expectConfigMap(namespaceName, configMapName, className string) {
	expectConfigMapData(namespaceName, configMapName, className, configMapName)
}

func expectConfigMapData(namespaceName, configMapName, className, value string) {
	Eventually(func(g Gomega) {
		cmd := exec.Command(
			"kubectl", "get", "configmap", configMapName,
			"-n", namespaceName,
			"-o", `go-template={{ index .metadata.annotations "namespaceclass.akuity.io/name" }}{{ "\n" }}{{ index .metadata.annotations "namespaceclass.akuity.io/managed-by" }}{{ "\n" }}{{ index .data "key" }}`,
		)
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.TrimSpace(output)).To(Equal(fmt.Sprintf("%s\nnsclass-controller\n%s", className, value)))
	}, 2*time.Minute, time.Second).Should(Succeed())
}

func expectConfigMapWithExistingData(namespaceName, configMapName, className, existingValue string) {
	Eventually(func(g Gomega) {
		cmd := exec.Command(
			"kubectl", "get", "configmap", configMapName,
			"-n", namespaceName,
			"-o", `go-template={{ index .metadata.annotations "namespaceclass.akuity.io/name" }}{{ "\n" }}{{ index .metadata.annotations "namespaceclass.akuity.io/managed-by" }}{{ "\n" }}{{ index .data "key" }}{{ "\n" }}{{ index .data "existing" }}`,
		)
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.TrimSpace(output)).To(Equal(fmt.Sprintf("%s\nnsclass-controller\n%s\n%s", className, configMapName, existingValue)))
	}, 2*time.Minute, time.Second).Should(Succeed())
}

func expectConfigMapMetadata(namespaceName, configMapName, className string) {
	Eventually(func(g Gomega) {
		cmd := exec.Command(
			"kubectl", "get", "configmap", configMapName,
			"-n", namespaceName,
			"-o", `go-template={{ index .metadata.annotations "namespaceclass.akuity.io/name" }}{{ "\n" }}{{ index .metadata.annotations "namespaceclass.akuity.io/managed-by" }}{{ "\n" }}{{ index .metadata.annotations "example.com/existing-annotation" }}{{ "\n" }}{{ index .metadata.labels "namespaceclass.akuity.io/name" }}{{ "\n" }}{{ index .metadata.labels "namespaceclass.akuity.io/managed-by" }}{{ "\n" }}{{ index .metadata.labels "example.com/existing-label" }}`,
		)
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.TrimSpace(output)).To(Equal(fmt.Sprintf("%s\nnsclass-controller\nkept\n%s\nnsclass-controller\nkept", className, className)))
	}, 2*time.Minute, time.Second).Should(Succeed())
}

func expectSecret(namespaceName, secretName, className, value string) {
	Eventually(func(g Gomega) {
		cmd := exec.Command(
			"kubectl", "get", "secret", secretName,
			"-n", namespaceName,
			"-o", `go-template={{ index .metadata.annotations "namespaceclass.akuity.io/name" }}{{ "\n" }}{{ index .metadata.annotations "namespaceclass.akuity.io/managed-by" }}{{ "\n" }}{{ index .data "key" }}`,
		)
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.TrimSpace(output)).To(Equal(fmt.Sprintf("%s\nnsclass-controller\n%s", className, value)))
	}, 2*time.Minute, time.Second).Should(Succeed())
}

func expectNoConfigMap(namespaceName, configMapName string) {
	Eventually(func(g Gomega) {
		_, err := utils.Run(exec.Command("kubectl", "get", "configmap", configMapName, "-n", namespaceName))
		g.Expect(err).To(HaveOccurred())
	}, 2*time.Minute, time.Second).Should(Succeed())
}

func expectNoConfigMapConsistently(namespaceName, configMapName string) {
	Consistently(func(g Gomega) {
		_, err := utils.Run(exec.Command("kubectl", "get", "configmap", configMapName, "-n", namespaceName))
		g.Expect(err).To(HaveOccurred())
	}, 10*time.Second, time.Second).Should(Succeed())
}

func expectNoNamespaceClass(className string) {
	Eventually(func(g Gomega) {
		_, err := utils.Run(exec.Command("kubectl", "get", "namespaceclass", className))
		g.Expect(err).To(HaveOccurred())
	}, 2*time.Minute, time.Second).Should(Succeed())
}

func expectManagedResource(className, resourceName string) {
	Eventually(func(g Gomega) {
		cmd := exec.Command(
			"kubectl", "get", "namespaceclass", className,
			"-o", "jsonpath={.status.managedResources[*].name}",
		)
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.Fields(output)).To(ContainElement(resourceName))
	}, 2*time.Minute, time.Second).Should(Succeed())
}

func expectManagedResourceInNamespace(className, namespaceName, resourceName string) {
	Eventually(func(g Gomega) {
		output, err := managedResourceRefs(className)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.Fields(output)).To(ContainElement(fmt.Sprintf("%s/%s", namespaceName, resourceName)))
	}, 2*time.Minute, time.Second).Should(Succeed())
}

func expectNoManagedResourceInNamespace(className, namespaceName, resourceName string) {
	Eventually(func(g Gomega) {
		output, err := managedResourceRefs(className)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.Fields(output)).NotTo(ContainElement(fmt.Sprintf("%s/%s", namespaceName, resourceName)))
	}, 2*time.Minute, time.Second).Should(Succeed())
}

func expectManagedResourceCount(className string, count int) {
	Eventually(func(g Gomega) {
		output, err := managedResourceRefs(className)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.Fields(output)).To(HaveLen(count))
	}, 2*time.Minute, time.Second).Should(Succeed())
}

func managedResourceRefs(className string) (string, error) {
	cmd := exec.Command(
		"kubectl", "get", "namespaceclass", className,
		"-o", `jsonpath={range .status.managedResources[*]}{.namespace}/{.name}{"\n"}{end}`,
	)
	return utils.Run(cmd)
}

func deleteNamespace(name string) {
	_, _ = utils.Run(exec.Command("kubectl", "delete", "namespace", name, "--ignore-not-found"))
}

func deleteNamespaceClass(name string) {
	_, _ = utils.Run(exec.Command("kubectl", "delete", "namespaceclass", name, "--ignore-not-found"))
}

func deleteAllNamespaceClasses() {
	_, _ = utils.Run(exec.Command("kubectl", "delete", "namespaceclass", "--all", "--ignore-not-found"))

	Eventually(func(g Gomega) {
		output, err := utils.Run(exec.Command("kubectl", "get", "namespaceclass", "-o", "name"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.TrimSpace(output)).To(BeEmpty())
	}, 2*time.Minute, time.Second).Should(Succeed())
}

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	By("creating temporary file to store the token request")
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		By("executing kubectl command to create the token")
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		By("parsing the JSON output to extract the token")
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
