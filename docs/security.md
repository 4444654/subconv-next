# Security Model

SubConv Next is a self-hosted subscription converter. It does not provide a public authentication gateway, proxy core, traffic forwarding, port forwarding, or network scanning.

## Deployment Boundary

- Default development configuration binds the service to `127.0.0.1`.
- The provided `docker-compose.yml` publishes port `9876` on `127.0.0.1` by default, mounts `/config` read-only, drops Linux capabilities, and uses a read-only root filesystem. A network-disabled one-shot helper prepares the bind-mounted data directory for the unprivileged UID before startup.
- A configured `SUBCONV_ACCESS_TOKEN` protects the management interface on both private and public peers. The Web UI exchanges it at `/login` for a 12-hour signed HttpOnly, SameSite session cookie; unsafe browser requests require a session-bound CSRF token. API clients may send a Bearer token, proactive HTTP Basic credentials, or `X-SubConv-Access-Token`. Tokens in URL query parameters are rejected.
- `SUBCONV_PUBLIC_CONVERTER=true` exposes the converter UI and an explicit converter-route allowlist without login. Stateful requests require a cryptographically random workspace capability, publication management requires the owning workspace capability, and anonymous requests receive separate workspace, expensive-operation, and general rate limits.
- Public workspaces cannot enable private-network subscription fetching or disable TLS certificate verification, even if a browser submits `allow_lan` or `insecure_skip_verify`; idle anonymous workspaces are capped at six hours and stale published links are cleaned after 30 days without access.
- If the system resolver returns only a Fake-IP or another reserved address, public mode retries through public DNS resolvers and accepts only validated public addresses. The selected address remains pinned for the request, including after redirects.
- Public workspace service controls are server-owned: clients cannot raise fetch size or timeout limits, shorten refresh intervals, enable proxy-header trust, or inject management credentials. Each public workspace is limited to 16 subscription sources, 32 manual sources, 512 KiB of manual content, bounded inline custom-rule collections, 5,000 final nodes, and a 4 MiB rendered YAML response. Remote custom-rule snapshots are disabled in public mode. The service retains at most 256 published items.
- Stateless parsing accepts at most 512 KiB and stateless rendering accepts at most 2,000 nodes. Public stateless rendering uses clean built-in defaults and cannot inherit server-side rule providers, headers, or custom rules. Refresh and state locking is scoped per workspace with a process refresh limit that defaults to four jobs and is tunable via `SUBCONV_MAX_CONCURRENT_REFRESHES`. Workspace and published-store caps default to 256 each (`SUBCONV_MAX_WORKSPACES`, `SUBCONV_MAX_PUBLICATIONS`). Logo discovery is limited to eight candidates and a ten-second request budget. Subscription and logo fetches are limited to common HTTP/HTTPS Web ports and re-check DNS/IP and port policy after every redirect.
- Public mode redirects `/login` to the converter, disables password submissions, returns a minimal `/healthz` payload, and rejects cross-site API reads as well as writes.
- A published `/s/{token}/...` URL can only download rendered YAML. It cannot restore editable source configuration in public mode. Browser-local drafts use a separate random `publish_id` capability when rebinding an existing publication.
- `SUBCONV_TRUST_PROXY_HEADERS=true` is safe only when the backend is reachable exclusively through a trusted proxy that rewrites forwarding headers. Otherwise clients can spoof their rate-limit identity.
- `SUBCONV_ALLOW_INSECURE_PUBLIC=true` is an explicit passwordless preview override. It exposes every management operation to reachable clients and is not a production security boundary.
- A non-loopback listener requires a management token of at least 24 characters at startup. Private-network and Docker bridge source addresses do not bypass authentication. A tokenless non-loopback preview requires the explicit `SUBCONV_ALLOW_INSECURE_PUBLIC=true` override and must remain local-only.
- Put public deployments behind a TLS reverse proxy or VPN and do not expose the backend port directly. A reverse proxy must preserve the incoming `Authorization` header.
- `/healthz` is intentionally unauthenticated and returns only basic service health.

