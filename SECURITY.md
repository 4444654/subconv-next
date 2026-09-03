# Security Policy

## Supported Versions

| Version | Supported |
| --- | --- |
| `main` / latest | Yes |
| V1.x | Yes |
| Older snapshots | No |

## Security Boundary

SubConv Next supports local, trusted-LAN, and workspace-isolated public converter deployments. Public deployments must keep the backend port private, terminate HTTPS at a reverse proxy, and add distributed edge rate limits.

The Web UI exchanges the access token on `/login` for a signed, HttpOnly, SameSite session cookie. Unsafe browser requests also require a session-bound CSRF token. API clients may use `Authorization: Bearer <token>` or send HTTP Basic credentials proactively. `/healthz` and bearer-style `/s/{token}/...` subscription links remain outside the management login boundary. Tokens must never be passed in API query parameters.

Every non-loopback listener requires an access token of at least 24 characters. Loopback, private-network, and Docker bridge source addresses do not bypass this boundary. `SUBCONV_ALLOW_INSECURE_PUBLIC=true` is only an explicit local-preview escape hatch.

`SUBCONV_PUBLIC_CONVERTER=true` allows passwordless use of the converter allowlist. Anonymous state is isolated by cryptographically random workspace capabilities, and publication management requires the owning workspace capability. `SUBCONV_ALLOW_INSECURE_PUBLIC=true` disables the full management boundary and is not supported for Internet-facing production use.

Public mode also enforces server-owned fetch limits, configuration complexity quotas, final-node and rendered-output limits, a bounded publication store, common Web-port restrictions, DNS-rebinding-resistant dialing, cross-site API rejection, and automatic cleanup. Public workspaces additionally cannot disable TLS certificate verification for upstream fetches. Workspace, publication-store, and refresh-concurrency caps default to 256/256/4 and can be lowered via `SUBCONV_MAX_WORKSPACES`, `SUBCONV_MAX_PUBLICATIONS`, and `SUBCONV_MAX_CONCURRENT_REFRESHES`. The browser login endpoint is disabled in this mode; protected API routes continue to accept the management Bearer token.

A published `/s/{token}/...` link only authorizes downloading rendered YAML. Public mode does not allow that link to restore the editable workspace or reveal upstream URLs, credentials, or manual source content. Editable recovery is limited to browser-local drafts and the separate high-entropy `publish_id` capability stored by those drafts.

## Sensitive Data

SubConv Next attempts to redact sensitive values in APIs and logs, including upstream subscription URL tokens, published subscription tokens, passwords, UUIDs, WireGuard private keys, pre-shared keys, `Authorization`, and `Cookie` values.

New runtime configuration, node state, cache, logs, generated YAML, and publication metadata are written with owner-only permissions. Deployments should also restrict access to the mounted `/data` directory and Docker daemon.

The published container runs as the unprivileged `10001:10001` user with a read-only root filesystem, all Linux capabilities dropped, and `no-new-privileges`. The writable `/data` volume must be owned by that UID/GID.

Do not publish real subscription URLs, tokens, node secrets, or unredacted logs in public issues.

## Private Subscription Links

Published subscriptions use bearer-style private URLs:

```text
/s/{token}/mihomo.yaml
```

Anyone holding the full URL can fetch the generated YAML. If a link leaks, use `重新生成私密链接` in the Web UI; the old token is invalidated immediately. Deleting a published subscription also invalidates its old URL.

## Reporting a Vulnerability

Please open a GitHub Security Advisory or private issue if available.

Include the affected version or commit, deployment mode, a minimal reproduction, and redacted logs or configuration. Do not include real tokens, upstream subscription links, node passwords, UUIDs, private keys, cookies, authorization headers, or full published subscription URLs.
