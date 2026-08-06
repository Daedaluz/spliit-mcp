.PHONY: dev backend frontend test test-postgres build lint clean \
	docker docker-push compose-up compose-down

# Backend on :8080 + Vite on :5173 (proxying /api and /auth). Ctrl-C kills both.
dev:
	@trap 'kill 0' EXIT; \
	set -a && [ -f .env ] && . ./.env; set +a; \
	go run ./cmd/spliit-mcp serve & \
	npm --prefix web run dev & \
	wait

backend:
	set -a && [ -f .env ] && . ./.env; set +a; go run ./cmd/spliit-mcp serve

frontend:
	npm --prefix web run dev

test:
	go test ./...
	npm --prefix web run typecheck

# Also exercise the Postgres migrations and Rebind path, not just SQLite.
test-postgres:
	docker run -d --rm --name spliit-mcp-pgtest \
		-e POSTGRES_PASSWORD=test -e POSTGRES_DB=spliitmcp -p 55432:5432 postgres:16-alpine
	@until docker exec spliit-mcp-pgtest pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
	-SPLIIT_MCP_TEST_POSTGRES_DSN="postgres://postgres:test@localhost:55432/spliitmcp?sslmode=disable" \
		go test ./internal/store/ -count=1
	docker stop spliit-mcp-pgtest

lint:
	go vet ./...
	gofmt -l . | tee /dev/stderr | (! read)

build:
	go build -o spliit-mcp ./cmd/spliit-mcp
	npm --prefix web run build

# Multi-arch images. The Go binary is pure Go (modernc SQLite, CGO off), so the
# build stage stays on the native platform and cross-compiles — far faster than
# emulating the toolchain under QEMU.
PLATFORMS ?= linux/amd64,linux/arm64
REGISTRY  ?= ghcr.io/daedaluz
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
REVISION  ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
CREATED   ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

BUILD_ARGS = --build-arg VERSION=$(VERSION) \
             --build-arg REVISION=$(REVISION) \
             --build-arg CREATED=$(CREATED)

# Builds without pushing, to check both architectures compile.
docker:
	docker buildx build --platform $(PLATFORMS) $(BUILD_ARGS) \
		-f Dockerfile.backend -t $(REGISTRY)/spliit-mcp:$(VERSION) .
	docker buildx build --platform $(PLATFORMS) $(BUILD_ARGS) \
		-f Dockerfile.frontend -t $(REGISTRY)/spliit-mcp-frontend:$(VERSION) .

# A multi-arch manifest cannot be loaded into the local daemon, so this pushes.
docker-push:
	docker buildx build --platform $(PLATFORMS) $(BUILD_ARGS) --push \
		-f Dockerfile.backend -t $(REGISTRY)/spliit-mcp:$(VERSION) .
	docker buildx build --platform $(PLATFORMS) $(BUILD_ARGS) --push \
		-f Dockerfile.frontend -t $(REGISTRY)/spliit-mcp-frontend:$(VERSION) .

compose-up:
	docker compose -f compose.dev.yml up --build

compose-down:
	docker compose -f compose.dev.yml down

clean:
	rm -f spliit-mcp spliit-mcp.db
	rm -rf web/dist
