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
internal/userdata  per-user resume position, watched/favorite state
internal/transcode optional ffprobe (metadata) + ffmpeg (on-demand HLS) wrappers
internal/metadata  optional external metadata provider (TMDb)
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

M1 (login), M2 (libraries + browse), M3 (local artwork), M4 (direct-play
playback), and M4c (resume/watched/favorite state via `internal/userdata`,
persisted per-user and surfaced in item DTOs + `/Users/{id}/Items/Resume`) are
done. Per-user endpoints enforce that a caller only acts on their own data
unless they are an admin (`effectiveUserID`). Libraries auto-register from
`CUBOZOA_MEDIA_DIR`;
`internal/media` scans them into items and serves `Items`/`Views`/`Latest`.
Posters/backdrops found next to media are served from `/Items/{id}/Images/{type}`
(see `media/images.go`). Playback: `/Items/{id}/PlaybackInfo` advertises direct
play and `/Videos/{id}/stream` serves raw bytes via `http.ServeContent` (Range
seek support); `media.ItemStream`/`ItemImage` both resolve an item ID to a file
and re-check it lives inside the library root (`withinRoot`) — clients never
supply a path. Item DTOs and MediaSources deliberately omit the filesystem
`Path`.

M4b (ffprobe metadata during scans + on-demand HLS transcoding via ffmpeg) is
done: ffmpeg/ffprobe are optional (discovered at startup; features advertised
only when present). Transcode sessions live under `CUBOZOA_DATA_DIR/transcodes`,
are reaped when idle and killed on shutdown; segment names are strictly
validated and the input path is resolved server-side via `media.ItemStream`
(never client-supplied). Routing note: a `GET` mux pattern also matches `HEAD`,
so don't register a separate `HEAD` route (it conflicts with literal sibling
paths like `main.m3u8`).

M5 (TV hierarchy) is done: a "tvshows" library is scanned into synthetic
Series and Season folder items plus Episodes (parsed from `SxxEyy`/`NxM`/`Season
NN` in `media/naming.go`); browse is parent-based (`ListItemsByParent`), and
`/Shows/{id}/Seasons`, `/Shows/{id}/Episodes`, and `/Shows/NextUp` back the
client's series pages. Movies/other libraries stay flat.

Subtitles (external sidecars discovered during scan, advertised as external
MediaStreams, delivered via `/Videos/{id}/Subtitles/{index}/{file}` with
on-the-fly SubRip→WebVTT conversion) and M3b (optional TMDb enrichment:
overview/rating/genres + downloaded artwork cached under
`CUBOZOA_DATA_DIR/metadata-images`, which `media.ItemImage` is allowed to serve
in addition to the library root) are done. The metadata provider is opt-in via
`CUBOZOA_TMDB_API_KEY` and gracefully disabled otherwise.

Edge hardening is in: direct HTTPS serving (TLS 1.2+) with HSTS over TLS,
persistent per-account lockout after repeated failures (generic 401, no
enumeration; policy via `CUBOZOA_LOCKOUT_*`), and structured audit logging
(`internal/audit`, events emitted from the auth handlers, optional
`CUBOZOA_AUDIT_LOG_FILE`).

Open next: music libraries (Artist/Album/Track), seek-aware/adaptive
transcoding, embedded-subtitle extraction, 2FA.
