BINARY := apf
PACKAGE := ./cmd/apf
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
DATADIR ?= $(PREFIX)/share/alpineform
DESTDIR ?=
INSTALL ?= install
GOVULNCHECK_VERSION ?= v1.4.0
ALPINE_BRANCH ?= v3.24
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
DOCS_CHECK_ARGS ?=
DOCUMENTATION_FILES := README.md README.zh-CN.md LICENSE NOTICE.md NOTICE.zh-CN.md SECURITY.md SECURITY.zh-CN.md CHANGELOG.md CHANGELOG.zh-CN.md

VERSION_PACKAGE := github.com/mofelee/alpineform/internal/version
LDFLAGS := -s -w \
	-X $(VERSION_PACKAGE).Version=$(VERSION) \
	-X $(VERSION_PACKAGE).Commit=$(COMMIT) \
	-X $(VERSION_PACKAGE).Date=$(BUILD_DATE)

.PHONY: build install docs-check test test-unit test-docs test-installer test-release-layout test-integration test-integration-case test-integration-layout vet format-check vulncheck update-golden check clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PACKAGE)

install: build
	@set -eu; \
	bin_dir="$(DESTDIR)$(BINDIR)"; \
	bin_target="$$bin_dir/apf"; \
	data_dir="$(DESTDIR)$(DATADIR)"; \
	data_dir="$${data_dir%/}"; \
	case "$$data_dir" in \
		""|/|.) echo "refusing unsafe data directory: $$data_dir" >&2; exit 1;; \
		*/*) :;; \
		*) echo "data directory must include a parent path: $$data_dir" >&2; exit 1;; \
	esac; \
	test ! -d "$$bin_target" || { echo "install target is a directory: $$bin_target" >&2; exit 1; }; \
	data_parent="$${data_dir%/*}"; \
	test -n "$$data_parent" || data_parent=/; \
	$(INSTALL) -d "$$bin_dir" "$$data_parent"; \
	data_stage=""; data_backup=""; binary_stage=""; binary_backup=""; \
	data_had=0; data_published=0; binary_had=0; binary_published=0; committed=0; \
	cleanup() { \
		trap '' HUP INT TERM; \
		if test "$$committed" = 0; then \
			if test "$$binary_published" = 1; then rm -f "$$bin_target"; fi; \
			if test "$$binary_had" = 1 && { test -e "$$binary_backup" || test -L "$$binary_backup"; }; then \
				mv "$$binary_backup" "$$bin_target"; \
			fi; \
			if test "$$data_published" = 1; then rm -rf "$$data_dir"; fi; \
			if test "$$data_had" = 1 && { test -e "$$data_backup" || test -L "$$data_backup"; }; then \
				mv "$$data_backup" "$$data_dir"; \
			fi; \
		fi; \
		test -z "$$data_stage" || rm -rf "$$data_stage"; \
		test -z "$$data_backup" || rm -rf "$$data_backup"; \
		test -z "$$binary_stage" || rm -f "$$binary_stage"; \
		test -z "$$binary_backup" || rm -f "$$binary_backup"; \
	}; \
	trap cleanup EXIT; \
	trap 'exit 1' HUP INT TERM; \
	data_stage="$$(mktemp -d "$${data_dir}.stage.XXXXXX")"; \
	data_backup="$${data_stage}.backup"; \
	binary_stage="$$(mktemp "$${bin_dir}/.apf.stage.XXXXXX")"; \
	binary_backup="$${binary_stage}.backup"; \
	$(INSTALL) -d "$$data_stage/docs" "$$data_stage/examples"; \
	$(INSTALL) -m 0644 $(DOCUMENTATION_FILES) "$$data_stage/"; \
	$(INSTALL) -m 0644 scripts/documentation-package-files.txt \
		"$$data_stage/documentation-package-files.txt"; \
	cp -R docs/. "$$data_stage/docs/"; \
	cp -R examples/. "$$data_stage/examples/"; \
	scripts/check-documentation-package.sh tree "$$data_stage" >/dev/null; \
	$(INSTALL) -m 0755 "$(BINARY)" "$$binary_stage"; \
	trap '' HUP INT TERM; \
	if test -e "$$data_dir" || test -L "$$data_dir"; then \
		mv "$$data_dir" "$$data_backup"; data_had=1; \
	fi; \
	mv "$$data_stage" "$$data_dir"; data_stage=""; data_published=1; \
	if test -e "$$bin_target" || test -L "$$bin_target"; then \
		mv "$$bin_target" "$$binary_backup"; binary_had=1; \
	fi; \
	mv "$$binary_stage" "$$bin_target"; binary_stage=""; binary_published=1; \
	committed=1; \
	rm -rf "$$data_backup"; data_backup=""; \
	rm -f "$$binary_backup"; binary_backup=""; \
	trap - EXIT HUP INT TERM

docs-check:
	python3 scripts/check-docs.py $(DOCS_CHECK_ARGS)

test:
	go test ./...

test-unit:
	go test -race -count=1 ./...

test-docs:
	python3 scripts/test-check-docs.py

test-installer:
	scripts/test-install.sh

test-release-layout:
	scripts/validate-release.sh

test-integration:
	APF_INTEGRATION_ALPINE_BRANCH="$(ALPINE_BRANCH)" APF_INTEGRATION_DISABLE_KVM="$(INTEGRATION_DISABLE_KVM)" test/integration/libvirt/run.sh

test-integration-case:
	test -n "$(CASE)"
	APF_INTEGRATION_ALPINE_BRANCH="$(ALPINE_BRANCH)" APF_INTEGRATION_CASE="$(CASE)" APF_INTEGRATION_DISABLE_KVM="$(INTEGRATION_DISABLE_KVM)" test/integration/libvirt/run.sh

test-integration-layout:
	test/integration/libvirt/validate-cases.sh

vet:
	go vet ./...

format-check:
	test -z "$$(gofmt -l $$(git ls-files '*.go'))"

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

update-golden:
	UPDATE_GOLDEN=1 go test ./internal/core/plan

check: docs-check test-docs test-unit vet format-check test-integration-layout test-release-layout

clean:
	go clean
	rm -f $(BINARY)
