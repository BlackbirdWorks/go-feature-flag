GOCMD=go
DOCKERCMD=docker
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
ALL_GO_MOD_DIRS := ./rules ./modules/core ./

# In CI we disable workspace mode.
ifeq ($(CI),true)
  GOWORK_ENV := GOWORK=off
  MODFLAG := 
else
  GOWORK_ENV :=
  MODFLAG :=
endif

GREEN  := $(shell tput -Txterm setaf 2)
YELLOW := $(shell tput -Txterm setaf 3)
WHITE  := $(shell tput -Txterm setaf 7)
CYAN   := $(shell tput -Txterm setaf 6)
RESET  := $(shell tput -Txterm sgr0)

.PHONY: all test test-unit test-unit-root test-unit-module test-integration test-e2e build vendor fuzz lint lint-install lint-module

SHELL := bash

all: help
## Build:
build: build-modules build-relayproxy build-jsonschema-generator build-cli  ## Build all the binaries and put the output in out/bin/

create-out-dir:
	mkdir -p out/bin
	mkdir -p out/contrib

build-relayproxy: create-out-dir ## Build the relay proxy in out/bin/
	CGO_ENABLED=0 GO111MODULE=on $(GOWORK_ENV) $(GOCMD) build $(MODFLAG) -o out/bin/relayproxy ./cmd/relayproxy/

# Single source of truth for the FIPS module version.
GOFIPS140_VERSION := $(shell cat .fips-version | tr -d '[:space:]')

build-relayproxy-fips: create-out-dir ## Build the relay proxy in FIPS 140-3 mode in out/bin/
	CGO_ENABLED=0 GOFIPS140=$(GOFIPS140_VERSION) GO111MODULE=on $(GOWORK_ENV) $(GOCMD) build $(MODFLAG) -o out/bin/relayproxy-fips ./cmd/relayproxy/

# Local docker image of the relay proxy, built from source. The DockerfileGoreleaser*
# files are release artifacts (they only COPY an already cross-compiled binary), so
# cmd/relayproxy/Dockerfile.local is the one that can be built by hand.
RELAYPROXY_IMAGE ?= go-feature-flag:local
RELAYPROXY_IMAGE_VERSION ?= localdev

build-relayproxy-docker: ## Build a local docker image of the relay proxy from source
	$(DOCKERCMD) build -f cmd/relayproxy/Dockerfile.local \
		--build-arg VERSION=$(RELAYPROXY_IMAGE_VERSION) \
		-t $(RELAYPROXY_IMAGE) .

build-cli: create-out-dir ## Build the cli in out/bin/
	CGO_ENABLED=0 GO111MODULE=on $(GOWORK_ENV) $(GOCMD) build $(MODFLAG) -o out/bin/cli ./cmd/cli/

build-jsonschema-generator: create-out-dir ## Build the jsonschema-generator in out/bin/
	CGO_ENABLED=0 GO111MODULE=on $(GOWORK_ENV) $(GOCMD) build $(MODFLAG) -o out/bin/jsonschema-generator ./cmd/jsonschema-generator/

build-modules:  ## Run build command to build all modules in the workspace
	@echo "Building all modules in the workspace..."
	@$(foreach module, $(ALL_GO_MOD_DIRS), (echo "→ Building $(module)"; CGO_ENABLED=0 GO111MODULE=on $(GOWORK_ENV) $(GOCMD) build $(MODFLAG) ./...);)

clean: ## Remove build related file
	-rm -fr ./bin ./out ./release
	-rm -f ./junit-report.xml checkstyle-report.xml ./coverage.xml ./profile.cov yamllint-checkstyle.xml ./coverage-*.cov.tmp
	-rm -rf vendor
	-rm -f go.work.sum go.work

vendor: tidy ## Copy of all packages needed to support builds and tests in the vendor directory
ifneq ($(CI),)
	$(GOWORK_ENV) $(GOCMD) mod vendor
else
	$(GOWORK_ENV) $(GOCMD) work vendor
endif

