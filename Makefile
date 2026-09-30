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

.PHONY: all build lint_prep lint lint_fix lint_ci test test_unit clean

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

clean:
	rm -f $(EXE_NAME)
