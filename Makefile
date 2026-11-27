# PROJECT_NAME and PROJECT_ORG: IDEA.md project variables first (the same source as {project_name}), then the git remote, then the directory path (NEVER hardcode)
PROJECT_NAME := $(shell sed -n 's/^project_name:[[:space:]]*//p' IDEA.md 2>/dev/null | head -n1 | tr -d '[:space:]')
ifeq ($(PROJECT_NAME),)
PROJECT_NAME := $(shell git remote get-url origin 2>/dev/null | sed -E 's|.*/([^/]+)$$|\1|; s|\.git$$||' || basename "$$(pwd)")
endif
PROJECT_ORG := $(shell sed -n 's/^project_org:[[:space:]]*//p' IDEA.md 2>/dev/null | head -n1 | tr -d '[:space:]')
ifeq ($(PROJECT_ORG),)
PROJECT_ORG := $(shell git remote get-url origin 2>/dev/null | sed -E 's|.*/([^/]+)/[^/]+(\.git)?$$|\1|' || basename "$$(dirname "$$(pwd)")")
endif

# INTERNAL_NAME: the frozen identity from IDEA.md (internal_name), PROJECT_NAME on first run (PART 0)
INTERNAL_NAME := $(shell sed -n 's/^internal_name:[[:space:]]*//p' IDEA.md 2>/dev/null | head -n1 | tr -d '[:space:]')
ifeq ($(INTERNAL_NAME),)
INTERNAL_NAME := $(PROJECT_NAME)
endif

# Local builds only: VERSION env (user override, AI never sets it) > non-empty release.txt > devel rule (short commit id, "devel" outside git)
VERSION ?= $(shell v=; [ -r release.txt ] && v=$$(tr -d '[:space:]' < release.txt); if [ -n "$$v" ]; then echo "$$v"; else git rev-parse --short=7 HEAD 2>/dev/null || echo devel; fi)

# Build info - BUILD_EPOCH is the single captured time source
# Unix build timestamp (seconds, UTC) - used by the updater's devel-channel check
BUILD_EPOCH := $(shell date -u +%s)
# Derived from BUILD_EPOCH - ISO 8601 UTC, e.g. "2025-12-04T13:05:13Z"
# Used only for Docker OCI labels (org.opencontainers.image.created); not an ldflag
BUILD_DATE := $(shell date -u -d @$(BUILD_EPOCH) +"%Y-%m-%dT%H:%M:%SZ")
COMMIT_ID := $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo "N/A")
# COMMIT_ID used directly - no VCS_REF alias

# Official site URL (OPTIONAL - never guess or assume)
# Sources (in order of precedence):
#   1. File: site.txt in project root (single line, URL only)
#   2. Environment variable: OFFICIAL_SITE=<url>
#   3. Empty (self-hosted projects - users must use --server flag)
# NEVER infer from project name, domain, or any other source
OFFICIAL_SITE := $(shell [ -f site.txt ] && cat site.txt || echo "${OFFICIAL_SITE:-}")

# Linker flags to embed build info
# BuildDate is NOT embedded - it is derived from BuildEpoch at process start
LDFLAGS := -s -w \
	-X 'github.com/$(PROJECT_ORG)/$(INTERNAL_NAME)/src/common/version.Version=$(VERSION)' \
	-X 'github.com/$(PROJECT_ORG)/$(INTERNAL_NAME)/src/common/version.CommitID=$(COMMIT_ID)' \
	-X 'github.com/$(PROJECT_ORG)/$(INTERNAL_NAME)/src/common/version.BuildEpoch=$(BUILD_EPOCH)' \
	-X 'github.com/$(PROJECT_ORG)/$(INTERNAL_NAME)/src/common/version.OfficialSite=$(OFFICIAL_SITE)'

