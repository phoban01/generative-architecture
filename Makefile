.PHONY: library-demo consumer-test composition-demo clean runtime-demo behavior-demo api-generate extract generate test check controller-demo verify render serve build

# Standard controller-gen handles Kubernetes deepcopy methods.
api-generate:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.20.1 object paths=./examples/machinevendor/api/v1alpha1

extract:
	go run ./cmd/arch extract

generate:
	go run ./cmd/arch generate
	go run ./cmd/composition-demo --generate-only
	go run ./cmd/library-demo --generate-only

test:
	go test ./...

# Native Go only: replay checked-in, hash-verified evidence.
check: test
	go run ./cmd/arch check --replay

# Native Go: generated dispatch -> handwritten adapter -> MAC reconcile phases.
controller-demo:
	go run ./cmd/controller-demo

# Extract actual publication behavior and check independent requirements.
behavior-demo:
	go run ./cmd/behavior-demo

# Refresh all evidence before testing source-hash receipts. Requires Quint or QUINT_BIN.
verify: generate
	go run ./cmd/walkthrough --refresh
	go run ./cmd/general-demo
	go run ./cmd/arch check
	go run ./cmd/controller-demo
	go run ./cmd/behavior-demo
	go run ./cmd/runtime-demo
	go run ./cmd/composition-demo
	go run ./cmd/library-demo --refresh
	go test ./...
	$(MAKE) consumer-test

# Requires an installed LikeC4 CLI or LIKEC4_BIN wrapper.
render:
	go run ./cmd/arch render

serve:
	go run ./cmd/arch serve

build:
	go build -o bin/arch ./cmd/arch
	go build -o bin/controller-demo ./cmd/controller-demo
	go build -o bin/behavior-demo ./cmd/behavior-demo
	go build -o bin/runtime-demo ./cmd/runtime-demo
	go build -o bin/composition-demo ./cmd/composition-demo
	go build -o bin/library-demo ./cmd/library-demo

# Real worker/queue conformance with a declared runtime profile.
runtime-demo:
	go run ./cmd/runtime-demo

# Preserve source, generated models, trace fixtures and YAML evidence receipts.
clean:
	rm -rf -- bin site
	find reports -type f -name '*.txt' -delete

# Extracted process steps composed with runtime scheduling; paused Go replay.
composition-demo:
	go run ./cmd/composition-demo

# Unrelated controller using only the public library; refresh bounded Quint traces.
library-demo:
	go run ./cmd/library-demo --refresh

# Prove packages can be consumed without Go's internal-package privileges.
consumer-test:
	cd testdata/consumer && GOWORK=off go test -mod=readonly ./...

# First explanation: source-derived behaviour and independent constraints.
walkthrough:
	go run ./cmd/walkthrough
