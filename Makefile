# Makefile — local dev only, NOT used by CI/CD. See AI.md PART 25.
# Six core targets. DO NOT ADD MORE.

PROJECT_NAME := $(shell git remote get-url origin 2>/dev/null | sed -E 's|\.git/?$$||' | sed -E 's|.*/||' || basename "$$(pwd)")
PROJECT_ORG  := $(shell git remote get-url origin 2>/dev/null | sed -E 's|\.git/?$$||' | sed -E 's|.*/([^/]+)/[^/]+$$|\1|' || basename "$$(dirname "$$(pwd)")")

# Version precedence: release.txt (wins if it exists) > VERSION env var > "devel" fallback
VERSION := $(shell cat release.txt 2>/dev/null || echo "$${VERSION:-devel}")

# Build info - BUILD_EPOCH is the single captured time source
# Unix build timestamp (seconds, UTC) - used by the updater's daily-channel check
BUILD_EPOCH := $(shell date -u +%s)
# Derived from BUILD_EPOCH - ISO 8601 UTC, e.g. "2025-12-04T13:05:13Z"
# Used only for Docker OCI labels (org.opencontainers.image.created); not an ldflag
BUILD_DATE := $(shell date -u -d @$(BUILD_EPOCH) +"%Y-%m-%dT%H:%M:%SZ")
COMMIT_ID  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "N/A")

# Official site URL (OPTIONAL - never guess or assume)
OFFICIAL_SITE := $(shell [ -f site.txt ] && cat site.txt || echo "$${OFFICIAL_SITE:-}")

# Linker flags to embed build info — see src/common/version
# BuildDate is NOT embedded - it is derived from BuildEpoch at process start
VERSION_PKG := github.com/$(PROJECT_ORG)/$(PROJECT_NAME)/src/common/version
LDFLAGS := -s -w \
	-X '$(VERSION_PKG).Version=$(VERSION)' \
	-X '$(VERSION_PKG).CommitID=$(COMMIT_ID)' \
	-X '$(VERSION_PKG).BuildEpoch=$(BUILD_EPOCH)' \
	-X '$(VERSION_PKG).OfficialSite=$(OFFICIAL_SITE)'

# Directories
BINDIR := binaries
RELDIR := releases

# Go directories (persistent across builds)
GO_CACHE  ?= $(HOME)/go/pkg/mod
GO_BUILD  ?= $(HOME)/.cache/go-build/$(PROJECT_NAME)

# Build targets - all 8 platforms
PLATFORMS ?= linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64,freebsd/amd64,freebsd/arm64

# Docker - Set REGISTRY based on your platform (ghcr.io, registry.gitlab.com, git.example.com)
REGISTRY ?= ghcr.io/$(PROJECT_ORG)/$(PROJECT_NAME)

# Resource limits for build containers
DOCKER_MEM  ?= 4g
DOCKER_CPUS ?= 2

GO_DOCKER_RUN := docker run --rm \
	--name $(PROJECT_NAME)-$$(tr -dc 'a-z0-9' </dev/urandom | head -c8) \
	--memory=$(DOCKER_MEM) --cpus=$(DOCKER_CPUS) \
	-v $(PWD):/app \
	-v $(GO_CACHE):/usr/local/share/go/pkg/mod \
	-v $(GO_BUILD):/usr/local/share/go/cache \
	-w /app \
	-e CGO_ENABLED=0 \
	-e GOFLAGS=-buildvcs=false
GO_DOCKER := $(GO_DOCKER_RUN) casjaysdev/go:latest

.PHONY: build local release docker test dev clean i18n-validate

