# nsclass-controller

`nsclass-controller` applies a shared set of namespace-scoped Kubernetes resources to every Namespace that opts in to a `NamespaceClass`.

Use it when clusters need repeatable namespace defaults, such as baseline ConfigMaps, ResourceQuotas, LimitRanges, Secrets, Roles, or other namespaced objects. A platform team defines a cluster-scoped `NamespaceClass`; namespace owners select it with the `namespaceclass.akuity.io/name` label or annotation on their Namespace.

## How It Works

1. Create a cluster-scoped `NamespaceClass`.
2. Add `namespaceclass.akuity.io/name: <class-name>` to a Namespace label or annotation.
3. The controller server-side applies every object in `spec.resources` into that Namespace.
4. Managed objects receive these labels and annotations:

```yaml
namespaceclass.akuity.io/name: <class-name>
namespaceclass.akuity.io/managed-by: nsclass-controller
```

If both a label and annotation are present on a Namespace, the label value is used.

The validating webhook rejects Namespace create and update requests that reference a missing `NamespaceClass`. Namespaces without a class reference are allowed.

## API

```yaml
apiVersion: nsclass.nsclass.laverya.com/v1alpha1
kind: NamespaceClass
metadata:
  name: baseline
spec:
  removalPolicy: Delete
  resources:
    - apiVersion: v1
      kind: ConfigMap
      metadata:
        name: namespace-defaults
      data:
        profile: baseline
```

`spec.resources` is a list of Kubernetes objects to apply into matching Namespaces.

Each resource must:

- Include `apiVersion`, `kind`, and `metadata.name`
- Be namespace-scoped
- Omit `metadata.namespace`, or accept that the controller overwrites it with the selected Namespace

`spec.removalPolicy` controls what happens when an existing Namespace stops referencing a class:

- `Retain` leaves managed resources in the Namespace and removes them from `status.managedResources`
- `Delete` deletes managed resources and removes them from `status.managedResources`

The default is `Retain`.

Deleting a `NamespaceClass` deletes the resources it still tracks in status. Updating a `NamespaceClass` applies new resources, updates changed resources, and deletes resources removed from the class. If any resource in `spec.resources` is invalid, reconciliation fails before applying the class resources.

## Quick Start

Install cert-manager before deploying the default overlay. The default kustomize configuration enables the validating webhook and uses cert-manager to issue the webhook serving certificate.

Build and push the controller image:

```sh
make docker-build docker-push IMG=<registry>/nsclass-controller:<tag>
```

Deploy the controller:

```sh
make deploy IMG=<registry>/nsclass-controller:<tag>
```

Apply the sample `NamespaceClass`:

```sh
kubectl apply -k config/samples/
```

Apply a Namespace that selects the sample by label:

```sh
kubectl apply -f config/samples/namespace_label.yaml
```

Or apply a Namespace that selects the sample by annotation:

```sh
kubectl apply -f config/samples/namespace_annotation.yaml
```

Verify that the sample resources were created:

```sh
kubectl get configmap namespace-defaults -n nsclass-sample-label -o yaml
kubectl get resourcequota namespace-quota -n nsclass-sample-label -o yaml
kubectl get namespaceclass namespaceclass-sample \
  -o jsonpath='{range .status.managedResources[*]}{.namespace}/{.kind}/{.name}{"\n"}{end}'
```

## Local Development

Run unit tests:

```sh
make test
```

Run lint with automatic fixes:

```sh
make lint-fix
```

Run e2e tests against an isolated Kind cluster:

```sh
make test-e2e
```

The e2e target builds the controller image, loads it into Kind, deploys the controller, and validates webhook and reconciliation behavior. It is intended for an isolated test cluster, not a shared development or production cluster.

## Uninstall

Delete sample resources:

```sh
kubectl delete -f config/samples/namespace_label.yaml --ignore-not-found
kubectl delete -f config/samples/namespace_annotation.yaml --ignore-not-found
kubectl delete -k config/samples/ --ignore-not-found
```

Remove the controller:

```sh
make undeploy
```

Remove the CRD:

```sh
make uninstall
```

## Distribution

Generate a single-install YAML bundle:

```sh
make build-installer IMG=<registry>/nsclass-controller:<tag>
```

The generated bundle is written to `dist/install.yaml`.

Install from a published bundle:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/nsclass-controller/<tag>/dist/install.yaml
```

Generate a Helm chart with the Kubebuilder Helm plugin:

```sh
kubebuilder edit --plugins=helm/v2-alpha
```

If webhooks or manifests change after chart generation, regenerate the chart with `--force` and manually restore any chart customizations.

## License

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
