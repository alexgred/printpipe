# printpipe needs only Go on the host. Builds are static (no cgo) for each
# target OS.

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
TARGETS := windows/amd64 darwin/amd64 darwin/arm64 linux/amd64
LDFLAGS := -s -w -X github.com/alexgred/printpipe/internal/cli.Version=$(VERSION)

.DEFAULT_GOAL := help

.PHONY: help
help: ## List the targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'

.PHONY: build
build: ## Build printpipe for every target into dist/
	@rm -rf dist
	@set -e; for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="$(LDFLAGS)" \
			-o dist/printpipe-$$os-$$arch$$ext . ; \
		echo dist/printpipe-$$os-$$arch$$ext; \
	done

# rsrc is only run here, to make the resource the linker embeds in the Windows
# binary; it is no dependency of printpipe.
.PHONY: manifest
manifest: ## Rebuild the Windows resource from printpipe.exe.manifest
	go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -manifest printpipe.exe.manifest -o rsrc_windows_amd64.syso

.PHONY: test
test: ## Run the tests with -race (ARGS="-run X ./internal/cli")
	go test -race $(or $(ARGS),./...)

# go vet for every target OS: the printer code differs per OS.
.PHONY: lint
lint: ## go vet for every target OS, and gofmt
	@set -e; for os in linux windows darwin; do GOOS=$$os go vet ./...; done
	@out=$$(gofmt -l .); [ -z "$$out" ] || { echo "$$out"; echo "run make fmt"; exit 1; }

.PHONY: fmt
fmt: ## Format the code
	gofmt -w .

.PHONY: dev
dev: ## Run the agent in dry run with dev.json: each job lands in dry-run.tspl
	go run . serve --config dev.json --dry-run dry-run.tspl
