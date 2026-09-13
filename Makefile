BINARY  := e
PKG     := ./...
VERSION := 0.0.0
PREFIX  ?= /usr/local
MONOVA  := $(shell which monova 2> /dev/null)
LDFLAGS  = -ldflags="-X main.Version=$(VERSION)"

export PATH := $(PATH):$(shell go env GOPATH)/bin
export CGO_ENABLED := 1

version:
ifdef MONOVA
MONOVA_VERSION := $(shell monova 2>/dev/null)
ifneq ($(MONOVA_VERSION),)
override VERSION = $(MONOVA_VERSION)
override LDFLAGS = -ldflags="-X main.Version=$(VERSION)"
endif
else
	$(info "Install monova with: grm install jsnjack/monova")
endif

test:
	go test $(PKG)

test-ui:
	E_UI_RUNTIME_TEST=1 go test ./internal/ui -run TestEditorRuntimeSmoke -count=1
	E_PREVIEW_RUNTIME_TEST=1 go test ./internal/markdownpreview -run TestWebKitRuntime -count=1

vet:
	go vet $(PKG)

fmt:
	@command -v goimports >/dev/null 2>&1 || { \
	  echo "goimports is not installed. Install it with:"; \
	  echo "  go install golang.org/x/tools/cmd/goimports@latest"; \
	  exit 1; \
	}
	goimports -w .

lint: vet
	@command -v golangci-lint >/dev/null 2>&1 || { \
	  echo "golangci-lint is not installed. Install it with:"; \
	  echo "  grm install golangci/golangci-lint"; \
	  exit 1; \
	}
	golangci-lint run

check: fmt vet build test lint
	@echo "==> make check: all green"

standards:
	curl -sL https://raw.githubusercontent.com/jsnjack/standards/master/AGENTS.universal.md -o AGENTS.universal.md
	curl -sL https://raw.githubusercontent.com/jsnjack/standards/master/AGENTS.go.md -o AGENTS.go.md

bin/$(BINARY): version
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/e
	ln -sf bin/$(BINARY) $(BINARY)

build: bin/$(BINARY)

run: build
	./bin/$(BINARY)

install: build
	install -Dm755 bin/$(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	install -Dm644 data/com.jsnjack.e.desktop $(DESTDIR)$(PREFIX)/share/applications/com.jsnjack.e.desktop
	install -Dm644 data/com.jsnjack.e.metainfo.xml $(DESTDIR)$(PREFIX)/share/metainfo/com.jsnjack.e.metainfo.xml
	install -Dm644 data/icons/scalable/apps/com.jsnjack.e.svg $(DESTDIR)$(PREFIX)/share/icons/hicolor/scalable/apps/com.jsnjack.e.svg

clean:
	rm -rf bin/ $(BINARY)

.PHONY: version build run install test test-ui vet fmt lint check standards clean
