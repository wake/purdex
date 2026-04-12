# Makefile
.PHONY: build test lint clean

BIN := bin/pdx

build:
	go build -o $(BIN) ./cmd/pdx

test:
	go test -race -count=1 ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
