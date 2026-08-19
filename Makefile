.PHONY: build frontend test check clean

BIN_DIR ?= bin

# Stamp the same fields the release pipeline stamps, so a local build reports
# something meaningful instead of claiming to be a release.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
MODULE  := github.com/nabu-sh/nabu
LDFLAGS := -X $(MODULE)/internal/version.Version=$(VERSION) -X $(MODULE)/internal/version.Commit=$(COMMIT)

build: frontend
	mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/nabu ./cmd/nabu
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/nabud ./cmd/nabud

frontend:
	cd frontend && npm ci && npm run build
	rm -rf webassets/dist
	mkdir -p webassets/dist
	cp -R frontend/dist/. webassets/dist/

test:
	go test ./...
	cd frontend && npm test -- --run

check:
	go vet ./...
	cd frontend && npm run lint && npm run typecheck

clean:
	rm -rf $(BIN_DIR) frontend/dist webassets/dist
	mkdir -p webassets/dist
	touch webassets/dist/.gitkeep
