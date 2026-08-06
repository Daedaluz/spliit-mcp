# Kubernetes deployment

```
base/                 the deployment, independent of any cluster
overlays/inits/       spliit-mcp.inits.se, in the mcp namespace
```

```sh
kubectl kustomize k8s/kustomize/overlays/inits          # render
kubectl apply -k k8s/kustomize/overlays/inits           # deploy
```

## Secrets are not in this repo

The repository is public, so nothing sensitive is committed. Two values must
exist as a secret before the backend will start:

```sh
kubectl create secret generic spliit-mcp-oidc -n mcp \
  --from-literal=client-secret='<the OIDC client secret>' \
  --from-literal=state-secret="$(openssl rand -hex 32)"
```

`state-secret` protects the login state and PKCE cookie. It has to be **stable
and shared by every replica**: each pod would otherwise generate its own keys,
and a login would fail whenever the OAuth callback landed on a different pod
than the one that started it.

While the GHCR packages are private, a pull secret is needed too:

```sh
kubectl create secret docker-registry ghcr-credentials -n mcp \
  --docker-server=ghcr.io \
  --docker-username='<github user>' \
  --docker-password='<a token with read:packages>'
```

Delete `overlays/inits/pull-secret.yaml` from the patch list once the packages
are public.

## The database

CloudNativePG provisions `spliit-mcp-db` and publishes the connection details as
the secret `spliit-mcp-db-app`; the deployment reads its `uri` key straight into
`SPLIIT_MCP_DATABASE_URL`. Nothing to create by hand, and no password ever
passes through this repo.

## Routing

An HTTPRoute on the shared `www` gateway sends `/mcp`, `/api`, `/auth`,
`/.well-known` and `/healthz` **directly to the backend**, and everything else to
nginx for the SPA. Keeping the MCP endpoint off the nginx hop means one less
proxy in front of a streaming response.

## What the overlay must get right

`SPLIIT_MCP_PUBLIC_URL` has to be the hostname clients actually use: the OIDC
redirect URI and the MCP token audience are both derived from it. Register the
matching redirect URI with the provider:

```
https://spliit-mcp.inits.se/auth/callback
```

Because that URL is `https`, session cookies are marked `Secure` automatically.

## Adapting to another cluster

Copy `overlays/inits`, then change the hostname in `httproute-host.yaml`, the
issuer and client ID in the configMapGenerator, and — if the gateway is named
differently — the `parentRefs` in `base/httproute.yaml`. The base assumes Gateway
API and CloudNativePG; swap `base/database.yaml` for a plain
`SPLIIT_MCP_DATABASE_URL` if you already run PostgreSQL elsewhere, or point it at
a SQLite path on a PVC for a single-replica install.