Management responses include CSP, clickjacking, MIME-sniffing, referrer, browser-permission, and cross-origin isolation headers. API responses are `no-store`. Public API and `/s/{token}/...` traffic receive in-process per-client limits. Published token lookup is indexed in memory after a lazy startup scan, while access counters are batched to avoid a metadata write and log entry for every download. Because this state is not shared across replicas, production reverse proxies or edge firewalls must add distributed connection and rate limits.

The UI's Content Security Policy limits script network connections to the same origin. HTTPS images may still be displayed, but subscription and logo content fetched by the server remains subject to private-address, DNS rebinding, redirect, size, timeout, and Web-port checks.

## Published Subscription Links

- Fixed `/sub/mihomo.yaml` is disabled and returns `404`.
- Published subscriptions use `/s/{random-token}/mihomo.yaml`.
- `publish_id` and subscription tokens are generated with `crypto/rand`.
- Each `publish_id` stores one `current.yaml`; normal regeneration overwrites that file.
- Rotating the private link changes only the token. The old token immediately returns `404`.
- Deleting a publish removes its published directory and the old link immediately returns `404`.
- Published YAML responses use `Cache-Control: no-store`, `X-Robots-Tag: noindex, nofollow, noarchive`, and `X-Content-Type-Options: nosniff`.

## Local Draft Privacy

Local draft data is only written after the user explicitly chooses `保存为本机草稿` or `更新本机草稿`.

Allowed in `localStorage.SUBCONV_LOCAL_DRAFT`:

- Editing configuration.
- Subscription source name, emoji, URL, and User-Agent.
- `publish_ref.publish_id`.
- `publish_ref.token_hint`.
- `publish_ref.updated_at`.
- Node edit state needed to restore the editing session.

Forbidden in local draft storage:

- Full `/s/{token}/mihomo.yaml` URL.
- Full subscription token.
- Rendered `current.yaml`.
- Runtime logs.
- `access_count` and `last_access_at`.

Restoring a draft creates a new workspace. If the saved `publish_id` still exists, the workspace is rebound to that publish and future `重新生成配置` overwrites the same `current.yaml` without changing the link.

## API Redaction

The following API surfaces must not expose secrets by default:

- `/api/config` redacts access tokens and subscription URL query values.
- Public `/api/config`, `/api/status`, and refresh responses omit server filesystem paths and listener details. Nested rule/template/DNS URLs and sensitive rule-provider headers are redacted in configuration responses.
- `/api/nodes` and node detail responses mask password, uuid, private key, and pre-shared key fields.
- `/api/logs` returns masked log lines.
- `/api/published/{publish_id}` requires the owning workspace capability and returns the current subscription URL without a separate raw token field.
- Updating a redacted Web UI configuration preserves the existing access token instead of replacing it with an empty or masked value.

## Logging

Logs are masked before writing to `/data/logs/app.log`.

Masked values include:

- `/s/{token}/mihomo.yaml`
- URL query parameters such as `token`, `key`, `auth`, `password`, and `uuid`
- URI userinfo secrets such as `ss://password@...`
- UUIDs
- `password`, `uuid`, `private-key`, `pre-shared-key`, `authorization`, and `cookie` key/value pairs

Log rotation keeps at most three rotated files, each up to 5 MB.

New configuration, workspace metadata, node state, subscription cache, logs, generated YAML, and publication files use owner-only `0600` permissions. Newly created workspace, cache, log, and publication directories use `0700`. Existing files adopt the private mode when the service next rewrites them.

## YAML Integrity

The renderer receives final nodes only. Disabled, deleted, excluded, invalid, info, and deduplicated nodes must not enter the final YAML.

After rendering, the YAML is parsed back and validated:

- Every `proxies[].name` exists in the final node set.
- `proxy-groups[].proxies` references only existing node names, existing group names, `DIRECT`, or `REJECT`.
- `⚡ 自动选择` references only real nodes.
- Country or region proxy-groups are not generated in V1.
- Excluded nodes are not referenced by proxies or groups.
- Rule providers referenced by rules exist.
- Rule targets exist.
- `MATCH` is the last rule.

## Reporting Security Issues

For a public GitHub release, create a private disclosure path before enabling Issues for security reports. Until then, do not post real subscription URLs, tokens, node credentials, or logs containing unredacted secrets in public issues.
