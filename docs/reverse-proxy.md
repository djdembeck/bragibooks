# Reverse proxy

Bragibooks is designed for one operator on a trusted network. A reverse proxy can add TLS and authentication, but the app still makes two independent checks:

- **Host authority.** `SERVER_ALLOWED_HOSTS` / `server.allowed_hosts` must accept the `Host` the backend receives. `localhost` and literal loopback/private addresses are already accepted; an external DNS name must be listed exactly.
- **Client address.** `BRAGIBOOKS_TRUSTED_PROXIES` is consulted only for keyless LAN access. Only `X-Forwarded-For` is parsed. `X-Real-IP` and `Forwarded` are not parsed; a request carrying any of the three from an untrusted peer, or without a verifiable client chain from a trusted peer, fails closed.

With an accepted `Host`, the SPA shell and `GET /api/health` are served without API authorization. `/api/*` and `/covers/*` are protected. A forwarded public client therefore loads the shell but gets 403 on protected requests while the External API is off and 401 while it is on, because the stock UI sends no key.

## Choose a mode

| Mode | Proxy behavior | Required application settings | Result |
| --- | --- | --- | --- |
| LAN TLS proxy | Clients are on the LAN. The proxy preserves the external `Host` and may forward XFF. | `SERVER_ALLOWED_HOSTS=<name>` when preserving an external name; `BRAGIBOOKS_TRUSTED_PROXIES=<proxy IP/CIDR>` only when forwarding XFF. | UI and API work for LAN clients. |
| Remote API only | The proxy preserves `Host`, or sends an allowed backend `Host`. | `SERVER_ALLOWED_HOSTS=<external name>` when preserving; External API on; client sends `Authorization: Bearer bk_…`. | API works. Trusted proxies are not required for key requests. The shell loads; protected requests still need the key. |
| Remote UI behind an authenticating proxy | The proxy authenticates every request and strips all forwarding headers. | `SERVER_ALLOWED_HOSTS=<external name>` when preserving; no `X-Forwarded-For`, `X-Real-IP`, or `Forwarded`. | The app sees the proxy as a LAN peer, so keyless UI, API, covers, and SSE work. The backend must not be reachable directly by untrusted clients. |
| Remote UI with key injection | The proxy injects a key on every request. | External API on; accepted `Host`. | Works only with CSRF and cover/SSE handling; see the caveat below. |

## LAN TLS proxy

If the proxy preserves the external hostname, list it exactly:

```sh
SERVER_ALLOWED_HOSTS=books.lan
```

If it rewrites the backend `Host` to `localhost` or a literal private address, no entry is needed. The app uses `Host` only for validation and logging; it serves relative UI/API paths.

If the proxy forwards `X-Forwarded-For`, list its address or network so the app can identify the client:

```sh
BRAGIBOOKS_TRUSTED_PROXIES=192.168.1.10
```

A trusted proxy whose XFF chain identifies a LAN client keeps the keyless UI path. A chain that identifies a public client is treated as remote. If the proxy sends no forwarding headers, its own address decides; `BRAGIBOOKS_TRUSTED_PROXIES` is not needed.

## Remote API only

Host acceptance and API authentication are separate:

1. Make the backend `Host` acceptable. Preserve the external name and list it, or send an allowed backend name.
2. Enable the External API in Settings.
3. Send the generated key as `Authorization: Bearer bk_…`.

`BRAGIBOOKS_TRUSTED_PROXIES` is not required for key requests: key validation runs before the keyless LAN fallback. The SPA shell may still load without a key, but protected requests fail; the stock UI does not manage an API key. For programmatic cover downloads, send the Bearer header with the request. A browser `<img>` cannot set that header, so browser cover access needs a cookie or proxy-authenticated path.

## Remote UI behind an authenticating proxy

This is the recommended way to make the full browser UI work for clients outside the LAN without key injection:

- Authenticate every route at the proxy: the SPA, `/api/*`, `/covers/*`, and the SSE job stream.
- Remove `X-Forwarded-For`, `X-Real-IP`, and `Forwarded` before the request reaches Bragibooks. Otherwise the app fails closed unless a trusted XFF chain identifies a LAN client.
- Preserve the external `Host` and list it in `SERVER_ALLOWED_HOSTS`, or rewrite the upstream `Host` to `localhost` or a literal private address.
- Keep the backend port off the public network. Any client that can reach it directly on the trusted network can use the keyless UI.

For example, the header portion of an Nginx location:

```nginx
proxy_set_header Host $host;                 # or: proxy_set_header Host localhost;
proxy_set_header X-Forwarded-For "";
proxy_set_header X-Real-IP "";
proxy_set_header Forwarded "";
proxy_buffering off;                         # keep SSE live
proxy_read_timeout 1h;                       # raise to cover the longest job
```

Add the proxy's authentication directive at the same level, and verify that direct backend access is blocked. `BRAGIBOOKS_TRUSTED_PROXIES` is not a substitute for this mode: with the headers removed, the proxy's own loopback/LAN address is what admits the request.

## Key injection caveat

A proxy can inject a valid key instead of stripping headers, but this is security-sensitive:

- Bearer-authenticated requests are exempt from the app's CSRF check because browsers cannot normally set `Authorization` cross-origin. A proxy that adds the header invalidates that assumption. Prefer cookie authentication, which still passes the CSRF guard, or enforce equivalent CSRF protection at the proxy.
- Browser `<img>` cover requests and `EventSource` SSE streams cannot set `Authorization` themselves. The proxy must inject it on every request, or use cookie authentication.
- The stock UI clears a leftover root-path `bragibooks_token` cookie on load, so an injected cookie must account for that.

Prefer the authenticating-proxy or VPN path unless key injection is tightly controlled.

## Forwarding-header details

- Only `X-Forwarded-For` is parsed for client identity. `X-Real-IP` and `Forwarded` only trigger the fail-closed check; a proxy that sends only one of those breaks keyless LAN access.
- The XFF chain is walked right-to-left, skipping trusted proxy hops. The first untrusted, parseable entry is the client. An unverifiable chain fails closed; the proxy's own address is never used as a fallback.
- `X-Forwarded-Host` and the host part of `Forwarded` never override `Host`.
- Ports on host authorities are validated but ignored for matching, so published ports keep working. IPv6 authorities must be bracketed; zone identifiers are not supported.
- `SERVER_ALLOWED_HOSTS` entries are exact names or literal addresses, not URLs, suffixes, or wildcards. Set it before start and restart after changing; invalid entries stop startup. One trailing dot and DNS case are ignored.
- `BRAGIBOOKS_TRUSTED_PROXIES` is env-only and read at request time; restart the process after changing it.

## IPv6 limitation

The Host guard accepts IPv6 ULA and link-local literals, but the keyless LAN classifier recognizes only IPv6 loopback (`::1`) and the IPv4 RFC 1918 ranges. A native IPv6 ULA or link-local client therefore needs the External API key or an IPv4 path. This is a deliberate limit, not a Host-allowlist failure.
