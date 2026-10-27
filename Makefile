.PHONY: api-generate walkthrough library-demo general-demo consumer-test test check verify render serve build clean envtest arch-pipeline

# Standard controller-gen handles Kubernetes deepcopy methods for the example APIs
# and the CRD manifests of the reference system.
CONTROLLER_GEN = go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.20.1
api-generate:
	$(CONTROLLER_GEN) object paths=./examples/database/api/v1alpha1
	$(CONTROLLER_GEN) object paths=./examples/refsystem/api/v1alpha1
	$(CONTROLLER_GEN) crd paths=./examples/refsystem/api/... output:crd:dir=./examples/refsystem/config/crd

# Sample controller: extract the process, watches and dynamic-client accesses from
# Go, generate the model and diagrams, then replay five schedules without Quint.
walkthrough:
	go run ./cmd/walkthrough

# Unrelated controller using only the public library: generate its model, sample
# 40 traces with Quint, replay them, run its mutation suite and write its receipt.
library-demo:
	go run ./cmd/library-demo --refresh

# Reusable multi-controller runtime model checked against real controller-runtime queues.
general-demo:
	go run ./cmd/general-demo

# Prove packages can be consumed without Go's internal-package privileges.
consumer-test:
	cd testdata/consumer && GOWORK=off go test -mod=readonly ./...

test:
	go test ./...

# Envtest: install kube-apiserver and etcd with setup-envtest from the
# controller-runtime release branch in go.mod, for the Kubernetes minor of
# k8s.io/api, then run every Envtest test. ENVTEST_REQUIRED makes a test
# fail rather than skip without assets, and the target fails when no
# Envtest test passed or any skipped.
ENVTEST_VERSION = $(shell go list -m -f '{{.Version}}' sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $$2, $$3}')
ENVTEST_K8S_VERSION = $(shell go list -m -f '{{.Version}}' k8s.io/api | awk -F'[v.]' '{printf "1.%d.x", $$3}')
ENVTEST_DIR ?= bin/envtest
envtest:
	@set -eu; \
	assets="$$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION) use $(ENVTEST_K8S_VERSION) --bin-dir $(ENVTEST_DIR) -p path)"; \
	test -n "$$assets" || { echo "setup-envtest printed no asset path" >&2; exit 1; }; \
	log="$$(mktemp)"; \
	KUBEBUILDER_ASSETS="$$(cd "$$assets" && pwd)" ENVTEST_REQUIRED=1 go test -p 2 -count=1 -v -run Envtest ./... > "$$log" 2>&1 || { tail -50 "$$log"; exit 1; }; \
	grep -E -e '--- (PASS|SKIP|FAIL): .*Envtest' "$$log"; \
	if grep -q -e '--- SKIP' "$$log"; then echo "an envtest test skipped" >&2; exit 1; fi; \
	grep -q -e '--- PASS: .*Envtest' "$$log" || { echo "no envtest test ran" >&2; exit 1; }

# Run the script steps of the adopter pipeline (.github/workflows/arch.yml)
# on ConfigMirror, as CI's architecture job does, into a temporary
# directory. Requires Quint and LikeC4 (QUINT_BIN, LIKEC4_BIN). Set
# ARCH_PIPELINE_BASE=main to diff the IR against a branch.
arch-pipeline:
	ARCH_PIPELINE=1 go test -count=1 -v -run TestArchPipeline ./hack/ci

# Native Go only: replay checked-in, hash-verified evidence.
check: test walkthrough

# Refresh all evidence before testing source-hash receipts. Requires Quint 0.33.x (QUINT_BIN).
verify:
	go run ./cmd/walkthrough --refresh
	go run ./cmd/general-demo
	go run ./cmd/library-demo --refresh
	go test ./...
	$(MAKE) consumer-test

# Build the sample controller's diagrams into site/. Requires LikeC4 1.59.x (LIKEC4_BIN).
LIKEC4 ?= $(or $(LIKEC4_BIN),likec4)
render:
	$(LIKEC4) build examples/database/generated -o site --base ./ --output-single-file

serve:
	$(LIKEC4) start examples/database/generated

build:
	go build -o bin/arch ./cmd/arch
	go build -o bin/walkthrough ./cmd/walkthrough
	go build -o bin/library-demo ./cmd/library-demo
	go build -o bin/general-demo ./cmd/general-demo

# Preserve source, generated models, trace fixtures and YAML evidence receipts.
clean:
	rm -rf -- bin site