# =============================================================================
# BUILD - Build all platforms + local binary (via Docker with cached modules)
# =============================================================================
build: clean
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@echo "Building version $(VERSION)..."

	@$(GO_DOCKER) go mod tidy
	@$(GO_DOCKER) go mod download

	@echo "Building local binary..."
	@$(GO_DOCKER) sh -c "GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH) \
		go build -buildvcs=false -trimpath -ldflags \"$(LDFLAGS)\" -o $(BINDIR)/$(PROJECT_NAME) ./src"

	@for platform in $$(echo "$(PLATFORMS)" | tr ',' ' '); do \
		OS=$${platform%/*}; \
		ARCH=$${platform#*/}; \
		OUTPUT=$(BINDIR)/$(PROJECT_NAME)-$$OS-$$ARCH; \
		[ "$$OS" = "windows" ] && OUTPUT=$$OUTPUT.exe; \
		echo "Building server $$OS/$$ARCH..."; \
		$(GO_DOCKER) sh -c "GOOS=$$OS GOARCH=$$ARCH \
			go build -buildvcs=false -trimpath -ldflags \"$(LDFLAGS)\" \
			-o $$OUTPUT ./src" || exit 1; \
	done

	@if [ -d "src/client" ]; then \
		for platform in $$(echo "$(PLATFORMS)" | tr ',' ' '); do \
			OS=$${platform%/*}; \
			ARCH=$${platform#*/}; \
			OUTPUT=$(BINDIR)/$(PROJECT_NAME)-cli-$$OS-$$ARCH; \
			[ "$$OS" = "windows" ] && OUTPUT=$$OUTPUT.exe; \
			echo "Building CLI $$OS/$$ARCH..."; \
			$(GO_DOCKER) sh -c "GOOS=$$OS GOARCH=$$ARCH \
				go build -buildvcs=false -trimpath -ldflags \"$(LDFLAGS)\" \
				-o $$OUTPUT ./src/client" || exit 1; \
		done; \
	fi

	@echo "Build complete: $(BINDIR)/"

# =============================================================================
# LOCAL - Build local binaries only (fast development builds)
# =============================================================================
local: clean
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@echo "Building local binaries version $(VERSION)..."

	@$(GO_DOCKER) go mod tidy
	@$(GO_DOCKER) go mod download

	@echo "Building $(PROJECT_NAME)..."
	@$(GO_DOCKER) sh -c "GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH) \
		go build -buildvcs=false -trimpath -ldflags \"$(LDFLAGS)\" -o $(BINDIR)/$(PROJECT_NAME) ./src"

	@if [ -d "src/client" ]; then \
		echo "Building $(PROJECT_NAME)-cli..."; \
		$(GO_DOCKER) sh -c "GOOS=$$(go env GOOS) GOARCH=$$(go env GOARCH) \
			go build -buildvcs=false -trimpath -ldflags \"$(LDFLAGS)\" -o $(BINDIR)/$(PROJECT_NAME)-cli ./src/client"; \
	fi

	@echo "Local build complete: $(BINDIR)/"

