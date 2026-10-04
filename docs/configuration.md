# Configuration

This document covers the V1 runtime configuration surface for Docker and binary deployments.

## Priority

Configuration priority is:

```text
CLI flags > environment variables > config file defaults
```

Use CLI flags for one-off binary runs. Use environment variables for Docker and process managers.

## CLI

Start the HTTP service:

```sh
subconv-next serve \
  --config /config/config.json \
  --host 0.0.0.0 \
  --port 9876 \
  --data-dir /data \
  --public-base-url https://subconv.example.com \
  --log-level info
```

Supported service flags:

| Flag | Default | Description |
| --- | --- | --- |
| `--config` | `config/config.json` | JSON or UCI config file path. |
| `--host` | config value | Listen address override. |
| `--port` | config value | Listen port override. |
| `--data-dir` | config paths | Runtime data root. Must be an absolute path. |
| `--public-base-url` | config value | Public origin used when generating subscription links. |
| `--log-level` | config value | Service and rendered YAML log level. |

Other CLI commands:

```sh
subconv-next version
subconv-next parse
subconv-next generate
```

## Environment Variables

| Variable | Default in Docker | Description |
| --- | --- | --- |
| `SUBCONV_HOST` | `0.0.0.0` | Listen address inside the container or process. |
| `SUBCONV_PORT` | `9876` | Listen port. |
| `SUBCONV_DATA_DIR` | `/data` | Runtime data directory. |
| `SUBCONV_PUBLIC_BASE_URL` | empty | Public base URL for subscription links and expected browser API origin, including reverse proxy HTTPS login. |
| `SUBCONV_LOG_LEVEL` | `info` | Service and renderer log level. |
| `SUBCONV_ACCESS_TOKEN` | empty | API token and initial web password until an independent password is configured. If set on a non-loopback listener, it must contain at least 24 characters. |
| `SUBCONV_MANAGEMENT_USERNAME` | `admin` | Web login username; 1–64 letters, digits, `_`, `.`, `@` or `-`. |
| `SUBCONV_MANAGEMENT_PASSWORD_HASH` | empty | Independent bcrypt web password hash, cost 10–14. When set, the API token cannot be used as the web password. |
| `SUBCONV_PUBLIC_CONVERTER` | `false` | Allow anonymous access to the workspace-isolated converter UI and approved converter APIs. |
| `SUBCONV_TRUST_PROXY_HEADERS` | `false` | Trust proxy-provided client IP headers for in-process rate limits; only use behind a private trusted proxy. |
| `SUBCONV_ALLOW_INSECURE_PUBLIC` | `false` | Explicitly disable management authentication. Preview use only. |

Example:

```sh
SUBCONV_HOST=0.0.0.0 \
SUBCONV_PORT=9876 \
SUBCONV_DATA_DIR=/data \
SUBCONV_PUBLIC_BASE_URL=https://subconv.example.com \
SUBCONV_LOG_LEVEL=info \
SUBCONV_ACCESS_TOKEN=replace-with-a-long-random-token \
SUBCONV_PUBLIC_CONVERTER=true \
subconv-next serve --config /config/config.json
```

## Web Login

The account-enabled binary requires both a username and password at `/login`. Old installations initially use `admin` and their existing API token as the password. For native installs, `scn account` sets an independent username and password with hidden input. Re-run the current installer first when upgrading older native installations; upstream images and binaries may not include this fork's account login feature.

For other process managers, read the password from standard input with `subconv-next hash-password`; the command expects 8–72 bytes with no trailing newline. Store its output in `SUBCONV_MANAGEMENT_PASSWORD_HASH` (quote the value when assigning it in a shell because bcrypt hashes contain `$`). The corresponding JSON/UCI service fields are `management_username` and `management_password_hash`. Never commit real credentials or hashes. A non-loopback listener requires a valid password hash or a strong API token; any configured API token must still meet the 24-character minimum.

Only hashes are stored for independent passwords. Changing the username, password hash, or API token invalidates existing management sessions. The browser session duration remains 12 hours. API token authentication and published subscription links remain independent of the web password. Account fields are excluded from workspace configuration and password hashes are omitted from API responses.

## Data Directory

`SUBCONV_DATA_DIR` defaults to `/data` in Docker. Docker deployments should mount:

```yaml
volumes:
  - ./data:/data
```

Runtime files include:

```text
/data/state.json
/data/cache/
/data/workspaces/
/data/published/
/data/logs/
```

Published subscription links depend on:

```text
/data/published/{publish_id}/current.yaml
/data/published/{publish_id}/meta.json
```

If `/data` is not mounted, links and workspace state can be lost when the container is removed.

## Public Base URL

`SUBCONV_PUBLIC_BASE_URL` controls the base URL returned by publish APIs and the expected browser origin used by API same-origin checks. Behind a TLS reverse proxy, set it to the address used in the browser, including a non-default port if applicable. Native installs can use `scn url https://subconv.example.com`.

Use it when SubConv Next is behind a reverse proxy:

```sh
SUBCONV_PUBLIC_BASE_URL=https://subconv.example.com
```

This value also makes HTTPS management login cookies Secure when TLS terminates at the proxy. A missing or mismatched address can cause login to return `CROSS_ORIGIN_REQUEST`. It does not configure TLS certificates, reverse proxy routing, credentials, or firewall rules.

## Docker Example

```yaml
services:
  subconv-next:
    image: ghcr.io/OWNER/subconv-next:latest
    container_name: subconv-next
    restart: unless-stopped
    ports:
      - "127.0.0.1:9876:9876"
    volumes:
      - ./config:/config:ro
      - ./data:/data
    environment:
      SUBCONV_HOST: 0.0.0.0
      SUBCONV_PORT: 9876
      SUBCONV_DATA_DIR: /data
      SUBCONV_PUBLIC_BASE_URL: ""
      SUBCONV_LOG_LEVEL: info
      SUBCONV_ACCESS_TOKEN: ${SUBCONV_ACCESS_TOKEN:-}
```

See [docker.md](docker.md) for Docker-specific deployment, health check, backup, and multi-arch build steps.

## DNS Defaults

The default rendered DNS block is intentionally scoped for OpenClash compatibility:

```yaml
dns:
  enable: true
  listen: 127.0.0.1:5335
  enhanced-mode: fake-ip
  default-nameserver: [119.29.29.29, 223.5.5.5]
  nameserver-policy:
    '*.linux.do': https://xxx.ddd.oaifree.com/query-dns
    geosite:cn,private,apple:
      - https://doh.pub/dns-query
      - https://dns.alidns.com/dns-query
    linux.do: https://xxx.ddd.oaifree.com/query-dns
  nameserver: ['https://1.1.1.1/dns-query#RULES', 'https://8.8.8.8/dns-query#RULES']
  proxy-server-nameserver: [119.29.29.29, 223.5.5.5]
  direct-nameserver: ['https://doh.pub/dns-query', 'https://dns.alidns.com/dns-query']
  direct-nameserver-follow-policy: true
  fake-ip-range: 198.18.0.0/16
  fake-ip-filter: ['*.lan', '*.local', '*.arpa', time.*.com, ntp.*.com, +.market.xiaomi.com, localhost.ptlogin2.qq.com, '*.msftncsi.com', www.msftconnecttest.com]
```

SubConv Next does not emit `fallback`, `fallback-filter`, or DoT servers by default. `default-nameserver` and `proxy-server-nameserver` use domestic plain DNS for bootstrap and node domains, while the global `nameserver` entries include `#RULES`.
