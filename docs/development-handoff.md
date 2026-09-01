# Development Handoff

## Project

SubConv Next is a Go service with an embedded Web UI for converting upstream
subscriptions into validated Mihomo / Clash Meta YAML. The repository also
contains the OpenWrt and LuCI integration.

## Current source state

- Branch: `main`
- Source baseline before migration: `554c23b feat: harden public converter and refresh UI`
- Remote: `origin`
- Working tree at handoff: clean before this migration update
- Local Docker port: `9876`
- Local Docker image currently configured by Compose: `ghcr.io/earl9/subconv-next:latest`

The source tree includes security middleware, authenticated management routes,
anonymous workspace isolation, SSRF-safe subscription fetching, log redaction,
refresh deduplication, responsive Web UI, YAML preview, diagnostics, and
configuration import/export support.

## Runtime state

Runtime state is not part of Git. It is stored in `.local-dev/data/` on the
current machine and mounted into the container as `/data`. It includes:

- subscription fetch caches;
- generated YAML and published bearer-link metadata;
- anonymous workspace configurations and node state;
- service logs.

Treat it as secret. In particular, published links and upstream subscription
URLs are bearer credentials. Do not copy them into issues, commits, screenshots,
or chat messages.

The local environment file is `.local-dev/.env`. It contains the management
access token and public-mode flags. It must remain outside Git.

The copied environment currently sets `SUBCONV_HOST_BIND=0.0.0.0`, so Docker
publishes port `9876` on every host interface. For local-only development,
change that value to `127.0.0.1` before starting the service. Keep
`SUBCONV_ALLOW_INSECURE_PUBLIC=false`; the public converter flag does not remove
the management protection.

## Start here

Read [Development Environment](development-environment.md), then run:

```sh
cd /path/to/subconv-next
git status -sb
docker compose config --quiet
docker compose up -d
curl -fsS http://127.0.0.1:9876/healthz
```

Open `http://127.0.0.1:9876/`. For an environment that keeps runtime files in
`.local-dev/data`, export `SUBCONV_DATA_HOST_DIR="$PWD/.local-dev/data"` before
starting Compose.

## Development workflow

1. Read the relevant design and security docs before changing API or UI behavior.
2. Keep Go files formatted with `gofmt`.
3. Run focused tests while iterating, then run `go test ./...`, `go test -race ./...`, and `go vet ./...` before push.
4. Run `node --check internal/api/static/app.js` for Web UI changes.
5. Use `docker compose config --quiet` and a health check after container changes.
6. Use `agent-browser` for real desktop and mobile UI checks when changing the Web UI.
7. Never reset or checkout away user changes; inspect the diff and preserve unrelated work.

## Important code locations

- `internal/api/handlers.go`: API handlers and configuration writes.
- `internal/api/security.go`: authentication, CSRF, CORS/origin checks, and rate limits.
- `internal/api/workspaces.go`: anonymous workspace lifecycle and isolation.
- `internal/api/published.go`: bearer-link publication and access metadata.
- `internal/fetcher/`: subscription fetching, DNS checks, and SSRF protection.
- `internal/pipeline/`: parsing, filtering, validation, and output writing.
- `internal/api/static/`: embedded Web UI source.
- `docker-compose.yml`: local container, mounts, limits, and health check.
- `docs/security.md` and `SECURITY.md`: security contract.

## Do not regress

- Do not commit `.env`, `data/`, `.local-dev/`, `dist/`, IPK files, or checksums.
- Release screenshots and checksums are kept under `.local-dev/release-artifacts/`
  and are optional for runtime migration.
- Do not log full subscription URLs, publication tokens, passwords, UUIDs,
  private keys, `Authorization`, or `Cookie` values.
- Do not store full publication URLs or upstream credentials in browser-local
  drafts.
- Do not expose port `9876` directly for Internet deployments.
- Do not enable `SUBCONV_ALLOW_INSECURE_PUBLIC=true` on a reachable host.
- Keep `/data` mounted during upgrades and container recreation.

## Handoff validation

The receiving developer should report:

- the commit and branch being used;
- the path containing the private runtime directory;
- Docker health status and `/healthz` response;
- test commands and results;
- whether a test generation succeeded without exposing a real link.
