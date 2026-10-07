VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/jverhoeks/repoidx/cmd.version=$(VERSION)
TARGETS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64
REPO    := jverhoeks/repoidx

.DEFAULT_GOAL := help
.PHONY: help build install run test cover vet fmt fmt-check tidy check dist snapshot \
	release-check release release-watch clean

help: ## list targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo
	@echo "  release:  make release TAG=v0.1.0"

build: ## build ./bin/repoidx
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/repoidx .

install: ## install into $GOBIN (or ~/go/bin)
	CGO_ENABLED=0 go install -ldflags "$(LDFLAGS)" .

run: build ## build and run, pass args with ARGS="stats -s"
	./bin/repoidx $(ARGS)

test: ## run the tests
	go test ./...

cover: ## tests with a coverage summary
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

vet: ## go vet
	go vet ./...

fmt: ## gofmt all files
	gofmt -w .

fmt-check: ## fail if anything needs gofmt
	@out=$$(gofmt -l .); [ -z "$$out" ] || { echo "needs gofmt:"; echo "$$out"; exit 1; }

tidy: ## go mod tidy
	go mod tidy

check: fmt-check vet test ## fmt-check, vet and test (what CI runs)

dist: ## cross-compiled binaries in ./dist
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "dist/repoidx-$$os-$$arch$$ext"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/repoidx-$$os-$$arch$$ext . || exit 1; \
	done

snapshot: ## local goreleaser build into ./dist, nothing published
	goreleaser release --snapshot --clean

release-check: ## validate .goreleaser.yaml
	goreleaser check

# Tags main and pushes the tag; the Release workflow on GitHub does the rest
# (binaries, GitHub release, Homebrew cask).
release: ## tag and push a release: make release TAG=v0.1.0
	@[ -n "$(TAG)" ] || { echo "usage: make release TAG=v0.1.0 (latest: $$(git describe --tags --abbrev=0 2>/dev/null || echo none))"; exit 1; }
	@echo "$(TAG)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$$' || { echo "TAG must look like v1.2.3"; exit 1; }
	@[ "$$(git rev-parse --abbrev-ref HEAD)" = main ] || { echo "not on main"; exit 1; }
	@[ -z "$$(git status --porcelain)" ] || { echo "working tree not clean"; exit 1; }
	@git fetch -q --tags origin main
	@[ "$$(git rev-parse HEAD)" = "$$(git rev-parse origin/main)" ] || { echo "main differs from origin/main; pull or push first"; exit 1; }
	@! git rev-parse -q --verify "refs/tags/$(TAG)" >/dev/null || { echo "tag $(TAG) already exists"; exit 1; }
	$(MAKE) check release-check
	git tag -a $(TAG) -m "repoidx $(TAG)"
	git push origin $(TAG)
	@echo "released $(TAG); follow it with: make release-watch"

release-watch: ## follow the latest Release workflow run
	gh run watch -R $(REPO) --exit-status $$(gh run list -R $(REPO) --workflow release.yml --limit 1 --json databaseId -q '.[0].databaseId')

clean: ## remove build output
	rm -rf bin dist coverage.out
