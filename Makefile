BINARY := apf
PACKAGE := ./cmd/apf
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
DATADIR ?= $(PREFIX)/share/alpineform
DESTDIR ?=
INSTALL ?= install
GOVULNCHECK_VERSION ?= v1.4.0
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

VERSION_PACKAGE := github.com/mofelee/alpineform/internal/version
LDFLAGS := -s -w \
	-X $(VERSION_PACKAGE).Version=$(VERSION) \
	-X $(VERSION_PACKAGE).Commit=$(COMMIT) \
	-X $(VERSION_PACKAGE).Date=$(BUILD_DATE)

.PHONY: build install test test-unit vet format-check vulncheck update-golden check clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PACKAGE)

install: build
	$(INSTALL) -d "$(DESTDIR)$(BINDIR)" "$(DESTDIR)$(DATADIR)"
	$(INSTALL) -m 0755 "$(BINARY)" "$(DESTDIR)$(BINDIR)/apf"
	$(INSTALL) -m 0644 README.md LICENSE NOTICE.md "$(DESTDIR)$(DATADIR)/"

test:
	go test ./...

test-unit:
	go test -race -count=1 ./...

vet:
	go vet ./...

format-check:
	test -z "$$(gofmt -l $$(git ls-files '*.go'))"

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

update-golden:
	UPDATE_GOLDEN=1 go test ./internal/core/plan

check: test-unit vet format-check

clean:
	go clean
	rm -f $(BINARY)
