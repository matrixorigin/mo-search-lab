GO ?= go
PYTHON ?= python3
VERSION ?= v0.14.1
TARGET_OS ?= linux
TARGET_ARCH ?= amd64

.PHONY: all build test vet format-check python-test check release release-full

all: build

build:
	GOWORK=off CGO_ENABLED=0 $(GO) build -mod=readonly -trimpath -ldflags='-s -w -X main.version=$(VERSION)' -o mo-search-lab ./cmd/mo-search-lab

test:
	GOWORK=off $(GO) test -mod=readonly -count=1 -timeout=120s ./...

vet:
	GOWORK=off $(GO) vet -mod=readonly ./...

format-check:
	@test -z "$$(gofmt -l cmd/mo-search-lab)" || { gofmt -l cmd/mo-search-lab; exit 1; }

python-test:
	$(PYTHON) -m unittest discover -s tools -p 'test_*.py' -v

check: format-check vet test python-test

release:
	GO='$(GO)' bash scripts/release.sh '$(VERSION)' '$(TARGET_OS)' '$(TARGET_ARCH)'

# Maintainer-only: dataset bytes remain outside Git; output must be a new directory.
release-full: release
	@test -n "$(PACKS_ROOT)" -a -n "$(OUTPUT_DIR)" || { echo 'set PACKS_ROOT and a new OUTPUT_DIR'; exit 1; }
	$(PYTHON) tools/package_field_bundle.py --version '$(VERSION)' --tool-archive 'dist/mo-search-lab-$(VERSION)-linux-amd64.tar.gz' --packs-root '$(PACKS_ROOT)' --output '$(OUTPUT_DIR)'
