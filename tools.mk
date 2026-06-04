LOCALBIN ?= $(shell pwd)/bin
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

KUBEBUILDER_VERSION ?= v4.14.0
CONTROLLER_TOOLS_VERSION ?= v0.21.0
KUSTOMIZE_VERSION ?= v5.8.1

KUBEBUILDER ?= $(LOCALBIN)/kubebuilder-$(KUBEBUILDER_VERSION)
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen-$(CONTROLLER_TOOLS_VERSION)
KUSTOMIZE ?= $(LOCALBIN)/kustomize-$(KUSTOMIZE_VERSION)

.PHONY: install-tools
install-tools: $(KUBEBUILDER) $(CONTROLLER_GEN) $(KUSTOMIZE)
	ln -sf kubebuilder-$(KUBEBUILDER_VERSION) $(LOCALBIN)/kubebuilder
	ln -sf controller-gen-$(CONTROLLER_TOOLS_VERSION) $(LOCALBIN)/controller-gen
	ln -sf kustomize-$(KUSTOMIZE_VERSION) $(LOCALBIN)/kustomize

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

$(KUBEBUILDER): $(LOCALBIN)
	curl -fsSL -o $(KUBEBUILDER) "https://github.com/kubernetes-sigs/kubebuilder/releases/download/$(KUBEBUILDER_VERSION)/kubebuilder_$(GOOS)_$(GOARCH)"
	chmod +x $(KUBEBUILDER)

$(CONTROLLER_GEN): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)
	mv -f $(LOCALBIN)/controller-gen $(CONTROLLER_GEN)

$(KUSTOMIZE): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION)
	mv -f $(LOCALBIN)/kustomize $(KUSTOMIZE)