tidy: ## Run go mod tidy for all modules in the workspace
ifeq ($(CI),)
	$(GOWORK_ENV) $(GOCMD) work sync
endif
	@$(foreach module, $(ALL_GO_MOD_DIRS), (echo "→ Tidying $(module)"; cd $(module) && $(GOWORK_ENV) $(GOCMD) mod tidy);)

tidy-check: tidy ## Check that go.mod and go.sum are tidy (fails if there are uncommitted changes after tidy)
	git diff --exit-code --quiet go.mod go.sum modules/core/go.mod modules/core/go.sum rules/go.mod rules/go.sum

verify: ## Run go mod verify for all modules to validate checksums match go.sum
	@$(foreach module, $(ALL_GO_MOD_DIRS), (echo "→ Verifying $(module)"; cd $(module) && $(GOWORK_ENV) $(GOCMD) mod verify) &&) true

## Dev:
# The replace is needed because xitongsys/parquet-go-source pulls cloud.google.com/go/compute v1.5.0
# (via gocloud.dev v0.26.0), which still contains the metadata package and clashes with
# cloud.google.com/go/compute/metadata in workspace mode. v1.12.0 is the first version without it.
# Remove it once we drop xitongsys/parquet-go.
workspace-init:
	go work init
	$(foreach module, $(ALL_GO_MOD_DIRS), go work use $(module);)
	go work edit -replace cloud.google.com/go/compute=cloud.google.com/go/compute@v1.12.0
	go work sync

watch-relayproxy: ## Launch the relay proxy in watch mode.
	docker run -it --rm -w /go/src/github.com/thomaspoignant/go-feature-flag/ \
		-v $(shell pwd):/go/src/github.com/thomaspoignant/go-feature-flag \
		-v $(shell pwd)/cmd/relayproxy/testdata/config/valid-file.yaml:/goff/goff-proxy.yaml \
		-p 1031:1031 cosmtrek/air \
		--build.cmd "$(GOWORK_ENV) go build $(MODFLAG) -o out/bin/relayproxy ./cmd/relayproxy/" \
		--build.bin "./out/bin/relayproxy"

swagger: ## Build swagger documentation
	$(GOWORK_ENV) $(GOCMD) install github.com/swaggo/swag/cmd/swag@latest
	cd cmd/relayproxy && swag init --parseDependency --parseDepth=1 --parseInternal --markdownFiles docs

generate-helm-docs: ## Generates helm documentation for the project
	$(GOWORK_ENV) $(GOCMD) install github.com/norwoodj/helm-docs/cmd/helm-docs@latest
	helm-docs

## Test:
# Test types run as separate targets (and separate CI jobs):
#   test-unit         unit tests, no docker tag (root module, modules/core, rules)
#   test-integration  testcontainers tests (azurite, mongo, postgres, redis, ...)
#   test-e2e          gopherstack-backed e2e tests (tests named *E2E*)
# Optional variables: COVERPROFILE=<file> writes a coverage profile,
# TEST_LOG=<file> tees the output, GROUP=<cmd|plugins|rest|all> picks the root
# unit shard, MODULE=<rules|modules/core> picks the module of test-unit-module.
# On the aggregate targets (test, test-unit) the files are suffixed per suite.
TEST_PKG_SCRIPT := .github/ci-scripts/test-pkgs.sh
COVER_FLAGS = $(if $(COVERPROFILE),-covermode=atomic -coverprofile=$(abspath $(COVERPROFILE)) $(if $(COVERPKG),-coverpkg=$(COVERPKG)),)
TEE = $(if $(TEST_LOG),2>&1 | tee $(abspath $(TEST_LOG)),)
GROUP ?= all
GOTESTFLAGS ?=

# Aggregate targets (test, test-unit) run several suites. Every suite gets its own
# file: the suite name is appended to COVERPROFILE / TEST_LOG before the extension
# (COVERPROFILE=cov/all.out -> cov/all-unit-root.out, cov/all-unit-rules.out, ...).
suite_file = $(if $(1),$(basename $(1))-$(2)$(suffix $(1)),)
suite_make = $(MAKE) --no-print-directory $(1) COVERPROFILE=$(call suite_file,$(COVERPROFILE),$(2)) TEST_LOG=$(call suite_file,$(TEST_LOG),$(2))

