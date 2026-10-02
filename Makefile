export PKG_VERSION?=0.0.0
export EXE_NAME?=aigw
export GOOS?=$(shell go env GOOS)
export GOARCH?=$(shell go env GOARCH)
export GOPRIVATE:=gitlab.com/loginid

export GOLINT_VER?=2.12.2
golint_name := golangci-lint-v$(GOLINT_VER)
golint_exe := /tmp/$(golint_name)

commit := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
# Read from go.mod rather than hardcoding, so renaming the module does not
# silently break version injection.
module := $(shell go list -m)
pkg_list := $(shell go list ./... | grep -v /test)

# The devcon spec the API commands are generated from. The ref is pinned so the
# same checkout always generates the same code; bump it on purpose, run
# `make generate`, and review the diff. Set AIGW_SPEC to a local file to skip
# the fetch, e.g. AIGW_SPEC=../loginid-aigw/api/rest/devcon/openapi3.yaml.
AIGW_SPEC_REF ?= d05caee804f7abbac4981631164a22d3643c98ca
AIGW_SPEC ?=
spec_project := loginid%2Fsoftware%2Floginid-aigw
spec_file_path := api%2Frest%2Fdevcon%2Fopenapi3.yaml
spec_cache := build/devcon-openapi3-$(AIGW_SPEC_REF).yaml

.PHONY: all build lint_prep lint lint_fix lint_ci test test_unit clean generate generate_check

all: lint test_unit build

build:
	CGO_ENABLED=0 \
	go build -o $(EXE_NAME) -a -trimpath --ldflags "-s -w \
	-X $(module)/internal/version.Version=$(PKG_VERSION) \
	-X $(module)/internal/version.GitCommit=$(commit)" ./main.go

lint_prep:
	@if [ ! -f $(golint_exe) ]; then \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b /tmp v$(GOLINT_VER); \
		mv /tmp/golangci-lint $(golint_exe); \
	fi

lint: lint_prep
	$(golint_exe) run --timeout 10m ./...

lint_fix: lint_prep
	$(golint_exe) run --fix --timeout 10m ./...

lint_ci: lint_prep
	$(golint_exe) run --timeout 10m --output.code-climate.path gl-code-quality-report.json ./...

test: test_unit

test_unit:
	CGO_ENABLED=0 \
	go test -count=1 $(pkg_list)

$(spec_cache):
	@mkdir -p build
	glab api "projects/$(spec_project)/repository/files/$(spec_file_path)/raw?ref=$(AIGW_SPEC_REF)" > $@.tmp
	mv $@.tmp $@

generate: $(if $(AIGW_SPEC),,$(spec_cache))
	go run -tags gen_commands ./internal/gen \
		-spec $(or $(AIGW_SPEC),$(spec_cache)) \
		-ref $(if $(AIGW_SPEC),local:$(AIGW_SPEC),$(AIGW_SPEC_REF))

# Fails when the checked-in generated code does not match the pinned spec and
# overlay, e.g. after an overlay edit without a regenerate.
generate_check: generate
	git diff --exit-code -- internal/apicmd/operations_gen.go

clean:
	rm -f $(EXE_NAME)
	rm -rf build