# Debug builds (make build): keep symbols and DWARF, same embedded build info
DEBUG_LDFLAGS := \
	-X 'github.com/$(PROJECT_ORG)/$(INTERNAL_NAME)/src/common/version.Version=$(VERSION)' \
	-X 'github.com/$(PROJECT_ORG)/$(INTERNAL_NAME)/src/common/version.CommitID=$(COMMIT_ID)' \
	-X 'github.com/$(PROJECT_ORG)/$(INTERNAL_NAME)/src/common/version.BuildEpoch=$(BUILD_EPOCH)' \
	-X 'github.com/$(PROJECT_ORG)/$(INTERNAL_NAME)/src/common/version.OfficialSite=$(OFFICIAL_SITE)'
# Debug builds: no optimization and no inlining
DEBUG_GCFLAGS := all=-N -l

# Directories
BINDIR := binaries
RELDIR := releases

# Go directories (persistent across builds)
# GO_CACHE: module download cache — safe to share across concurrent builds (Go file-locks writes)
# GO_BUILD: compile cache — scoped per project to prevent corruption when multiple projects build concurrently
GO_CACHE  ?= $(HOME)/go/pkg/mod
GO_BUILD  ?= $(HOME)/.cache/go-build/$(PROJECT_NAME)

# Build targets - all 8 platforms
PLATFORMS ?= linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64,freebsd/amd64,freebsd/arm64

# Docker - Set REGISTRY based on your platform (ghcr.io, registry.gitlab.com, git.example.com)
REGISTRY ?= ghcr.io/$(PROJECT_ORG)/$(PROJECT_NAME)

# Resource limits for build containers
DOCKER_MEM  ?= 4g
DOCKER_CPUS ?= 2

# CPU weight under contention (Docker's default is 1024): a build never starves the host's other work
DOCKER_CPU_SHARES ?= 256

# Hard ceiling in seconds for ONE container run, so a hung step can never hold a container for hours (raise it for a very long cross-compile)
DOCKER_TIMEOUT ?= 3600

# GO_DOCKER_RUN: shared docker run prefix (no image) so targets can add mounts before the image
# --rm: never leave an exited container behind; --no-healthcheck: the toolchain image's service HEALTHCHECK must not poll a one-shot build container;
# timeout: a hung run is stopped after DOCKER_TIMEOUT seconds (the signal is forwarded to the container, which stops)
GO_DOCKER_RUN := timeout -k 30 $(DOCKER_TIMEOUT) docker run --rm --no-healthcheck \
	--name $(PROJECT_NAME)-$$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8) \
	--memory=$(DOCKER_MEM) --cpus=$(DOCKER_CPUS) --cpu-shares=$(DOCKER_CPU_SHARES) \
	-v $(PWD):/app \
	-v $(GO_CACHE):/usr/local/share/go/pkg/mod \
	-v $(GO_BUILD):/usr/local/share/go/cache \
	-w /app \
	-e CGO_ENABLED=0 \
	-e GOFLAGS=-buildvcs=false
GO_DOCKER := $(GO_DOCKER_RUN) casjaysdev/go:latest
# CGO_ENABLED=0 and GOFLAGS=-buildvcs=false are casjaysdev/go:latest defaults; set explicitly for clarity

.DEFAULT_GOAL := help
.PHONY: build release docker test clean help