test: ## Run every test type (unit, integration, e2e); one coverage/log file per suite
	@$(MAKE) --no-print-directory test-unit
	@$(call suite_make,test-integration,integration)
	@$(call suite_make,test-e2e,e2e)

test-unit: ## Run the unit tests of all modules (no docker); one coverage/log file per module
	@$(call suite_make,test-unit-root,unit-root)
	@$(call suite_make,test-unit-module MODULE=rules,unit-rules)
	@$(call suite_make,test-unit-module MODULE=modules/core,unit-core)

test-unit-root: ## Run the root module unit tests with the race detector (GROUP=cmd|plugins|rest|all)
	@set -o pipefail; pkgs=$$($(TEST_PKG_SCRIPT) unit-$(GROUP)); \
	[ -n "$$pkgs" ] || { echo "no packages in group $(GROUP)"; exit 1; }; \
	CGO_ENABLED=1 GO111MODULE=on $(GOWORK_ENV) $(GOTEST) $(GOTESTFLAGS) -v -race $(COVER_FLAGS) $$pkgs $(TEE)

test-unit-module: ## Run the unit tests of one nested module (MODULE=rules|modules/core)
	@test -n "$(MODULE)" || { echo "usage: make test-unit-module MODULE=<dir>"; exit 1; }
	@set -o pipefail; cd $(MODULE) && CGO_ENABLED=1 GO111MODULE=on $(GOWORK_ENV) $(GOTEST) $(GOTESTFLAGS) -v -race -tags=docker $(COVER_FLAGS) ./... $(TEE)

# The plain unit tests of these packages run again here (the docker tag compiles
# them together with the container tests). That is cheap and always correct,
# unlike selecting tests by name with -run.
test-integration: ## Run the testcontainers integration tests (needs docker)
	@set -o pipefail; pkgs=$$($(TEST_PKG_SCRIPT) integration); \
	[ -n "$$pkgs" ] || { echo "no integration packages found"; exit 1; }; \
	CGO_ENABLED=1 GO111MODULE=on $(GOWORK_ENV) $(GOTEST) $(GOTESTFLAGS) -v -race -tags=docker -skip 'E2E' $(COVER_FLAGS) $$pkgs $(TEE)

test-e2e: ## Run the gopherstack e2e tests (needs docker)
	@set -o pipefail; pkgs=$$($(TEST_PKG_SCRIPT) e2e); \
	[ -n "$$pkgs" ] || { echo "no e2e packages found"; exit 1; }; \
	CGO_ENABLED=1 GO111MODULE=on $(GOWORK_ENV) $(GOTEST) $(GOTESTFLAGS) -v -race -tags=docker -run 'E2E' $(COVER_FLAGS) $$pkgs $(TEE)

test-shards-check: ## Fail if the CI test groups do not cover every package
	@$(TEST_PKG_SCRIPT) check

fuzz: ## Run every fuzz test for a bounded time (usage: make fuzz FUZZTIME=10s)
	@FUZZTIME="$(or $(FUZZTIME),10s)" GOWORK_ENV="$(GOWORK_ENV)" GOCMD="$(GOCMD)" .github/ci-scripts/fuzz.sh $(ALL_GO_MOD_DIRS)

fuzz-list: ## Print "<module> <dir>" for every package with fuzz tests
	@.github/ci-scripts/fuzz.sh --list $(ALL_GO_MOD_DIRS)

fuzz-dir: ## Fuzz one package (usage: make fuzz-dir MODULE=./ DIR=./internal/cache FUZZTIME=10s)
	@test -n "$(MODULE)" -a -n "$(DIR)" || { echo "usage: make fuzz-dir MODULE=<module> DIR=<dir>"; exit 1; }
	@FUZZTIME="$(or $(FUZZTIME),10s)" GOWORK_ENV="$(GOWORK_ENV)" GOCMD="$(GOCMD)" .github/ci-scripts/fuzz.sh --dir $(MODULE) $(DIR)

