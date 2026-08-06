# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Common commands

```sh
go test ./...                                  # full Go suite
go test ./internal/mcp -run TestCreateExpense  # single test
go vet ./... && gofmt -l .
go build ./cmd/spliit-mcp                      # produces ./spliit-mcp
make test-postgres                             # store tests against a throwaway Postgres
```

Frontend (always invoke from the repo root with `--prefix`, not `cd web`):

```sh
npm --prefix web ci
npm --prefix web run dev         # Vite on :5173, proxies /api and /auth to :8080
npm --prefix web run typecheck
npm --prefix web run build       # outputs web/dist
```

## The fact that drives the design

Spliit's tRPC API has **no authentication**. `createTRPCContext` returns `{}` and
every procedure is a bare `baseProcedure`, so the **group ID is the only
credential**. `groups.list` even takes the group IDs as input — Spliit does not
know which groups belong to whom.

Two consequences:

1. This server stores group IDs on users' behalf; they are secrets, not
   identifiers. That is what the config page manages.
2. **`store.ResolveGroup` is the authorization boundary.** It always filters by
   the calling user's `sub`. If a tool ever reaches Spliit with a group ID that
   did not come from the caller's own rows, OIDC has been bypassed entirely.
   `TestToolsRefuseAnotherUsersGroup` is the regression test for this — keep it
   passing.

## Architecture

```
cmd/spliit-mcp     cobra root: `serve`, `migrate`
internal/config    viper; SPLIIT_MCP_* env overrides
internal/db        sqlx; dialect chosen from the DSN; embedded migrations
internal/store     all queries; ErrNotFound / ErrConflict
internal/oidc      relying party (web login) + bearer verification (MCP)
internal/spliit    wrapper over go.chrastecky.dev/spliit-api
internal/mcp       tool definitions and handlers
internal/handlers  config JSON API, auth routes, SPA serving
web/               Vite + React 18 SPA
```

### Dual database

One `sqlx.DB`. A `postgres://` DSN uses `lib/pq`; anything else is a SQLite path
handled by `modernc.org/sqlite` (pure Go, no cgo).

**Write every query with `?` placeholders and run it through `db.Rebind`.** Never
write `$1` — it breaks SQLite. `store.rebind` does this for you. The two
migration sets under `internal/db/migrations/{sqlite,postgres}` must stay in
lockstep; the store tests run against both when
`SPLIIT_MCP_TEST_POSTGRES_DSN` is set.

SQLite timestamp columns are declared `TIMESTAMP` (not `TEXT`) so the driver
converts them to `time.Time`.

### Two auth surfaces

The config UI is an OIDC relying party producing a session cookie. The `/mcp`
endpoint is an OAuth protected resource: `auth.RequireBearerToken` wraps the
streamable handler in `cmd/spliit-mcp/main.go`, and the verified subject reaches
tools via `req.Extra.TokenInfo.UserID`. Tokens are audience-checked against
`cfg.MCPResourceURL()` unless `oidc.skip_audience_check` is set.

### Money

Spliit stores integer minor units (`amount.Amount`). Tools accept and return
**decimal strings**, never floats. Convert with `spliit.ToAmount`, which shifts
and rounds — the upstream `amount.FromDecimal` demands an exponent of exactly
`-2` and would reject a plain `10.5`.

### "You"

A user has one `display_name`; each group pins a `participant_id`. On add, the
API suggests a participant only when exactly one name matches
case-insensitively, otherwise the SPA makes the user pick. Spliit mints a fresh
participant ID when one is removed and re-added, so a pinned ID can go stale —
handlers detect this and return an error pointing at the config page rather than
guessing.

### Tool errors

Return `toolError(err)`, not a Go error. These become `IsError` results so the
model sees the message and can correct itself; a protocol error would abort the
turn. Error text should name the valid options (see `findByName`).

## Testing

`internal/mcp/tools_test.go` runs the real MCP stack over HTTP against a fake
Spliit tRPC endpoint, with a stub verifier where **the bearer token is the
subject** — so `env.connect(t, "alice")` is an authenticated session as `alice`.
`fakeSpliit.results` maps a procedure name to its canned payload; note the
responses are the tRPC-shaped ones (`groups.get` returns `{"group": {...}}`).
