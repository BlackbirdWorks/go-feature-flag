GOCMD=go
DOCKERCMD=docker
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
ALL_GO_MOD_DIRS := ./modules/core ./

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

.PHONY: all test build vendor fuzz

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

build-doc: ## Build the documentation
	cd website; \
	npm i && npm run build

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
	git diff --exit-code --quiet go.mod go.sum modules/core/go.mod modules/core/go.sum

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

watch-doc: ## Launch a local server to work on the documentation
	cd website; \
    	npm i && npx docusaurus start

serve-doc: ## Serve the doc build by the build-doc target
	cd website; \
		npm run serve

swagger: ## Build swagger documentation
	$(GOWORK_ENV) $(GOCMD) install github.com/swaggo/swag/cmd/swag@latest
	cd cmd/relayproxy && swag init --parseDependency --parseDepth=1 --parseInternal --markdownFiles docs

generate-helm-docs: ## Generates helm documentation for the project
	$(GOWORK_ENV) $(GOCMD) install github.com/norwoodj/helm-docs/cmd/helm-docs@latest
	helm-docs

## Test:
test: ## Run the tests of the project
	@for module in $(ALL_GO_MOD_DIRS); do \
		echo "→ Testing $$module"; \
		cd $$module && CGO_ENABLED=1 GO111MODULE=on $(GOWORK_ENV) $(GOCMD) test -v -race -tags=docker ./... || exit 1;  \
		cd - >/dev/null; \
	done

fuzz: ## Run every fuzz test for a bounded time (usage: make fuzz FUZZTIME=10s)
	@FUZZTIME="$(or $(FUZZTIME),10s)" GOWORK_ENV="$(GOWORK_ENV)" GOCMD="$(GOCMD)" .github/ci-scripts/fuzz.sh $(ALL_GO_MOD_DIRS)

provider-tests: ## Run the integration tests for the Open Feature Providers
	./openfeature/provider_tests/integration_tests.sh

coverage: ## Run the tests of the project and export the coverage for all modules
	@rm -f coverage*.cov coverage*.cov.tmp
	@for module in $(ALL_GO_MOD_DIRS); do \
		echo "Running coverage for $$module..."; \
		export original_path=$(shell pwd); \
		covfile="coverage-$$(basename $$module).cov.tmp"; \
		cd $$module && $(GOWORK_ENV) $(GOTEST) -cover -covermode=count -tags=docker -coverprofile=$${original_path}/$${covfile} ./... || exit 1; \
		cd $$original_path; \
	done
	@echo "mode: count" > coverage.cov && cat *.cov.tmp | grep -v "mode: count" | grep -v "/examples/" >> coverage.cov
	@rm -f *.cov.tmp

bench: ## Launch the benchmark test
	 $(GOWORK_ENV) $(GOTEST) -tags=bench -bench Benchmark -cpu 2 -run=^$$

## Lint:
lint: ## Use golintci-lint on all the modules of the project
	mkdir -p ./bin
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b ./bin
	@$(foreach module, $(ALL_GO_MOD_DIRS), (echo "→ Linting $(module)"; cd $(module) && $(CURDIR)/bin/golangci-lint run --timeout=5m --config $(CURDIR)/.golangci.yml ./...) &&) true

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
