# Docker Deployment

SubConv Next is Docker-first for V1. The image contains the Go binary and embedded Web UI assets; no Node.js runtime is required.

## Quick Start

Use Docker Engine with the Compose v2 plugin and run these commands from the repository root, using the supplied [docker-compose.yml](../docker-compose.yml):

```sh
mkdir -p config data
export SUBCONV_ACCESS_TOKEN="$(openssl rand -hex 32)"
docker compose up -d
curl -fsS http://127.0.0.1:9876/healthz
```

Open:

```text
http://127.0.0.1:9876/
```

Sign in with `SUBCONV_ACCESS_TOKEN`. Keep this token in a password manager or another private location and export the same value before future updates. Do not commit tokens or runtime data to the repository.

The image runs `/usr/bin/subconv-next` directly. It does not include a Go toolchain, Node.js runtime, or a baked-in runtime configuration. If `/config/config.json` is absent, the process starts from built-in defaults and applies environment overrides.

The runtime process uses the unprivileged UID/GID `10001:10001`. Compose runs a short-lived, network-disabled `subconv-data-init` service before the application starts; it grants UID/GID `10001:10001` ownership of the existing `./data` tree and then exits. If an existing configuration file is not readable by that account, adjust it once before upgrading:

```sh
sudo chown root:10001 ./config/config.json
sudo chmod 0640 ./config/config.json
```

The supplied Compose file publishes the host port only on loopback by default. Because the process listens on `0.0.0.0` inside the container, it still requires a strong access token:

```yaml
ports:
  - "127.0.0.1:9876:9876"
```

For trusted LAN access, explicitly set `SUBCONV_HOST_BIND` and an access token:

```sh
export SUBCONV_HOST_BIND=0.0.0.0
export SUBCONV_ACCESS_TOKEN="$(openssl rand -hex 32)"
docker compose up -d
```

For a public passwordless converter, keep `9876` bound to `127.0.0.1`, put SubConv Next behind a TLS reverse proxy, and set `SUBCONV_PUBLIC_CONVERTER=true`, `SUBCONV_PUBLIC_BASE_URL`, and a strong `SUBCONV_ACCESS_TOKEN` for protected routes. Anonymous visitors use independent random workspaces. Do not publish port `9876` directly, and add distributed rate limits at the public edge because the built-in limiter is per process.

The backend deliberately leaves `/healthz` and `/s/{token}/...` outside the management login boundary. Published subscription URLs are bearer credentials and must be kept private.

For a temporary tokenless local preview, set `SUBCONV_ALLOW_INSECURE_PUBLIC=true` explicitly. This disables the management boundary and must never be used when the host port is reachable from another machine.

## Standalone Docker

Compose is optional. With Docker Engine alone, prepare dedicated bind-mount directories and start the same image directly. The following ownership commands apply only to this application's `./data` directory; use a new deployment directory, not a shared data directory. When running as root, omit `sudo`.

```sh
mkdir -p config data
sudo chown -R 10001:10001 ./data
sudo chmod 0700 ./data
export SUBCONV_ACCESS_TOKEN="$(openssl rand -hex 32)"
docker run -d \
  --name subconv-next \
  --restart unless-stopped \
  --init \
  --user 10001:10001 \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=16m \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  -p 127.0.0.1:9876:9876 \
  -v "$PWD/config:/config:ro" \
  -v "$PWD/data:/data" \
  -e SUBCONV_ACCESS_TOKEN \
  "${SUBCONV_IMAGE:-ghcr.io/earl9/subconv-next:latest}"
curl -fsS http://127.0.0.1:9876/healthz
```

Save the token and use it to sign in. For trusted LAN access, replace the port mapping with `-p 0.0.0.0:9876:9876` and access `http://<server-ip>:9876/`. Keep authentication enabled. `SUBCONV_HOST_BIND` is a Compose setting and does not affect a standalone `docker run` command.

Do not run the Compose and standalone examples at the same time: they use the same container name and port. An optional `./config/config.json` must be readable by UID/GID `10001:10001`; without it, built-in defaults and environment variables apply.

## Persistence

The runtime data directory is `/data`:

