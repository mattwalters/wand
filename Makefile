BIN     := bin/wand
PKG     := github.com/mattwalters/wand
VERSION ?= dev

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-z0-9-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the wand binary into bin/
	go build -ldflags "-X $(PKG)/internal/cli.Version=$(VERSION)" -o $(BIN) ./cmd/wand

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
	@go run ./cmd/wand ui --script "$(SCRIPT)" --dump-screen

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

.PHONY: clean
clean: ## Remove build output
	rm -rf bin/
