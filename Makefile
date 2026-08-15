.PHONY: build frontend test check clean

BIN_DIR ?= bin

build: frontend
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/nabu ./cmd/nabu
	go build -o $(BIN_DIR)/nabud ./cmd/nabud

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
