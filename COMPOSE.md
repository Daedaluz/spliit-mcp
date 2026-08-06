# Local test stack

`compose.dev.yml` brings up a real Spliit instance, its database, spliit-mcp's
database, and spliit-mcp itself behind nginx.

```sh
cp .env.example .env     # point it at your OIDC provider
docker compose -f compose.dev.yml up --build
```

| | |
|---|---|
| http://localhost:8080 | spliit-mcp config UI |
| http://localhost:3000 | Spliit |

Everything — ports, database passwords, the Spliit version, and the whole OIDC
configuration — is set in `.env`. Any `SPLIIT_MCP_*` variable the server
understands works there, not only the ones listed in `.env.example`.

## Bring your own identity provider

No identity provider is bundled, so the stack works with whatever you already
run. Configure yours in `.env` and register this redirect URI:

```
http://localhost:8080/auth/callback
```

For the MCP endpoint specifically, the provider should issue **JWT access
tokens** with an audience of `http://localhost:8080/mcp`. If it cannot bind
audiences, set `SPLIIT_MCP_OIDC_SKIP_AUDIENCE_CHECK=true` — but that removes the
protection against replaying a token minted for another service, so it is worth
fixing at the provider instead.

If the provider issues opaque access tokens, spliit-mcp falls back to
introspection, which needs `SPLIIT_MCP_OIDC_CLIENT_SECRET` set.

### If the provider does not allow anonymous client registration

MCP clients try to register themselves at the provider's
`registration_endpoint` (RFC 7591). Many providers require an initial access
token for that, and reject the attempt:

```
SDK auth failed: missing or invalid registration credential
```

That error comes from the **provider**, not from spliit-mcp. The fix is to
pre-register a client and tell the MCP client to use it instead of registering.

1. Create an application at your provider for the MCP client:
   - a **public / PKCE** client (`token_endpoint_auth_method: none`) is enough
   - redirect URI: `http://localhost:45454/callback` — Claude Code uses
     `http://localhost:<callback-port>/callback`, and the port must be pinned
     because the URI has to be registered ahead of time
   - put it in the **same project/tenant** as the web UI client, or token
     introspection will report the token as inactive

2. Point Claude Code at it:

   ```sh
   claude mcp add --transport http spliit http://localhost:8080/mcp \
     --client-id <the new client id> \
     --callback-port 45454
   ```

   Add `--client-secret` too if you made it a confidential client.

3. Set `SPLIIT_MCP_OIDC_MCP_CLIENT_ID` to the same client ID. spliit-mcp accepts
   it as a valid token audience, which is what lets the audience check stay on
   for providers that put the client ID in `aud` rather than a resource URL.

### The one constraint: the issuer URL

`SPLIIT_MCP_OIDC_ISSUER` has to resolve to the **same URL** from two places: your
browser, which gets redirected there to log in, and the backend container, which
fetches discovery and JWKS from it and compares the issuer string.

- **A provider on the network or internet** — nothing to do; one URL works
  everywhere.
- **A provider on your own machine** — `localhost` means the container itself
  inside Docker, so it cannot be used as-is. Use `host.docker.internal`, which
  the backend already has mapped to the host gateway, and make your browser
  resolve it too by adding one line to `/etc/hosts`:

  ```
  127.0.0.1 host.docker.internal
  ```

  Then set `SPLIIT_MCP_OIDC_ISSUER=http://host.docker.internal:<port>/...` and
  register the same hostname in your provider's redirect URI. Both sides now
  agree on one string.

## Walking through it

1. Open http://localhost:8080 and sign in.
2. Set **your name** to the one you use in Spliit.
3. In another tab, create a group at http://localhost:3000 and copy its URL.
4. Back in the config UI, paste that URL under **Add a group**. If a participant
   matches your name it is pinned automatically; otherwise pick yourself.
5. Point an MCP client at `http://localhost:8080/mcp`.

The stack starts with the bundled Spliit already registered as each new user's
first server, so step 3 needs no extra setup.

## Resetting

```sh
docker compose -f compose.dev.yml down -v    # -v also drops both databases
```

## Not Linux?

`host.docker.internal` resolves out of the box on Docker Desktop, so the
`/etc/hosts` line above is only needed on Linux.
