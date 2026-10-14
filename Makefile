.PHONY: api-generate walkthrough library-demo general-demo consumer-test test check verify render serve build clean

# Standard controller-gen handles Kubernetes deepcopy methods for the sample API.
api-generate:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.20.1 object paths=./examples/database/api/v1alpha1

# Sample controller: extract the process, watches and dynamic-client accesses from
# Go, generate the model and diagrams, then replay five schedules without Quint.
walkthrough:
	go run ./cmd/walkthrough

# Unrelated controller using only the public library; handwritten behaviour model.
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
	go build -o bin/walkthrough ./cmd/walkthrough
	go build -o bin/library-demo ./cmd/library-demo
	go build -o bin/general-demo ./cmd/general-demo

# Preserve source, generated models, trace fixtures and YAML evidence receipts.
clean:
	rm -rf -- bin site
