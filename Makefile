# Spicrawl CLI (the spicrawl command). `make` on its own prints this list.

.DEFAULT_GOAL := help
.PHONY: help build test lint fmt install snapshot npm spec schema clean

PKG      := github.com/Spicrawl/cli
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
LDFLAGS  := -s -w -X $(PKG)/internal/api.Version=$(VERSION)
GOFLAGS  := -trimpath
BIN      := bin/spicrawl

## help: list the available targets
help:
	@echo "Spicrawl CLI (spicrawl)"
	@echo ""
	@echo "Targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) \
		| sed -e 's/^## //' \
		| awk -F': ' '{ printf "  \033[1m%-10s\033[0m %s\n", $$1, $$2 }'

## build: build ./bin/spicrawl, version from git describe
build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/spicrawl

## test: run the unit tests
test:
	go test ./...

## lint: go vet, and fail if any file is not gofmt'd
lint:
	go vet ./...
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

## fmt: gofmt every file in place
fmt:
	gofmt -w .

INSTALL_BIN := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)

## install: install spicrawl into GOBIN (or GOPATH/bin)
install:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(INSTALL_BIN)/spicrawl ./cmd/spicrawl

## snapshot: goreleaser snapshot build into dist/ (uploads nothing)
snapshot:
	@command -v goreleaser >/dev/null 2>&1 || { echo "goreleaser not installed: https://goreleaser.com/install/"; exit 1; }
	goreleaser release --snapshot --clean

## spec: refresh openapi.yaml from https://docs.spicrawl.com/openapi.yaml
spec:
	curl -fsSL https://docs.spicrawl.com/openapi.yaml -o openapi.yaml

## npm: lay out the single npm package in dist/npm/cli/ from a goreleaser build
npm:
	@scripts/build-npm.sh

## schema: regenerate the embedded JSON Schemas (go generate ./internal/schema/)
schema:
	go generate ./internal/schema/

## clean: remove bin/ and dist/
clean:
	rm -rf bin dist
