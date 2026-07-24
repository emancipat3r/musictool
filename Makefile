# spotifytool — single static binary, cross-compiled for the homelab.
# CGO_ENABLED=0 is mandatory: the pure-Go SQLite driver keeps the binary static.

BINARY      := spotifytool
PKG         := ./cmd/spotifytool
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)
GO          := go
export CGO_ENABLED=0

.PHONY: all build test vet tidy clean dist run-serve fmt

all: vet test build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

# Cross-compile both homelab targets.
dist: dist/$(BINARY)-linux-amd64 dist/$(BINARY)-linux-arm64

dist/$(BINARY)-linux-amd64:
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $@ $(PKG)

dist/$(BINARY)-linux-arm64:
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $@ $(PKG)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

run-serve: build
	./bin/$(BINARY) serve

clean:
	rm -rf bin dist
