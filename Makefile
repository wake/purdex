# Makefile
.PHONY: build webui build-embed test lint clean

BIN := bin/pdx
HASH := $(shell git log -1 --format=%h 2>/dev/null)
LDFLAGS := -X github.com/wake/purdex/internal/module/dev.BakedInHash=$(HASH)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/pdx

# webui: build the SPA and stage it into the embed dir so a subsequent
# `go build` bakes the real app instead of the tracked placeholder.
# Requires the SPA toolchain (pnpm/node). NOTE: this overwrites the tracked
# placeholder internal/webui/dist/index.html locally — do NOT commit the
# resulting built dist (assets are gitignored; leave index.html unstaged).
webui:
	cd spa && pnpm run build
	rm -rf internal/webui/dist
	mkdir -p internal/webui/dist
	cp -r spa/dist/. internal/webui/dist/

# build-embed: production single-binary with the real SPA embedded.
build-embed: webui build

test:
	go test -race -count=1 ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