```yaml
volumes:
  - ./config:/config:ro
  - ./data:/data
```

Published subscriptions are stored under:

```text
/data/published/{publish_id}/current.yaml
/data/published/{publish_id}/meta.json
```

Keep `./data` mounted. Without this volume, published links and workspace state are lost on container removal. The initialization service changes ownership only inside this dedicated data directory; it has no network and runs with only the filesystem capabilities required for that migration.

To verify persistence, generate a subscription link, restart the container, then request the same link again:

```sh
docker compose restart subconv-next
curl -I "http://127.0.0.1:9876/s/{token}/mihomo.yaml"
```

The link should still return `200` as long as `./data` is mounted.

For backup:

```sh
tar -czf subconv-next-data-backup.tgz ./data
```

For restore, stop the container, restore `./data`, then start it again:

```sh
docker compose down
tar -xzf subconv-next-data-backup.tgz
docker compose up -d
```

## Environment Variables

Docker supports these environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `SUBCONV_HOST` | `0.0.0.0` | Listen address inside the container. |
| `SUBCONV_PORT` | `9876` | Listen port and healthcheck port. |
| `SUBCONV_DATA_DIR` | `/data` | Runtime state, cache, logs, and published subscriptions. |
| `SUBCONV_PUBLIC_BASE_URL` | empty | Public origin used in generated subscription links. |
| `SUBCONV_LOG_LEVEL` | `info` | Service and render log level. |
| `SUBCONV_ACCESS_TOKEN` | empty | Management UI/API token. Required and at least 24 characters when `SUBCONV_HOST` is non-loopback, including the default container listener. |
| `SUBCONV_PUBLIC_CONVERTER` | `false` | Expose the workspace-isolated converter UI without a login. |
| `SUBCONV_TRUST_PROXY_HEADERS` | `false` | Use the first `X-Forwarded-For`/`X-Real-IP` address for built-in limits. Enable only when the backend is reachable exclusively through a trusted proxy. |
| `SUBCONV_ALLOW_INSECURE_PUBLIC` | `false` | Disable management login on a non-loopback listener. Preview use only. |
| `SUBCONV_MAX_WORKSPACES` | `256` | Cap on concurrently stored workspaces. Lower it on small hosts exposed to the Internet. |
| `SUBCONV_MAX_PUBLICATIONS` | `256` | Cap on stored published subscriptions. |
| `SUBCONV_MAX_CONCURRENT_REFRESHES` | `4` | Process-wide concurrent refresh limit. |

Example:

```sh
SUBCONV_ACCESS_TOKEN="$(openssl rand -hex 32)" \
SUBCONV_PUBLIC_CONVERTER=true \
SUBCONV_PUBLIC_BASE_URL=https://subconv.example.com \
docker compose up -d
```

`SUBCONV_PUBLIC_BASE_URL` only changes generated subscription links returned by the API. It does not configure TLS or reverse proxy behavior.

`SUBCONV_PUBLIC_CONVERTER=true` is the supported passwordless mode. It exposes only the converter allowlist and requires random workspace capabilities for stateful operations. `SUBCONV_ALLOW_INSECURE_PUBLIC=true` exposes every management operation to every reachable client and must not be used for an Internet-facing production deployment.

Public mode caps idle anonymous workspaces at six hours and treats published links as stale after 30 days without access. Set `SUBCONV_TRUST_PROXY_HEADERS=true` only when port `9876` is firewalled from the Internet and the reverse proxy strips and rewrites forwarding headers.

Public-mode resource policy is intentionally fixed by the server. A workspace may contain at most 16 subscription sources and 32 manual sources, manual content is capped at 512 KiB, final output is capped at 5,000 nodes and 4 MiB, and the service retains at most 256 published items. Stateless public rendering starts from clean built-in defaults and never inherits server rule providers or headers. Remote custom-rule snapshots are disabled; use inline rules or runtime rule providers instead. Refreshes and state changes are isolated per workspace with a four-job process refresh limit. Published subscription downloads are rate-limited per client; token lookup uses an in-memory hash index after a lazy startup scan, and access metadata is written in batches. Logo discovery checks at most eight candidates within a ten-second request budget. Outbound subscription and logo requests are limited to common HTTP/HTTPS Web ports; private, loopback, link-local, multicast, and cloud metadata targets remain blocked after DNS resolution and redirects.

