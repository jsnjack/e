BINARY  := e
PKG     := ./...
VERSION := 0.0.0
PREFIX  ?= /usr/local
MONOVA  := $(shell which monova 2> /dev/null)
LDFLAGS  = -ldflags="-X main.Version=$(VERSION)"
APPID   := com.jsnjack.e
# RPM and release need a concrete version even before any git tag exists.
RPMVERSION := $(shell monova 2>/dev/null || echo 0.0.0)
TODAY      := $(shell date +%Y-%m-%d)
DIST       := dist
ARCH       := $(shell go env GOARCH)
RELDIR     := $(BINARY)-$(RPMVERSION)-linux-$(ARCH)

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

# GTK tests run in a headless Wayland session; E_TEST_VISIBLE=1 shows them.
test-ui:
	E_UI_RUNTIME_TEST=1 scripts/headless.sh go test ./internal/ui -run 'TestEditorRuntimeSmoke|TestProjectTreeRuntime' -count=1
	E_PREVIEW_RUNTIME_TEST=1 scripts/headless.sh go test ./internal/markdownpreview -run TestWebKitRuntime -count=1

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

install: build data/$(APPID).metainfo.xml
	install -Dm755 bin/$(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	install -Dm644 data/$(APPID).desktop $(DESTDIR)$(PREFIX)/share/applications/$(APPID).desktop
	install -Dm644 data/$(APPID).metainfo.xml $(DESTDIR)$(PREFIX)/share/metainfo/$(APPID).metainfo.xml
	install -Dm644 data/icons/scalable/apps/$(APPID).svg $(DESTDIR)$(PREFIX)/share/icons/hicolor/scalable/apps/$(APPID).svg

# The metainfo is generated from the template so the newest release entry
# carries the current monova version and date. It regenerates on every run
# because the version changes with each commit, not with the template.
data/$(APPID).metainfo.xml: data/$(APPID).metainfo.xml.in
	sed -e 's/@VERSION@/$(RPMVERSION)/' -e 's/@DATE@/$(TODAY)/' $< > $@

# rpmbuild packages the binary built by the build target; it does not compile Go.
rpm: packaging/$(BINARY).spec build data/$(APPID).metainfo.xml
	@command -v rpmbuild >/dev/null 2>&1 || { echo "rpmbuild is not installed. Install it with: sudo dnf install rpm-build"; exit 1; }
	rpmbuild -bb --define "_topdir $(CURDIR)/rpmbuild" \
	    --define "srcdir $(CURDIR)" \
	    --define "appversion $(RPMVERSION)" packaging/$(BINARY).spec
	@echo "==> RPM(s):"; find rpmbuild/RPMS -name '*.rpm' 2>/dev/null

# The tarball links against the system GTK stack, so it still needs those
# libraries; the RPM declares them and is the real install.
release-artifacts: build rpm
	rm -rf $(DIST)
	mkdir -p $(DIST)/$(RELDIR)
	cp bin/$(BINARY) data/$(APPID).desktop data/$(APPID).metainfo.xml \
	    data/icons/scalable/apps/$(APPID).svg LICENSE README.md $(DIST)/$(RELDIR)/
	tar -C $(DIST) -czf $(DIST)/$(RELDIR).tar.gz $(RELDIR)
	rm -rf $(DIST)/$(RELDIR)
	@find rpmbuild/RPMS -name '$(BINARY)-$(RPMVERSION)-*.rpm' ! -name '*debug*' -exec cp {} $(DIST)/ \;
	@echo "==> release artifacts in $(DIST)/:"; ls -1 $(DIST)

# release publishes a GitHub release tagged v<monova version> with grm and
# uploads the tarball and RPM. grm gets or creates the release, so a re-run
# resumes a partial upload. Push the commit to tag first.
release: release-artifacts
	@command -v grm >/dev/null 2>&1 || { echo "grm is not installed. Install it with: grm install jsnjack/grm"; exit 1; }
	@args=""; for f in $(DIST)/*.tar.gz $(DIST)/*.rpm; do args="$$args -f $$f"; done; \
	  echo "==> grm release jsnjack/$(BINARY)$$args -t v$(RPMVERSION)"; \
	  grm release jsnjack/$(BINARY) $$args -t "v$(RPMVERSION)"
	@echo "==> released v$(RPMVERSION)"

clean:
	rm -rf bin/ rpmbuild/ $(DIST) $(BINARY) data/$(APPID).metainfo.xml

.PHONY: version build run install test test-ui vet fmt lint check standards clean \
	rpm release release-artifacts data/$(APPID).metainfo.xml
