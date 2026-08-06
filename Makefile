.PHONY: dev backend frontend test build lint clean

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

clean:
	rm -f spliit-mcp spliit-mcp.db
	rm -rf web/dist
