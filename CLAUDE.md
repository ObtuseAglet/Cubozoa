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
  `golang.org/x/crypto` + `go.etcd.io/bbolt` (embedded datastore) +
  `github.com/jackc/pgx/v5` (only used by the opt-in Postgres backend). Adding a
  dependency is a security decision — justify it.

## Layout

```
cmd/cubozoa        entrypoint: config, wiring, hardened http.Server, signals
internal/config    env-driven config, safe defaults
internal/security  argon2id, secure tokens, constant-time compare  (crypto lives ONLY here)
internal/store     Store interface + bbolt (default), JSON, and Postgres impls; swap backends without touching callers
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
keyed by item + seek offset (`StartTimeTicks` seeks via `-ss`, so seeking starts
its own session), are reaped when idle and killed on shutdown; segment names are
strictly validated and the input path is resolved server-side via
`media.ItemStream` (never client-supplied). Routing note: a `GET` mux pattern also matches `HEAD`,
so don't register a separate `HEAD` route (it conflicts with literal sibling
paths like `main.m3u8`).

M5 (TV hierarchy) is done: a "tvshows" library is scanned into synthetic
Series and Season folder items plus Episodes (parsed from `SxxEyy`/`NxM`/`Season
NN` in `media/naming.go`); browse is parent-based (`ListItemsByParent`), and
`/Shows/{id}/Seasons`, `/Shows/{id}/Episodes`, and `/Shows/NextUp` back the
client's series pages. Movies/other libraries stay flat.

Subtitles (external sidecars discovered during scan, advertised as external
MediaStreams, delivered via `/Videos/{id}/Subtitles/{index}/{file}` with
on-the-fly SubRip→WebVTT conversion; embedded *text* subtitle tracks are also
advertised when ffmpeg is present and extracted to WebVTT on demand via
`/Videos/{id}/Subtitles/embedded/{streamIndex}/{file}`) and M3b (optional TMDb
enrichment:
overview/rating/genres + downloaded artwork cached under
`CUBOZOA_DATA_DIR/metadata-images`, which `media.ItemImage` is allowed to serve
in addition to the library root) are done. The metadata provider is opt-in via
`CUBOZOA_TMDB_API_KEY` and gracefully disabled otherwise.

Live TV / IPTV is in (`internal/livetv`): an M3U playlist (`CUBOZOA_IPTV_M3U`,
URL or file, periodically refreshed) is parsed into channels exposed through the
Jellyfin Live TV API (`/LiveTv/Info,Channels,GuideInfo,Programs`; empty
recordings/timers) plus a synthetic "Live TV" view. A channel plays by remuxing
its upstream through ffmpeg into a sliding-window HLS stream
(`/Videos/{id}/live.m3u8`, `-c copy`); if the remux fails because the codecs
can't be copied (`transcode.ErrSessionFailed`, detected via a per-session `done`
channel), the handler transparently falls back to re-encoding and remembers that
per channel (`Server.liveReencodeChannels`). PlaybackInfo returns an infinite
MediaSource. An optional XMLTV guide (`CUBOZOA_IPTV_EPG`) fills `/LiveTv/Programs`
and the currently-airing program is surfaced on each channel tile
(`BaseItemDto.CurrentProgram`). Upstream URLs come from operator config, resolved
server-side by channel ID (never client-supplied). Channel logos are proxied +
cached from the `tvg-logo` URL via the item image endpoint. EPG acquisition is
robust: gzip is decompressed transparently, comma-separated `CUBOZOA_IPTV_EPG`
sources are merged, and the guide auto-discovers from the playlist header's
`url-tvg` when unset. Channels join the guide by `tvg-id` first, then fall back
to a normalized display-name match (`livetv/match.go`, built from the XMLTV
`<channel><display-name>` entries) so messy playlists still get a guide. DVR (`internal/dvr`) records channels to
`CUBOZOA_DATA_DIR/recordings` via ffmpeg on a schedule (persisted timers that
resume after restart); `/LiveTv/Timers` (POST/GET/DELETE) and `/LiveTv/Recordings`
back the client's DVR, and completed recordings play through recording-aware
PlaybackInfo + `/Videos/{id}/stream`. Series (recurring) timers
(`/LiveTv/SeriesTimers`, POST/GET/DELETE) match upcoming guide airings by
normalized title (optionally on one channel or `RecordAnyChannel`) and expand
into one-off recordings, deduped by program id; the recorder pulls upcoming
airings from the Live TV guide via a `dvr.ProgramSource` adapter wired in
`main.go`.

Adaptive (multi-bitrate) HLS is in: `/Videos/{id}/master.m3u8` returns an ABR
master playlist whose rungs come from `transcode.SelectRenditions(width,height,
bitrate)` (never upscales; top rung = source); each rung is its own scaled
transcode served at `/Videos/{id}/hls/{quality}/main.m3u8` (+ relative segment
URLs). `main.m3u8` remains the single auto-rendition stream. Session keys are
`item:startTicks:quality`. PlaybackInfo advertises the master URL.

Edge hardening is in: direct HTTPS serving (TLS 1.2+) with HSTS over TLS,
persistent per-account lockout after repeated failures (generic 401, no
enumeration; policy via `CUBOZOA_LOCKOUT_*`), and structured audit logging
(`internal/audit`, events emitted from the auth handlers, optional
`CUBOZOA_AUDIT_LOG_FILE`).

Music (M-music) is done: a "music" library is scanned into MusicArtist and
MusicAlbum folder items plus Audio tracks (parsed from `Artist/Album/NN Title`
in `media/naming.go` `parseTrack`); browse is parent-based and `/Artists` lists
artists. Movies stay flat; tvshows use the Series/Season/Episode tree.

Two-factor auth (TOTP, RFC 6238) is done: opt-in per account via Cubozoa-native
`/Cubozoa/2FA/{Setup,Activate,Status,Disable,Recover}` endpoints; secret +
recovery-code hashes live on the user record (`internal/security/totp.go`,
`internal/auth/totp.go`). Compatible with stock clients — when enabled, the
6-digit code is appended to the password and `auth.verifyCredentials` splits it.
Recovery codes are single-use; disable requires a current code.

An embedded bbolt store backend is done (`internal/store/boltstore.go`): a
durable B+tree store selected by `CUBOZOA_STORE=bolt|json` (default `bolt`).
Each call is a single transaction (per-record fsync, no whole-file rewrite), and
the hot lookups are served from secondary-index buckets — `idx_session_token`
(auth on every request), `idx_item_library`, and `idx_item_parent` (browse) —
instead of scanning. A shared conformance test (`conformance_test.go`) runs the
same assertions against both backends to guarantee parity; benchmarks
(`bench_test.go`) show the resume-write path ~28× faster and browse ~8× faster
than the JSON store. No SQL layer was added, so there is no query-language attack
surface; bbolt's only transitive dep was already indirect.

An optional Postgres backend is done (`internal/store/pgstore.go`, `jackc/pgx`):
selected by `CUBOZOA_STORE=postgres` + `CUBOZOA_DATABASE_URL`, for large/multi-node
deployments. It mirrors the bolt design — each entity is a `jsonb` blob keyed by
ID with indexed columns only for the interface's lookup dimensions
(`name_lower`, `token_hash`, `path`, item `library_id`/`parent_id`) — so the
persisted shape stays owned by the Go models. Every query is parameterized
(`$1`…), table names are compile-time constants, and unique violations map to
`ErrConflict`; `ReplaceLibraryItems`/`DeleteLibrary`/`TouchSession` run in
transactions. The schema is created on startup (`CREATE TABLE IF NOT EXISTS`) and
the connection string is read only from the env, never logged. It stays opt-in
so the default single-binary deployment (bolt) needs no external database. The
shared `conformance_test.go` runs the same assertions against Postgres too when
`CUBOZOA_TEST_DATABASE_URL` is set (skipped otherwise).