# =============================================================================
# BUILD - Debug binaries for all platforms (via Docker with cached modules)
# =============================================================================
# Debug build: symbols and DWARF kept, optimizer and inlining off (-N -l), no -trimpath, so a debugger or a stack trace shows real source lines
build: clean
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@echo "Building debug binaries, version $(VERSION)..."

	# Tidy, download, local build and every platform in ONE container (one sh -c loop, never a container per platform)
	@$(GO_DOCKER_RUN) -e BUILD_LDFLAGS="$(DEBUG_LDFLAGS)" -e BUILD_GCFLAGS="$(DEBUG_GCFLAGS)" casjaysdev/go:latest sh -c ' \
		set -e; \
		go mod tidy; \
		go mod download; \
		echo "Building $(PROJECT_NAME) (local platform)..."; \
		GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH) go build -buildvcs=false -gcflags "$$BUILD_GCFLAGS" -ldflags "$$BUILD_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME) ./src; \
		if [ -d src/client ]; then \
			echo "Building $(PROJECT_NAME)-cli (local platform)..."; \
			GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH) go build -buildvcs=false -gcflags "$$BUILD_GCFLAGS" -ldflags "$$BUILD_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-cli ./src/client; \
		fi; \
		for platform in $$(echo "$(PLATFORMS)" | tr "," " "); do \
			OS=$${platform%/*}; \
			ARCH=$${platform#*/}; \
			EXT=""; \
			if [ "$$OS" = "windows" ]; then EXT=".exe"; fi; \
			echo "Building server $$OS/$$ARCH..."; \
			GOOS=$$OS GOARCH=$$ARCH go build -buildvcs=false -gcflags "$$BUILD_GCFLAGS" -ldflags "$$BUILD_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-$$OS-$$ARCH$$EXT ./src; \
			if [ -d src/client ]; then \
				echo "Building CLI $$OS/$$ARCH..."; \
				GOOS=$$OS GOARCH=$$ARCH go build -buildvcs=false -gcflags "$$BUILD_GCFLAGS" -ldflags "$$BUILD_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-cli-$$OS-$$ARCH$$EXT ./src/client; \
			fi; \
		done'

	@echo "Build complete: $(BINDIR)/"

# =============================================================================
# RELEASE - Production binaries for all platforms, source archive and checksums
# =============================================================================
# Builds only: it never tags, pushes or publishes (CI/CD tags, builds and publishes the release)
# Production build: stripped (-s -w), -trimpath, optimized, version info embedded
release:
	@mkdir -p $(RELDIR) $(GO_CACHE) $(GO_BUILD)
	@rm -rf $(RELDIR)/*
	@echo "Building production binaries, version $(VERSION)..."

	# Tidy, download and every platform in ONE container
	@$(GO_DOCKER_RUN) -e BUILD_LDFLAGS="$(LDFLAGS)" casjaysdev/go:latest sh -c ' \
		set -e; \
		go mod tidy; \
		go mod download; \
		for platform in $$(echo "$(PLATFORMS)" | tr "," " "); do \
			OS=$${platform%/*}; \
			ARCH=$${platform#*/}; \
			EXT=""; \
			if [ "$$OS" = "windows" ]; then EXT=".exe"; fi; \
			echo "Building server $$OS/$$ARCH..."; \
			GOOS=$$OS GOARCH=$$ARCH go build -buildvcs=false -trimpath -ldflags "$$BUILD_LDFLAGS" -o $(RELDIR)/$(PROJECT_NAME)-$$OS-$$ARCH$$EXT ./src; \
			if [ -d src/client ]; then \
				echo "Building CLI $$OS/$$ARCH..."; \
				GOOS=$$OS GOARCH=$$ARCH go build -buildvcs=false -trimpath -ldflags "$$BUILD_LDFLAGS" -o $(RELDIR)/$(PROJECT_NAME)-cli-$$OS-$$ARCH$$EXT ./src/client; \
			fi; \
		done'

	# version.txt stays unprefixed (numeric SemVer or the devel commit id)
	@echo "$(VERSION)" > $(RELDIR)/version.txt

	# Source archive (exclude VCS and build artifacts)
	@tar --exclude='.git' --exclude='.github' --exclude='.gitea' \
		--exclude='binaries' --exclude='releases' --exclude='*.tar.gz' \
		-czf $(RELDIR)/$(PROJECT_NAME)-$(VERSION)-source.tar.gz .

	# Checksums
	@cd $(RELDIR) && FILES="$$(ls)" && sha256sum $$FILES > sha256.txt && sha512sum $$FILES > sha512.txt

	@echo "Release build complete: $(RELDIR)/"

