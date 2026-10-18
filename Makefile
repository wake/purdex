# Makefile
.PHONY: build build-wwd test lint clean check-goenv test-goenv
.DEFAULT_GOAL := build

BIN := bin/pdx
HASH := $(shell git log -1 --format=%h 2>/dev/null)
VERSION := $(shell cat VERSION 2>/dev/null)
LDFLAGS := -X github.com/wake/purdex/internal/buildinfo.Hash=$(HASH) -X github.com/wake/purdex/internal/buildinfo.Version=$(VERSION)

check-goenv: ; @sh scripts/check-goenv.sh

test-goenv:
	sh scripts/check-goenv_test.sh

build: check-goenv
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/pdx

WWD_TARGETS := darwin/arm64 darwin/amd64 linux/amd64

# wwd cross builds: bin/wwd-<os>-<arch> (spec §2). Pure Go, no cgo.
build-wwd:
	@for t in $(WWD_TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		echo "wwd $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o bin/wwd-$$os-$$arch ./cmd/wwd || exit 1; \
	done

test:
	go test -race -count=1 ./...

lint:
	@unformatted=$$(gofmt -l cmd internal); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed (run gofmt -w):"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	# wwd also runs on mserv (Linux): the *_linux.go files are not seen by the vet above
	GOOS=linux GOARCH=amd64 go vet ./internal/wwd/... ./cmd/wwd/

clean:
	rm -rf bin/