# =============================================================================
# RELEASE - Manual local release (stable only)
# =============================================================================
release: build
	@mkdir -p $(RELDIR)
	@echo "Preparing release $(VERSION)..."

	@echo "$(VERSION)" > $(RELDIR)/version.txt

	@for f in $(BINDIR)/$(PROJECT_NAME)-*; do \
		[ -f "$$f" ] || continue; \
		strip "$$f" 2>/dev/null || true; \
		cp "$$f" $(RELDIR)/; \
	done

	@tar --exclude='.git' --exclude='.github' --exclude='.gitea' \
		--exclude='binaries' --exclude='releases' --exclude='*.tar.gz' \
		-czf $(RELDIR)/$(PROJECT_NAME)-$(VERSION)-source.tar.gz .

	# Generate checksums
	@cd $(RELDIR) && FILES="$$(ls)" && sha256sum $$FILES > sha256.txt && sha512sum $$FILES > sha512.txt

	@gh release delete $(VERSION) --yes 2>/dev/null || true
	@git tag -d $(VERSION) 2>/dev/null || true
	@git push origin :refs/tags/$(VERSION) 2>/dev/null || true

	@gh release create $(VERSION) $(RELDIR)/* \
		--title "$(PROJECT_NAME) $(VERSION)" \
		--notes "Release $(VERSION)" \
		--latest

	@echo "Release complete: $(VERSION)"

# =============================================================================
# DOCKER - Build and push container to registry (set REGISTRY env var)
# =============================================================================
docker:
	@echo "Building Docker image $(VERSION)..."

	@docker buildx version > /dev/null 2>&1 || (echo "docker buildx required" && exit 1)

	@docker buildx create --name $(PROJECT_NAME)-builder --use 2>/dev/null || \
		docker buildx use $(PROJECT_NAME)-builder

	@docker buildx build \
		-f docker/Dockerfile \
		--platform linux/amd64,linux/arm64 \
		--push \
		--build-arg VERSION="$(VERSION)" \
		--build-arg BUILD_DATE="$(BUILD_DATE)" \
		--build-arg BUILD_EPOCH="$(BUILD_EPOCH)" \
		--build-arg COMMIT_ID="$(COMMIT_ID)" \
		-t $(REGISTRY):$(VERSION) \
		-t $(REGISTRY):latest \
		.

	@echo "Docker build complete: $(REGISTRY):$(VERSION)"

# =============================================================================
# I18N-VALIDATE - Build-time translation check required by AI.md PART 30
# =============================================================================
i18n-validate:
	@mkdir -p $(GO_CACHE) $(GO_BUILD)
	@echo "Validating translation files..."
	@$(GO_DOCKER) go run ./cmd/i18n-validate src/common/i18n/locales/

# =============================================================================
# TEST - Run all tests with coverage enforcement (via Docker)
# =============================================================================
test: i18n-validate
	@mkdir -p $(GO_CACHE) $(GO_BUILD)
	@echo "Running tests with coverage..."
	@$(GO_DOCKER) sh -c " \
		mkdir -p \"\$${TMPDIR:-/tmp}/$(PROJECT_ORG)\" && \
		COVDIR=\$$(mktemp -d \"\$${TMPDIR:-/tmp}/$(PROJECT_ORG)/$(PROJECT_NAME)-XXXXXX\") && \
		go mod download && \
		go test -v -cover -coverprofile=\$$COVDIR/coverage.out ./... && \
		COVERAGE=\$$(go tool cover -func=\$$COVDIR/coverage.out | grep total | awk '{print \$$3}' | sed 's/%//') && \
		echo \"Coverage: \$$COVERAGE%\" && \
		if [ \$$(echo \"\$$COVERAGE < 60\" | bc -l) -eq 1 ]; then \
			echo \"ERROR: Coverage is \$$COVERAGE%, must be >= 60%\"; exit 1; \
		fi && \
		echo \"Tests complete - Coverage: \$$COVERAGE% (>= 60% required)\""

# =============================================================================
# DEV - Quick build for local development/testing (to random temp dir)
# =============================================================================
dev:
	@mkdir -p $(GO_CACHE) $(GO_BUILD)
	@$(GO_DOCKER) go mod tidy
	@mkdir -p "$${TMPDIR:-/tmp}/$(PROJECT_ORG)" && \
		BUILD_DIR=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECT_ORG)/$(PROJECT_NAME)-XXXXXX") && \
		echo "Quick dev build to $$BUILD_DIR..." && \
		$(GO_DOCKER_RUN) -v $$BUILD_DIR:/build casjaysdev/go:latest \
			go build -buildvcs=false -o /build/$(PROJECT_NAME) ./src && \
		echo "Built: $$BUILD_DIR/$(PROJECT_NAME)" && \
		if [ -d "src/client" ]; then \
			$(GO_DOCKER_RUN) -v $$BUILD_DIR:/build casjaysdev/go:latest \
				go build -buildvcs=false -o /build/$(PROJECT_NAME)-cli ./src/client && \
			echo "Built: $$BUILD_DIR/$(PROJECT_NAME)-cli"; \
		fi && \
		echo "Test:  docker run --rm --name $(PROJECT_NAME)-test -v $$BUILD_DIR:/app alpine:latest /app/$(PROJECT_NAME) --help"

# =============================================================================
# CLEAN - Remove build artifacts
# =============================================================================
clean:
	@rm -rf $(BINDIR) $(RELDIR)
