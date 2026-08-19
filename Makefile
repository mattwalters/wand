BIN     := bin/wand
PKG     := github.com/mattwalters/wand
# Resolve the install dir the way the go tool does: GOBIN when set, else
# GOPATH/bin. Reading only GOPATH/bin would put `make install` and
# `make install-release` in different directories on a machine that sets GOBIN
# — and `make uninstall` would then delete neither of them.
GOBIN   := $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN   := $(shell go env GOPATH)/bin
endif

# Local builds stamp themselves from git, so `wand version` names the commit
# you are running and whether the tree was dirty. --match keeps `git describe`
# off the moving major tag release.yml pushes (v0 -> the latest v0.x.y);
# without it a local build reports "v0-5-g22923b1", which reads like a release
# of v0 rather than five commits past v0.1.0.
VERSION ?= $(shell git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS = -X $(PKG)/internal/cli.Version=$(VERSION) \
          -X $(PKG)/internal/cli.Commit=$(COMMIT) \
          -X $(PKG)/internal/cli.Date=$(DATE)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-z0-9-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the wand binary into bin/
	go build -ldflags "$(LDFLAGS)" -o $(BIN) .

# The dev build deliberately owns the name `wand`: every repo's PreToolUse
# hook shells out to a bare `wand guard`, so a build that does not own the
# name is one whose guard is never dogfooded. That also means a broken build
# breaks Linear writes in every repo, not just this one — hence the smoke
# below, and `make uninstall` as the one-command way back to what ships.
.PHONY: install
install: build ## Build, smoke, then put wand on your PATH (needs Go's bin dir on PATH)
	@$(MAKE) --no-print-directory smoke
	@mkdir -p $(GOBIN)
	install -m 0755 $(BIN) $(GOBIN)/wand
	@printf 'Installed %s to %s\n' '$(VERSION)' '$(GOBIN)/wand'
	@printf 'PATH resolves wand to: %s\n' "$$(command -v wand || echo '(nothing — is $(GOBIN) on your PATH?)')"

.PHONY: uninstall
uninstall: ## Remove the dev binary, so the released wand takes over again
	rm -f $(GOBIN)/wand
	@printf 'Removed %s\n' '$(GOBIN)/wand'
	@printf 'PATH resolves wand to: %s\n' "$$(command -v wand || echo '(nothing — brew install mattwalters/wand/wand)')"

# Built from source at the tag, so it is not byte-identical to the release
# artifact — same code, different toolchain and no -s -w. To exercise what
# users actually get, install the cask and run it by absolute path.
.PHONY: install-release
install-release: ## Install a published release, e.g. make install-release VERSION=v0.1.0
	@echo '$(VERSION)' | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "VERSION must name a released tag, like v1.2.3 (got '$(VERSION)')"; exit 1; }
	@sha=$$(git rev-list -n 1 '$(VERSION)' 2>/dev/null | cut -c1-7); \
	 go install -ldflags "-X $(PKG)/internal/cli.Version=$(VERSION) -X $(PKG)/internal/cli.Commit=$${sha:-none} -X $(PKG)/internal/cli.Date=$(DATE)" '$(PKG)@$(VERSION)'
	@$(MAKE) --no-print-directory smoke BIN='$(GOBIN)/wand'
	@printf 'Installed %s to %s (built from source at the tag)\n' '$(VERSION)' '$(GOBIN)/wand'

# Smokes any wand binary, not just a freshly built one — point it at the cask
# copy to check a release: make smoke BIN=/opt/homebrew/bin/wand
.PHONY: smoke
smoke: ## Check that a wand binary answers, e.g. make smoke BIN=bin/wand
	@test -x '$(BIN)' || { echo "smoke: no binary at $(BIN) — run make build"; exit 1; }
	@'$(BIN)' version >/dev/null || { echo "smoke: '$(BIN) version' failed"; exit 1; }
	@printf '%s' '{"tool_name":"mcp__smoke__save_issue","tool_input":{"id":"WND-1","state":"Todo"}}' \
	  | '$(BIN)' guard >/dev/null 2>&1; \
	  test $$? -eq 2 || { echo "smoke: guard did not block a promotion to Todo — refusing to install"; exit 1; }
	@printf 'smoke: %s answers, and its guard still blocks\n' '$(BIN)'

.PHONY: run
run: ## Run the TUI from source
	@go run . ui

.PHONY: test
test: ## Run the fast suite (tiers 0-2)
	go test ./...

.PHONY: test-e2e
test-e2e: ## Run the pty smoke test (tier 3)
	go test -tags e2e ./e2e/...

.PHONY: test-conformance
test-conformance: ## Prove worker isolation against the real harness (spends a model call)
	go test -tags conformance -v -run Isolation ./internal/worker/...

.PHONY: update-goldens
update-goldens: ## Regenerate golden screens, then READ THE DIFF
	go test ./... -update
	@echo
	@echo "Goldens regenerated. Review the diff before committing:"
	@echo "    git diff -- '*/testdata/screens/*.txt'"

.PHONY: screen
screen: ## Print a screen, e.g. make screen SCRIPT=j,enter
	@go run . ui --script "$(SCRIPT)" --dump-screen

.PHONY: docs
docs: ## Build the docs site into docs/public (needs hugo)
	@command -v hugo >/dev/null || { echo "hugo not found — brew install hugo"; exit 1; }
	hugo --source docs

.PHONY: docs-serve
docs-serve: ## Serve the docs site at http://localhost:1313/ with live reload (needs hugo)
	@command -v hugo >/dev/null || { echo "hugo not found — brew install hugo"; exit 1; }
	hugo server --source docs --baseURL http://localhost:1313/

.PHONY: fmt
fmt: ## Format all Go source
	gofmt -w .

.PHONY: vet
vet: ## Run go vet over everything, including tagged files
	go vet ./...
	go vet -tags e2e ./e2e/...
	go vet -tags conformance ./internal/worker/...

.PHONY: check
check: ## What CI runs
	@test -z "$$(gofmt -l . )" || { echo "unformatted files:"; gofmt -l .; exit 1; }
	$(MAKE) vet
	$(MAKE) test

.PHONY: release
release: ## Tag and push a release (a human act), e.g. make release VERSION=v0.1.0
	@echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || { echo "VERSION must look like v1.2.3 (got '$(VERSION)')"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "working tree not clean"; exit 1; }
	@test "$$(git branch --show-current)" = "main" || { echo "release from main (currently on $$(git branch --show-current))"; exit 1; }
	git tag $(VERSION)
	git push origin $(VERSION)

.PHONY: clean
clean: ## Remove build output
	rm -rf bin/
