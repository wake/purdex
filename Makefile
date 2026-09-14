# Makefile
.PHONY: build test lint clean check-goenv test-goenv

BIN := bin/pdx
HASH := $(shell git log -1 --format=%h 2>/dev/null)
VERSION := $(shell cat VERSION 2>/dev/null)
LDFLAGS := -X github.com/wake/purdex/internal/buildinfo.Hash=$(HASH) -X github.com/wake/purdex/internal/buildinfo.Version=$(VERSION)

check-goenv: ; @sh scripts/check-goenv.sh

test-goenv:
	sh scripts/check-goenv_test.sh

build: check-goenv
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/pdx

test:
	go test -race -count=1 ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