# =============================================================================
# DOCKER - Build the host-platform container image locally (no push: CI/CD publishes; set REGISTRY env var for the tag)
# =============================================================================
# Uses multi-stage Dockerfile - Go compilation happens inside Docker
# No pre-built binaries needed
docker:
	@echo "Building Docker image $(VERSION)..."

	# Ensure buildx is available
	@docker buildx version > /dev/null 2>&1 || (echo "docker buildx required" && exit 1)

	# Create/use the ONE shared builder (never one per project: every builder is a long-lived buildkit container)
	@docker buildx create --name multiarch-builder --use 2>/dev/null || \
		docker buildx use multiarch-builder

	# Build the host platform and load it into the local image store (no push and no multi-arch: both are CI/CD's responsibility)
	@docker buildx build \
		-f docker/Dockerfile \
		--load \
		--build-arg VERSION="$(VERSION)" \
		--build-arg BUILD_DATE="$(BUILD_DATE)" \
		--build-arg BUILD_EPOCH="$(BUILD_EPOCH)" \
		--build-arg COMMIT_ID="$(COMMIT_ID)" \
		--build-arg OFFICIAL_SITE="$(OFFICIAL_SITE)" \
		-t $(REGISTRY):$(VERSION) \
		-t $(REGISTRY):latest \
		.

	@echo "Docker build complete: $(REGISTRY):$(VERSION)"

# =============================================================================
# TEST - Run all tests with coverage enforcement (via Docker)
# =============================================================================
test:
	@mkdir -p $(GO_CACHE) $(GO_BUILD)
	@echo "Running tests with coverage..."
	@$(GO_DOCKER) sh -c " \
		mkdir -p \"\$${TMPDIR:-/tmp}/$(PROJECT_ORG)\" && \
		COVDIR=\$$(mktemp -d \"\$${TMPDIR:-/tmp}/$(PROJECT_ORG)/$(INTERNAL_NAME)-XXXXXX\") && \
		trap 'rm -rf "\$$COVDIR"' EXIT && \
		go mod download && \
		go vet ./... && \
		staticcheck ./... && \
		govulncheck ./... && \
		if ls tests/*.sh >/dev/null 2>&1; then shellcheck tests/*.sh && shfmt -d -i 2 -ci tests/*.sh; fi && \
		mkdir -p \$$COVDIR/data && \
		TMPDIR=\$$COVDIR go test -v -cover ./... -args -test.gocoverdir=\$$COVDIR/data && \
		go tool covdata textfmt -i=\$$COVDIR/data -o=\$$COVDIR/coverage.out && \
		COVERAGE=\$$(go tool cover -func=\$$COVDIR/coverage.out | grep '^total:' | awk '{print \$$3}' | sed 's/%//') && \
		echo \"Coverage: \$$COVERAGE%\" && \
		if [ \$$(echo \"\$$COVERAGE < 60\" | bc -l) -eq 1 ]; then \
			echo \"ERROR: Coverage is \$$COVERAGE%, must be >= 60%\"; exit 1; \
		fi && \
		echo \"Tests complete - Coverage: \$$COVERAGE% (>= 60% required) ✓\""
	@echo "Running the binary checks (start, --help, --version, CLI)..."
	@./tests/run_tests.sh

# =============================================================================
# Coverage gates:
#   - All Go projects: 60% minimum (go test -cover must report >= 60.0%)
#   - Override upward in IDEA.md (## Project variables -> coverage_minimum: 80)
#     when higher coverage is appropriate. Never override downward.
#   - CLI tools and libraries: same 60% floor.
# Coverage runs in CI on every push and in `make test` locally.
# Never skip with -short or -count=0. No //go:coverage ignore workarounds.
# =============================================================================

# =============================================================================
# CLEAN - Remove build artifacts
# =============================================================================
clean:
	@rm -rf $(BINDIR) $(RELDIR)

# =============================================================================
# HELP - List the targets (the default goal)
# =============================================================================
help:
	@echo "Targets:"
	@echo "  build    Debug binaries for all platforms to $(BINDIR)/"
	@echo "  test     Lint, vulnerability scan, unit tests, then the binary checks (start, --help, --version, CLI)"
	@echo "  release  Production binaries for all platforms, source archive and checksums to $(RELDIR)/"
	@echo "  docker   Container image for the host platform (no push)"
	@echo "  clean    Remove $(BINDIR)/ and $(RELDIR)/"
	@echo "  help     This list"
