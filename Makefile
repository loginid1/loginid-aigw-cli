export PKG_VERSION?=0.0.0
export EXE_NAME?=aigw
# They apply to `build` only. Make still passes command-line variables to
# every recipe, so the targets that must run on this machine (tests, the
# generator, tool installs) clear them with $(host).
GOOS?=$(shell go env GOOS)
GOARCH?=$(shell go env GOARCH)
host := GOOS= GOARCH=

export GOLINT_VER?=2.12.2
# Built with `go install` at a pinned version, so the Go checksum database
# verifies it, and kept in the project's build/ rather than a shared /tmp,
# where another user could plant a binary at the expected path.
golint_dir := $(CURDIR)/build/bin/golangci-lint-v$(GOLINT_VER)
golint_exe := $(golint_dir)/golangci-lint

commit := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
# Read from go.mod rather than hardcoding, so renaming the module does not
# silently break version injection.
module := $(shell go list -m)
pkg_list := $(shell go list ./... | grep -v /test)

# The devcon spec the API commands are generated from. The ref is pinned so the
# same checkout always generates the same code; bump it on purpose, run
# `make generate`, and review the diff. Set AIGW_SPEC to a local file to skip
# the fetch, e.g. AIGW_SPEC=../loginid-aigw/api/rest/devcon/openapi3.yaml.
AIGW_SPEC_REF ?= 992bd2fd4747fd3d010d721f6c6437bd6049dd43
AIGW_SPEC ?=
spec_project := loginid%2Fsoftware%2Floginid-aigw
# One path for both fetches, so moving the spec cannot break CI alone.
spec_path := api/rest/devcon/openapi3.yaml
spec_file_path := $(subst /,%2F,$(spec_path))
spec_cache := build/devcon-openapi3-$(AIGW_SPEC_REF).yaml

.PHONY: all build lint_prep lint lint_fix lint_ci test test_unit test_coverage clean generate generate_check notices

all: lint test_unit build

# "." rather than ./main.go, so the binary records its module path and VCS
# revision (go version -m).
build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	go build -o $(EXE_NAME) -trimpath --ldflags "-s -w \
	-X $(module)/internal/version.Version=$(PKG_VERSION) \
	-X $(module)/internal/version.GitCommit=$(commit)" .

lint_prep:
	@if [ ! -x $(golint_exe) ]; then \
		$(host) GOBIN=$(golint_dir) \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$(GOLINT_VER); \
	fi

lint: lint_prep
	$(golint_exe) run --timeout 10m ./...

lint_fix: lint_prep
	$(golint_exe) run --fix --timeout 10m ./...

lint_ci: lint_prep
	$(golint_exe) run --timeout 10m --output.code-climate.path gl-code-quality-report.json ./...

test: test_unit

test_unit:
	$(host) CGO_ENABLED=0 \
	go test -count=1 $(pkg_list)
	# The generator sits behind a build tag, so ./... alone never builds it.
	$(host) CGO_ENABLED=0 \
	go test -count=1 -tags gen_commands ./internal/gen/

# CI reads the total from the last line of `go tool cover -func`.
test_coverage:
	$(host) CGO_ENABLED=0 \
	go test -count=1 -coverprofile=coverage.txt $(pkg_list)
	$(host) CGO_ENABLED=0 \
	go test -count=1 -tags gen_commands ./internal/gen/
	$(host) go tool cover -func=coverage.txt | tail -1

# Locally this uses your glab login. In CI it fetches the one commit over git
# with the job token: git is a documented job-token use, the files API is not.
# It works once loginid-aigw lists this project in its job token allowlist.
$(spec_cache):
	@mkdir -p build
ifdef CI_JOB_TOKEN
	rm -rf build/spec-repo && git init -q build/spec-repo
	@echo "git fetch loginid-aigw $(AIGW_SPEC_REF)"
	@git -C build/spec-repo fetch -q --depth 1 \
		"https://gitlab-ci-token:$(CI_JOB_TOKEN)@$(CI_SERVER_HOST)/loginid/software/loginid-aigw.git" $(AIGW_SPEC_REF)
	git -C build/spec-repo show FETCH_HEAD:$(spec_path) > $@.tmp
	rm -rf build/spec-repo
else
	glab api "projects/$(spec_project)/repository/files/$(spec_file_path)/raw?ref=$(AIGW_SPEC_REF)" > $@.tmp
endif
	mv $@.tmp $@

generate: $(if $(AIGW_SPEC),,$(spec_cache))
	$(host) go run -tags gen_commands ./internal/gen \
		-spec $(or $(AIGW_SPEC),$(spec_cache)) \
		-ref $(if $(AIGW_SPEC),local:$(AIGW_SPEC),$(AIGW_SPEC_REF))

# Fails when the checked-in generated code does not match the pinned spec and
# overlay, e.g. after an overlay edit without a regenerate.
generate_check: generate
	git diff --exit-code -- internal/apicmd/operations_gen.go

# Every binary distribution must carry these; include THIRD_PARTY_NOTICES.txt
# in release archives. Re-run after changing dependencies.
notices:
	$(host) ./scripts/third-party-notices.sh THIRD_PARTY_NOTICES.txt

clean:
	rm -f $(EXE_NAME)
	rm -rf build
