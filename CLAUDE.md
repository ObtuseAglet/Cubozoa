# Cubozoa — guidance for AI agents

Cubozoa is a secure-by-design media server that implements the **Jellyfin client
API**, so existing Jellyfin client apps connect to it unchanged. The product
goals, in priority order: (1) security, (2) Jellyfin client compatibility,
(3) Plex-like ease of use.

## Golden rules

- **Security is the product.** Read `SECURITY.md` before touching auth, crypto,
  sessions, or the HTTP edge. Never weaken a default, never return internal
  error detail to clients, never log secrets, always compare secrets in
  constant time, always hash passwords (argon2id) and tokens (SHA-256).
- **Compatibility is a contract.** The JSON field names/casing in
  `internal/jellyfin` are what real clients deserialize. Changing them can break
  clients. When adding endpoints, match Jellyfin's request/response shapes.
- **Keep the dependency surface tiny.** Current deps: Go stdlib +
  `golang.org/x/crypto`. Adding a dependency is a security decision — justify it.

## Layout

```
cmd/cubozoa        entrypoint: config, wiring, hardened http.Server, signals
internal/config    env-driven config, safe defaults
internal/security  argon2id, secure tokens, constant-time compare  (crypto lives ONLY here)
internal/store     Store interface + JSON impl; swap for SQL later without touching callers
internal/auth      seeding, credential verification, session lifecycle
internal/media     library scanner + browse service (filesystem -> items)
internal/jellyfin  wire DTOs + auth-header parsing (the compatibility contract)
internal/server    routing, middleware, handlers
```

Dependencies point inward: `server` → `auth` → `store`/`security`. Don't create
cycles or let handlers reach past `auth` into crypto details.

## Conventions

- Routing uses Go 1.22+ `http.ServeMux` method+wildcard patterns; the most
  specific pattern wins (so `/Users/Me` beats `/Users/{id}`).
- Anonymous vs. authenticated is enforced by `requireAuth`/`requireAdmin`
  wrappers in `routes.go`, not inside handlers.
- `withAuthContext` resolves the token into the request context for every
  request but never rejects; enforcement is the wrappers' job.
- Add a test alongside any new auth/security/protocol behavior. Tests live next
  to the code (`*_test.go`).

## Workflow

- Gate before committing: `make check` (runs `go vet` + tests). Prefer
  `make race` for concurrency-touching changes.
- Build: `make build` produces a single `cubozoa` binary.
- The datastore is a JSON file under `CUBOZOA_DATA_DIR`; it is gitignored.

## Roadmap context

M1 (client handshake + login) and M2 (libraries + browse) are done. Libraries
auto-register from `CUBOZOA_MEDIA_DIR`; `internal/media` scans them into items
and serves `Items`/`Views`/`Latest`. Item DTOs deliberately omit the filesystem
`Path` — never leak it to clients.

Next is M3 (images + external metadata), then M4 (playback: direct play, then
HLS transcoding via ffmpeg). When adding playback, `MediaItem.Path` is the
on-disk source; serve it only to authenticated users and never expose the raw
path in a DTO.
