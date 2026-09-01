# Development Environment

This document describes how to move and run the current SubConv Next
development environment. It is safe to commit because it contains no live
credentials, subscription URLs, or runtime data.

## Requirements

- Git
- Go 1.22 or newer
- Docker Engine with Compose v2
- OpenSSL for generating a management token
- Optional: Node.js for static JavaScript syntax checks

## Repository

```sh
git clone <repository-url> subconv-next
cd subconv-next
git checkout main
```

The migration starts from source commit `554c23b`. Always verify the actual
remote tip before deploying:

```sh
git fetch origin
git status -sb
git log -1 --oneline --decorate
```

## Private local files

The following files are deliberately outside Git:

| Item | Purpose | Sensitivity |
| --- | --- | --- |
| `.local-dev/.env` | Docker variables and access token | secret |
| `.local-dev/data/` | Persistent state, caches, logs, workspaces and published links | secret |
| `.local-dev/dist/` | Local binaries and IPK artifacts | internal build output |
| `.local-dev/release-artifacts/` | Screenshots, review output, and release checksums | optional internal artifacts |
| `.local-dev/handoff/` | Previous local handoff notes | may contain operational details |
| `.local-dev/tmpinspect/` | Temporary inspection files | disposable |

Copy `.local-dev/` through a protected channel only. Keep permissions private:

```sh
chmod 700 .local-dev
chmod 600 .local-dev/.env
```

The root `.env` is a symlink to `.local-dev/.env` on the current machine so
existing Compose commands continue to work. On a new machine either create
the same symlink or set the host directory variables explicitly:

```sh
ln -s .local-dev/.env .env
```

## Docker setup

The Compose file uses these host-side defaults:

```text
config: ./config
data:   ./data
```

For a migrated private runtime, point Compose at the protected data folder:

```sh
export SUBCONV_CONFIG_HOST_DIR="$PWD/config"
export SUBCONV_DATA_HOST_DIR="$PWD/.local-dev/data"
docker compose up -d
```

If the copied environment keeps runtime data at `./data`, no override is
needed. The container still uses `/config` and `/data` internally.

The current local environment uses port `9876`. Do not expose it directly to
the Internet. A fresh checkout defaults to loopback:

```text
http://127.0.0.1:9876/
```

The private environment copied with this handoff currently overrides
`SUBCONV_HOST_BIND=0.0.0.0` for an earlier LAN/public-mode test. This makes
the port reachable on every host interface. Change it to
`SUBCONV_HOST_BIND=127.0.0.1` in `.local-dev/.env` before using the environment
on an untrusted network, then recreate the container.

Health check and container status:

```sh
docker compose ps
curl -fsS http://127.0.0.1:9876/healthz
```

## Build from source

```sh
go test ./...
go test -race ./...
go vet ./...
node --check internal/api/static/app.js
git diff --check
docker compose config --quiet
```

Build and run a local image:

```sh
docker build -t subconv-next:local .
SUBCONV_IMAGE=subconv-next:local docker compose up -d --force-recreate subconv-next
```

The image build runs the Go test suite again. Keep the data directory mounted
when recreating the container; otherwise published links and workspace state
are lost.

## Configuration notes

- `config/config.json` is the tracked baseline configuration.
- Do not place real subscription URLs or node credentials in tracked files.
- `SUBCONV_ACCESS_TOKEN` protects management routes and must be at least 24
  random characters for a non-loopback listener.
- `SUBCONV_PUBLIC_CONVERTER=true` enables anonymous, isolated converter
  workspaces while management routes remain protected by the token.
- Never use `SUBCONV_ALLOW_INSECURE_PUBLIC=true` on a reachable host.
- Keep `SUBCONV_PUBLIC_BASE_URL` empty for local-only use.

## Moving the runtime data

Stop the service before copying the data tree:

```sh
docker compose down
rsync -a --delete .local-dev/data/ new-host:/path/to/subconv-next/.local-dev/data/
rsync -a .local-dev/.env new-host:/path/to/subconv-next/.local-dev/.env
```

If release screenshots or checksums are needed for review, copy
`.local-dev/release-artifacts/` separately; they are not required to run the
service.

On the new host, verify ownership if Docker reports a permission error. The
Compose init container normally repairs ownership inside the dedicated data
directory. Do not run recursive ownership changes against the repository root.

After starting, verify that the existing published files remain present:

```sh
find .local-dev/data/published -maxdepth 2 -type f -print
docker compose ps
curl -fsS http://127.0.0.1:9876/healthz
```

## Logs and backups

Runtime logs are in `.local-dev/data/logs/app.log`. Treat them as sensitive
even though the application redacts known secrets. Do not commit logs.

Create an encrypted or otherwise protected backup before migration:

```sh
tar -czf subconv-next-data-backup.tgz -C .local-dev data
```

Delete temporary archives after transfer and rotate the management token if
the archive or `.env` was exposed.

## Troubleshooting

- `permission denied`: confirm `.local-dev/data` is writable by the Compose
  init container and run `docker compose up -d` again.
- `port is already allocated`: set `SUBCONV_PORT` and
  `SUBCONV_HOST_BIND`, then rerun Compose.
- `no allowed IPs`: check DNS/proxy behavior; public mode retries public DNS
  but still blocks private and reserved addresses.
- `no nodes available`: inspect the redacted logs and verify the upstream
  subscription URL is reachable from the host.
- stale browser UI: use a hard reload after replacing the image; keep the
  workspace query string when restoring a session.

## Handoff checklist

- [ ] Clone the repository and check out the intended commit.
- [ ] Copy `.local-dev/` through a private channel.
- [ ] Create the `.env` symlink or export host directory overrides.
- [ ] Run `docker compose config --quiet`.
- [ ] Run `go test ./...` and `go vet ./...`.
- [ ] Start Compose and confirm `healthy`.
- [ ] Open `http://127.0.0.1:9876/` and generate a test configuration.
- [ ] Confirm `.local-dev/data` remains mounted after a container restart.
- [ ] Rotate any token that was present on a shared transfer medium.
