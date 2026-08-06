# spliit-mcp

An MCP server for [Spliit](https://github.com/spliit-app/spliit), the open-source
expense-splitting app, with OIDC authorization and a web page for managing which
groups are available and which participant is *you*.

## Why the config page exists

Spliit's tRPC API has **no authentication at all**. Its tRPC context is empty
(`src/trpc/init.ts`) and every procedure is public, so **the group ID is the only
credential** — anyone holding one has full read/write access to that group. On
top of that, `groups.list` takes the group IDs as *input*: Spliit does not know
which groups are yours, the client does (the real UI keeps them in localStorage).

So this server has to be the system of record for your group IDs, and those IDs
are effectively secrets. That is what the config page manages, and what OIDC
protects. MCP tools resolve a group **only against the calling user's own rows**,
so a group ID leaked from anywhere else cannot be used through this server.

## Two authorization surfaces

Both use the same OIDC provider, for different things. Neither has anything to do
with Spliit, which has no auth to integrate with.

| Surface | Mechanism |
|---|---|
| Config web UI | OIDC relying party: authorization code + PKCE → server-side session cookie |
| `/mcp` endpoint | OAuth 2.0 protected resource: bearer access tokens, verified against the issuer's JWKS (or introspection for opaque tokens) |

Bearer tokens are audience-checked against this server's resource identifier
(`<public_url>/mcp`, RFC 8707), so a token minted for another service at the same
issuer cannot be replayed here. `GET /.well-known/oauth-protected-resource`
publishes the metadata MCP clients use to discover the flow after a 401.

> **The most likely integration snag:** the MCP client auth flow expects the
> authorization server to support Dynamic Client Registration, and not every OIDC
> provider does. If yours does not, pre-register a client and set
> `oidc.mcp_client_id`. Verify this against your real issuer early.

## Configuration

Copy `config.example.yaml` and adjust, or use `SPLIIT_MCP_*` environment
variables (dots become underscores, e.g. `SPLIIT_MCP_OIDC_CLIENT_SECRET`).

Register `<public_url>/auth/callback` as a redirect URI with your OIDC provider.

Storage is SQLite by default; a `postgres://` DSN in `database_url` switches
engines. Migrations for both ship in the binary.

## Running

```sh
make build          # Go binary + web/dist
./spliit-mcp serve -c config.yaml

make dev            # backend on :8080, Vite on :5173 with a proxy
make test           # go test ./... + SPA typecheck
make test-postgres  # also runs the store tests against a throwaway Postgres
```

### Local test stack

`compose.dev.yml` runs a real Spliit instance alongside spliit-mcp and both
databases. No identity provider is bundled — bring your own and configure it in
`.env`, along with ports, passwords and the Spliit version:

```sh
cp .env.example .env
docker compose -f compose.dev.yml up --build
```

See [COMPOSE.md](COMPOSE.md) for the walkthrough and the one constraint on the
issuer URL.

### Container images

Published to GHCR for `linux/amd64` and `linux/arm64`:

```
ghcr.io/daedaluz/spliit-mcp             # API
ghcr.io/daedaluz/spliit-mcp-frontend    # config UI (nginx)
```

```sh
make docker         # build both, both architectures, without pushing
make docker-push    # build and push
```

Two deployment shapes, as in `nextstop`:

1. **Monolith** — the Go binary serves the API and the SPA when `web_dir` points
   at `web/dist`.
2. **Split** — `Dockerfile.backend` (distroless) serves the API; `Dockerfile.frontend`
   (nginx) builds and serves the SPA and proxies `/api`, `/auth`, `/mcp` and
   `/.well-known` to `$API_UPSTREAM`. Set `web_dir` empty in the backend.

## Using it

1. Sign in to the config page.
2. Under **Settings**, set **your name** — the one you go by in Spliit. It is
   matched against participants when you join a group.
3. **Join a group** by pasting its ID or full Spliit URL, or **create** one
   outright. Joining fetches the group read-only first; if exactly one
   participant matches your name it is preselected, otherwise you pick yourself.
4. The **Groups** tab shows what your client can reach, and flags any group
   whose participant needs re-picking.
5. Connect your MCP client:

```sh
claude mcp add --transport http spliit https://spliit-mcp.example.com/mcp
```

Self-hosting alongside spliit.app needs no setup: a pasted group link says which
instance hosts it, and that is stored with the group. Aliases are unique per
user across instances, so tools only ever need the alias.

## Tools

**Expenses and balances** — `list_groups`, `get_group`, `get_balances`,
`list_expenses`, `get_expense`, `create_expense`, `update_expense`,
`delete_expense`, `get_stats`, `list_activities`, `list_categories`.

**Group management**, mirroring the settings page — `inspect_group`,
`join_group`, `leave_group`, `create_group`, `set_active_participant`,
`get_server_info`.

Every tool takes `group` (an alias from `list_groups`). `create_expense` defaults
to you having paid, split evenly between all participants. `get_balances` is
framed relative to you ("You owe 120.00 SEK overall"). Amounts are decimal
strings in the group's currency — money never passes through a float.

Joining always requires naming which participant is you, whether through the UI
or `join_group`; `inspect_group` reads a group without joining so those names
can be discovered first. A group joined without that identity could not be
written to at all, so it is asked for up front rather than deferred.

## Credits

Spliit API access uses [`go.chrastecky.dev/spliit-api`](https://github.com/RikudouSage/SpliitApi),
an unofficial Go client covering Spliit's tRPC procedures.
