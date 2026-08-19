BIN     := bin/wand
PKG     := github.com/mattwalters/wand
VERSION ?= dev

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-z0-9-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the wand binary into bin/
	go build -ldflags "-X $(PKG)/internal/cli.Version=$(VERSION)" -o $(BIN) .

.PHONY: install
install: ## Install wand onto your PATH (needs $(go env GOPATH)/bin on PATH)
	go install -ldflags "-X $(PKG)/internal/cli.Version=$(VERSION)" .
	@echo "Installed to $$(go env GOPATH)/bin/wand"

.PHONY: run
run: ## Run the TUI from source
	@go run . ui

.PHONY: test
test: ## Run the fast suite (tiers 0-2)
	go test ./...

.PHONY: test-e2e
test-e2e: ## Run the pty smoke test (tier 3)
	go test -tags e2e ./e2e/...

.PHONY: update-goldens
update-goldens: ## Regenerate golden screens, then READ THE DIFF
	go test ./... -update
	@echo
	@echo "Goldens regenerated. Review the diff before committing:"
	@echo "    git diff -- '*/testdata/screens/*.txt'"

.PHONY: screen
screen: ## Print a screen, e.g. make screen SCRIPT=j,enter
	@go run . ui --script "$(SCRIPT)" --dump-screen

.PHONY: fmt
fmt: ## Format all Go source
	gofmt -w .

.PHONY: vet
vet: ## Run go vet over everything, including tagged files
	go vet ./...
	go vet -tags e2e ./e2e/...

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