When the host resolver returns only a Clash/Mihomo Fake-IP or another reserved address, public converter mode retries DNS resolution through public resolvers and still pins the validated public address for the outbound request. This avoids false SSRF rejections without allowing requests to private or reserved networks.

Published subscription URLs are download-only bearer credentials in public mode. They cannot be used to recover source URLs, manual node content, or the editable workspace. Keep browser-local drafts if editable recovery is required.

For Nginx, preserve cookies and the incoming `Authorization` header so browser sessions and Bearer API credentials both reach the backend:

```nginx
location / {
    proxy_pass http://127.0.0.1:9876;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_set_header Authorization $http_authorization;
}
```

## Runtime Flags

The same values can be passed as explicit flags. Flags take precedence over environment variables:

```sh
subconv-next serve \
  --config /config/config.json \
  --host 0.0.0.0 \
  --port 9876 \
  --data-dir /data \
  --public-base-url https://subconv.example.com \
  --log-level info
```

## Health Check

```sh
curl -fsS http://127.0.0.1:9876/healthz
```

Expected shape:

```json
{"ok":true,"version":"...","data_dir":"/data","uptime_seconds":1}
```

In public converter mode, the response is intentionally smaller:

```json
{"ok":true,"uptime_seconds":1}
```

The health response does not include subscription URLs, published tokens, upstream URLs, or node secrets.

## Updating

```sh
docker compose pull
docker compose up -d
curl -fsS http://127.0.0.1:9876/healthz
```

To deploy the checked-out source instead of a registry release, build a local image and select it explicitly. Keep the same exported access token and data directories:

```sh
docker build -t subconv-next:local .
export SUBCONV_IMAGE=subconv-next:local
docker compose up -d
```

For standalone Docker, pull the selected registry image (or rebuild the local image), then stop and remove only the old application container:

```sh
docker pull "${SUBCONV_IMAGE:-ghcr.io/earl9/subconv-next:latest}"
docker stop subconv-next
docker rm subconv-next
```

Skip `docker pull` for a local build. Repeat the `docker run` command from [Standalone Docker](#standalone-docker) with the same image selection, saved access token, and bind mounts. Do not regenerate the token or remove `./config` and `./data` during an update. Container removal leaves these host directories intact.

## Log Redaction Check

Check logs with:

```sh
docker logs subconv-next
```

Logs must not contain complete upstream subscription tokens, full `/s/{token}/mihomo.yaml` links, node passwords, UUIDs, private keys, pre-shared keys, `Authorization`, or `Cookie` values. Published subscriptions should appear with `token_hint` or redacted paths such as `/s/<redacted>/mihomo.yaml`.

## Multi-Arch Build

Prepare buildx once:

```sh
docker buildx create --use --name subconv-next-builder
```

Build amd64 and arm64:

```sh
docker buildx build --platform linux/amd64,linux/arm64 -t subconv-next:local .
```

For registry release:

```sh
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/earl9/subconv-next:v1.0.0 \
  -t ghcr.io/earl9/subconv-next:latest \
  --push \
  .
```

Verify the pushed manifest:

```sh
docker buildx imagetools inspect ghcr.io/earl9/subconv-next:v1.0.0
docker buildx imagetools inspect ghcr.io/earl9/subconv-next:latest
```

The Dockerfile uses `TARGETOS` and `TARGETARCH`, so arm64 builds do not require code changes.

## Subscription Header Verification

After generating a published link, verify final headers with:

```sh
curl -D - -o /tmp/mihomo.yaml "http://127.0.0.1:9876/s/{token}/mihomo.yaml"
curl -I "http://127.0.0.1:9876/s/{token}/mihomo.yaml"
```

When upstream subscriptions provided traffic metadata, both commands should include:

```text
Subscription-Userinfo: upload=...; download=...; total=...; expire=...
Profile-Update-Interval: 24
Content-Disposition: attachment; filename="mihomo.yaml"
```