provider-tests: ## Run the integration tests for the Open Feature Providers
	./openfeature/provider_tests/integration_tests.sh

# Merged coverage: every suite writes its own profile into coverage/, then
# coverage.py merges them (a block covered by any suite counts once), applies
# .github/ci-scripts/coverage-exclude.txt and prints the LINE coverage that
# GitHub's native coverage reads from coverage/report/cobertura.xml. CI runs the
# same script in the `coverage` job. COVERAGE_MIN=<percent> makes it fail below.
COVERAGE_SCRIPT := .github/ci-scripts/coverage.py
COVERAGE_MIN ?=

coverage: ## Run every test suite with coverage, merge the profiles and report line coverage (needs docker)
	@$(MAKE) --no-print-directory clean-coverage
	@mkdir -p coverage
	@$(MAKE) --no-print-directory test COVERPROFILE=coverage/all.out
	@$(MAKE) --no-print-directory coverage-report

clean-coverage: ## Remove the coverage/ directory
	-@rm -r coverage 2>/dev/null; true

coverage-report: ## Merge the profiles found in coverage/ (e.g. downloaded from CI) and report line coverage
	@mkdir -p coverage/report
	@python3 $(COVERAGE_SCRIPT) $$(find coverage -maxdepth 2 -name '*.out' -not -path 'coverage/report/*') \
		--merged coverage/report/merged.out --cobertura coverage/report/cobertura.xml \
		--summary coverage/report/summary.md --top 40 --check-codecov codecov.yml \
		$(if $(COVERAGE_MIN),--min $(COVERAGE_MIN),)

bench: ## Launch the benchmark test
	 $(GOWORK_ENV) $(GOTEST) -tags=bench -bench Benchmark -cpu 2 -run=^$$

## Lint:
lint-install:
	mkdir -p ./bin
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b ./bin

lint-run:
	@test -n "$(MODULE)" || { echo "usage: make lint-module MODULE=<dir>"; exit 1; }
	@echo "→ Linting $(MODULE)"; cd $(MODULE) && $(CURDIR)/bin/golangci-lint run --timeout=5m --config $(CURDIR)/.golangci.yml ./...

lint-module: lint-install lint-run ## Lint one module (usage: make lint-module MODULE=rules)

# Lints every module and fails at the end if any failed (no early stop).
lint: lint-install ## Use golintci-lint on all the modules of the project
	@rc=0; for module in $(ALL_GO_MOD_DIRS); do \
		$(MAKE) --no-print-directory lint-run MODULE=$$module || rc=1; \
	done; exit $$rc

vuln-check: ## Run govulncheck on all modules in the workspace
	@which govulncheck > /dev/null 2>&1 || $(GOCMD) install golang.org/x/vuln/cmd/govulncheck@latest
	@for module in $(ALL_GO_MOD_DIRS); do \
		echo "→ Checking vulnerabilities in $$module"; \
		cd $$module && $(GOWORK_ENV) govulncheck ./... || exit 1; \
		cd - >/dev/null; \
	done

vuln-check-relayproxy: ## Run govulncheck on the relay proxy only
	@which govulncheck > /dev/null 2>&1 || $(GOCMD) install golang.org/x/vuln/cmd/govulncheck@latest
	$(GOWORK_ENV) govulncheck ./cmd/relayproxy/...

## Help:
help: ## Show this help.
	@echo ''
	@echo 'Usage:'
	@echo '  ${YELLOW}make${RESET} ${GREEN}<target>${RESET}'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} { \
		if (/^[a-zA-Z_-]+:.*?##.*$$/) {printf "    ${YELLOW}%-20s${GREEN}%s${RESET}\n", $$1, $$2} \
		else if (/^## .*$$/) {printf "  ${CYAN}%s${RESET}\n", substr($$1,4)} \
		}' $(MAKEFILE_LIST)
