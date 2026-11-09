VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.Version=$(VERSION)
BIN     := bin

.PHONY: all build build-latchd build-relay test test-acme test-ui lint fmt vet tidy clean ui-install ui-build

all: build

build: build-latchd build-relay

build-latchd:
	go build -ldflags '$(LDFLAGS)' -o $(BIN)/latchd ./latchd

build-relay:
	go build -ldflags '$(LDFLAGS)' -o $(BIN)/latch-relay ./latch-relay

test:
	go test -race -count=1 ./...

# test-acme runs the ACME integration test against a local Pebble server
# (https://github.com/letsencrypt/pebble). It is gated behind a build tag and
# excluded from the default `test` target; CI runs it as a separate job.
#   PEBBLE_DIR=https://localhost:14000/dir make test-acme
PEBBLE_DIR ?= https://localhost:14000/dir
test-acme:
	PEBBLE_DIR='$(PEBBLE_DIR)' go test -tags acme_pebble -count=1 ./latch-relay/internal/acme/...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

# UI targets become real in phase 9; they are no-ops while latch-ui is empty.
ui-install:
	@[ -f latch-ui/package.json ] && (cd latch-ui && pnpm install --frozen-lockfile) || echo "latch-ui: nothing to install yet"

ui-build:
	@[ -f latch-ui/package.json ] && (cd latch-ui && pnpm build) || echo "latch-ui: nothing to build yet"

test-ui:
	@[ -f latch-ui/package.json ] && (cd latch-ui && pnpm test) || echo "latch-ui: nothing to test yet"

clean:
	rm -rf $(BIN)
