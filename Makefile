# Makefile
.PHONY: build test lint clean

BIN := bin/pdx
HASH := $(shell git log -1 --format=%h 2>/dev/null)
LDFLAGS := -X github.com/wake/purdex/internal/module/dev.BakedInHash=$(HASH)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/pdx

test:
	go test -race -count=1 ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
