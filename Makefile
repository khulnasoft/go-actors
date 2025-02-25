TEMP_DIR := ./.tmp

# Command templates #################################
GOIMPORTS_CMD := $(TEMP_DIR)/gosimports -local github.com/khulnasoft

# Tool versions #################################
GOSIMPORTS_VERSION := v0.3.8
GOLICENSES_VERSION := v5.0.1

# Formatting variables #################################
BOLD := $(shell tput -T linux bold)
PURPLE := $(shell tput -T linux setaf 5)
GREEN := $(shell tput -T linux setaf 2)
CYAN := $(shell tput -T linux setaf 6)
RED := $(shell tput -T linux setaf 1)
RESET := $(shell tput -T linux sgr0)
TITLE := $(BOLD)$(PURPLE)
SUCCESS := $(BOLD)$(GREEN)

# Test variables #################################

## Build variables #################################
VERSION := $(shell git describe --dirty --always --tags)
DIST_DIR := ./dist
SNAPSHOT_DIR := ./snapshot
CHANGELOG := CHANGELOG.md
OS := $(shell uname | tr '[:upper:]' '[:lower:]')

ifndef VERSION
	$(error VERSION is not set)
endif

define title
    @printf '$(TITLE)$(1)$(RESET)\n'
endef

define safe_rm_rf
	bash -c 'test -z "$(1)" && false || rm -rf $(1)'
endef

define safe_rm_rf_children
	bash -c 'test -z "$(1)" && false || rm -rf $(1)/*'
endef

.DEFAULT_GOAL:=help


.PHONY: all
all: static-analysis test ## Run all linux-based checks (linting, license check, unit, integration, and linux compare tests)
	@printf '$(SUCCESS)All checks pass!$(RESET)\n'

.PHONY: static-analysis
static-analysis: check-go-mod-tidy lint check-licenses  ## Run all static analysis checks

.PHONY: test
test: unit ## Run all tests (currently unit, integration, linux compare, and cli tests)


## Bootstrapping targets #################################

.PHONY: bootstrap
bootstrap: $(TEMP_DIR) bootstrap-go bootstrap-tools ## Download and install all tooling dependencies (+ prep tooling in the ./tmp dir)
	$(call title,Bootstrapping dependencies)

.PHONY: bootstrap-tools
bootstrap-tools: $(TEMP_DIR)
	GO111MODULE=on GOBIN=$(realpath $(TEMP_DIR)) go get -u golang.org/x/perf/cmd/benchstat
	curl -sSfL https://raw.githubusercontent.com/khulnasoft/go-licenses/master/golicenses.sh | sh -s -- -b $(TEMP_DIR)/ $(GOLICENSES_VERSION)
	GOBIN="$(realpath $(TEMP_DIR))" go install github.com/rinchsan/gosimports/cmd/gosimports@$(GOSIMPORTS_VERSION)

.PHONY: bootstrap-go
bootstrap-go:
	go mod download

$(TEMP_DIR):
	mkdir -p $(TEMP_DIR)


## Static analysis targets #################################

.PHONY: lint
lint:  ## Run gofmt + golangci lint checks
	$(call title,Running linters)
	# ensure there are no go fmt differences
	@printf "files with gofmt issues: [$(shell gofmt -l -s .)]\n"
	@test -z "$(shell gofmt -l -s .)"

	# run all golangci-lint rules
	$(LINT_CMD)
	@[ -z "$(shell $(GOIMPORTS_CMD) -d .)" ] || (echo "goimports needs to be fixed" && false)

	# go tooling does not play well with certain filename characters, ensure the common cases don't result in future "go get" failures
	$(eval MALFORMED_FILENAMES := $(shell find . | grep -e ':'))
	@bash -c "[[ '$(MALFORMED_FILENAMES)' == '' ]] || (printf '\nfound unsupported filename characters:\n$(MALFORMED_FILENAMES)\n\n' && false)"

.PHONY: format
format:  ## Auto-format all source code + run golangci lint fixers
	$(call title,Running formatting)
	gofmt -w -s .
	$(GOIMPORTS_CMD) -w .
	go mod tidy

.PHONY: check-licenses
check-licenses:  ## Ensure transitive dependencies are compliant with the current license policy
	$(call title,Checking for license compliance)
	$(TEMP_DIR)/golicenses check ./...

check-go-mod-tidy:
	@ .github/scripts/go-mod-tidy-check.sh && echo "go.mod and go.sum are tidy!"


## Testing targets #################################

.PHONY: unit
unit: $(TEMP_DIR)  ## Run unit tests (with coverage)
	$(call title,Running unit tests)
	go test -coverprofile $(TEMP_DIR)/unit-coverage-details.txt $(shell go list ./... | grep -v anchore/syft/test)
	@.github/scripts/coverage.py $(COVERAGE_THRESHOLD) $(TEMP_DIR)/unit-coverage-details.txt


## Halp! #################################

.PHONY: help
help:  ## Display this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "$(BOLD)$(CYAN)%-25s$(RESET)%s\n", $$1, $$2}'

test: build
	go test ./... -count=1 --race --timeout=5s

proto:
	protoc --go_out=. --go-vtproto_out=.  --go_opt=paths=source_relative --proto_path=. actor/actor.proto

build:
	go build -o bin/goactors examples/goactors/main.go 
	go build -o bin/hooks examples/middleware/hooks/main.go 
	go build -o bin/childprocs examples/childprocs/main.go 
	go build -o bin/request examples/request/main.go 
	go build -o bin/restarts examples/restarts/main.go 
	go build -o bin/eventstream examples/eventstream/main.go 
	go build -o bin/tcpserver examples/tcpserver/main.go 
	go build -o bin/metrics examples/metrics/main.go
	go build -o bin/chatserver examples/chat/server/main.go
	go build -o bin/chatclient examples/chat/client/main.go
	go build -o bin/cluster_member_1 examples/cluster/member_1/main.go
	go build -o bin/cluster_member_2 examples/cluster/member_2/main.go

bench:
	go run ./_bench/.

bench-profile:
	go test -bench='^BenchmarkGoactors$$' -run=NONE -cpuprofile cpu.prof -memprofile mem.prof ./_bench

.PHONY: proto
