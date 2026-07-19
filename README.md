# Cubozoa

**A secure-by-design media server that speaks the Jellyfin client API.**

Cubozoa aims to be what many self-hosters want: the open ecosystem and existing
client apps of [Jellyfin](https://jellyfin.org), the polish and "it just works"
ease of Plex, and a codebase where security is a first-class design constraint
rather than an afterthought.

> Cubozoa is the class of box jellyfish — small, fast, and not to be trifled with.

## Why

- **Bring your own clients.** Cubozoa implements the Jellyfin server API, so the
  Jellyfin apps you already use (web, mobile, TV, Kodi, etc.) connect to it
  unchanged. No new client to install, no ecosystem to rebuild.
- **Secure by default.** Every default is the safe one. Strong password hashing
  (argon2id), token hashes at rest, constant-time secret comparison, conservative
  HTTP timeouts, rate-limited authentication, and a deliberately small dependency
  surface. There are no insecure "convenience" toggles.
- **Plex-like ease.** A single static binary, zero required configuration, and a
  first-run experience that generates a strong admin password for you. Point it
  at a folder and go.

## Status

Early development, but already a working media server for direct-play content.
**Milestones 1–4 (including 4b transcoding and 4c resume) are complete:** an
unmodified Jellyfin client can discover Cubozoa, log in, **browse libraries**
scanned from disk (with **poster/backdrop artwork** and **ffprobe-sourced
durations and codecs**), **play media** — via direct play with full seek support
(HTTP Range) or **on-demand HLS transcoding** (ffmpeg) for formats a client
can't play natively — and **resume where it left off** (per-user position,
watched status and favorites persist across restarts). **TV libraries** are
organized into Series → Season → Episode (with "Next Up"), and **music** into
Artist → Album → Track. **External subtitles** are delivered (SubRip→WebVTT),
an optional **TMDb provider** enriches items, and the edge is hardened with
**HTTPS, account lockout and audit logging**. See [the roadmap](#roadmap) for
what is next.

ffmpeg and ffprobe are **optional**: when present, Cubozoa probes media for
metadata and offers HLS transcoding; when absent, direct play still works and
those features are simply disabled. The compatibility and security model are
proven end-to-end with tests (transcoding output is validated to be playable
H.264/AAC).

## Quick start

Requires Go 1.25+.

```bash
# Build the single binary
go build -o cubozoa ./cmd/cubozoa

# Run it (binds :8096, the Jellyfin default port)
./cubozoa

# ...or point it at your media and libraries are discovered automatically:
CUBOZOA_MEDIA_DIR=/srv/media ./cubozoa
```

With `CUBOZOA_MEDIA_DIR` set, each immediate subdirectory becomes a library —
name a folder `Movies` or `TV Shows` and Cubozoa infers its type. Libraries are
(re)scanned in the background at startup; trigger a manual rescan any time with
`POST /Library/Refresh`.

On first run with no configured password, Cubozoa creates an `admin` account and
prints a generated password **once** — capture it. Then point any Jellyfin
client at `http://<host>:8096`.

### Run with Docker

The image bundles ffmpeg/ffprobe, runs as an unprivileged user, and mounts your
media read-only:

```bash
docker run -d --name cubozoa \
  -p 8096:8096 \
  -v cubozoa-data:/data \
  -v /path/to/media:/media:ro \
  ghcr.io/obtuseaglet/cubozoa:latest
# grab the generated admin password:
docker logs cubozoa
```

Or with Compose — copy `docker-compose.yml`, point the `media` volume at your
library root, and `docker compose up -d`.

### Prebuilt binaries

Tagged releases publish static, dependency-free binaries for linux, macOS and
Windows (amd64/arm64) plus `SHA256SUMS`. Download one, mark it executable, and
run it — no runtime to install. (For transcoding, have `ffmpeg` on `PATH` or set
`CUBOZOA_FFMPEG_PATH`.) Build them yourself with `make dist`.

### Configuration

All configuration is via environment variables; every one has a safe default.

| Variable | Default | Description |
| --- | --- | --- |
| `CUBOZOA_BIND_ADDRESS` | `:8096` | Listen address (`host:port`). |
| `CUBOZOA_DATA_DIR` | OS config dir `/cubozoa` | Where the datastore and server identity live. |
| `CUBOZOA_STORE` | `bolt` | Datastore backend: `bolt` (embedded bbolt B+tree, `cubozoa.db`), `json` (legacy single file, `cubozoa.json`), or `postgres` (external PostgreSQL, requires `CUBOZOA_DATABASE_URL`). |
| `CUBOZOA_DATABASE_URL` | _(empty)_ | PostgreSQL connection string (pgx/libpq URL or DSN) used when `CUBOZOA_STORE=postgres`. Read only from the environment; never logged. |
| `CUBOZOA_MEDIA_DIR` | _(empty)_ | Root folder whose subdirectories are auto-registered as libraries (type inferred from the folder name). |
| `CUBOZOA_SERVER_NAME` | hostname | Friendly name shown on the login screen. |
| `CUBOZOA_PUBLIC_BASE_URL` | _(empty)_ | Address advertised to clients; otherwise reflected from the request. |
| `CUBOZOA_ADMIN_USERNAME` | `admin` | Username for the seeded first-run admin. |
| `CUBOZOA_ADMIN_PASSWORD` | _(generated)_ | Password for the seeded admin. If unset, a strong one is generated. |
| `CUBOZOA_TRUST_PROXY_HEADERS` | `false` | Honor `X-Forwarded-*` (only enable behind a trusted reverse proxy). |
| `CUBOZOA_FFMPEG_PATH` | _(PATH lookup)_ | ffmpeg binary for HLS transcoding; transcoding is disabled if not found. |
| `CUBOZOA_FFPROBE_PATH` | _(PATH lookup)_ | ffprobe binary for media metadata; scans record titles/years only if not found. |
| `CUBOZOA_TMDB_API_KEY` | _(empty)_ | Enables TMDb metadata enrichment (overviews, ratings, genres, artwork). Disabled if unset. |
| `CUBOZOA_TLS_CERT_FILE` / `CUBOZOA_TLS_KEY_FILE` | _(empty)_ | Serve HTTPS directly (TLS 1.2+). Set both, or terminate TLS at a proxy. |
| `CUBOZOA_AUDIT_LOG_FILE` | _(stdout)_ | Append structured security-event (audit) JSON to this file. |
| `CUBOZOA_LOCKOUT_THRESHOLD` / `CUBOZOA_LOCKOUT_MINUTES` | `5` / `15` | Failed logins before an account locks, and the cool-off window. `0` disables lockout. |
| `CUBOZOA_IPTV_M3U` | _(empty)_ | M3U playlist (URL or path) whose channels are exposed as Live TV. Empty disables Live TV. |
| `CUBOZOA_IPTV_EPG` | _(auto)_ | XMLTV guide(s) for the program guide — comma-separated URLs/paths, `.gz` supported. If unset, the guide advertised in the playlist's `url-tvg` header is used automatically. |
| `CUBOZOA_IPTV_EPG_ALIASES` | _(empty)_ | JSON file mapping `"Channel Name": "guide.tvg.id"` — an operator override for channels that neither `tvg-id` nor name-matching join to the guide. |
| `CUBOZOA_IPTV_REFRESH_HOURS` | `12` | How often the IPTV playlist and EPG are reloaded. |

## Architecture

Cubozoa is written in Go for memory safety, a single static deployable binary,
trivial cross-compilation, and excellent streaming/concurrency — the right tools
for a secure, easy-to-run media server.

```
cmd/cubozoa            # entrypoint: config, wiring, hardened HTTP server, signals
internal/
  config              # env-driven configuration with safe defaults
  security            # argon2id hashing, secure tokens, constant-time compare
  store               # storage interface + JSON-backed implementation (swappable)
  auth                # account seeding, credential verification, session lifecycle
  media               # library scanner, browse service, artwork & stream resolution
  userdata            # per-user resume position, watched/favorite state
  transcode           # optional ffprobe (metadata) + ffmpeg (HLS) wrappers
  metadata            # optional external metadata provider (TMDb)
  jellyfin            # Jellyfin wire DTOs + auth-header protocol parsing
  server              # routing, middleware, and the Jellyfin-compatible handlers
```

The layers depend inward: handlers use the `auth` service, which uses the
`store` interface and `security` primitives. The `store` interface means the
backend is swappable without touching business logic: Cubozoa ships an embedded
bbolt B+tree store (default), a legacy single-file JSON store, and an optional
external PostgreSQL backend for large or multi-node deployments — all selectable
via `CUBOZOA_STORE` and covered by one shared conformance test suite.

## Security model

See [SECURITY.md](SECURITY.md) for the full threat model and reporting policy.
Highlights enforced in code today:

- **Passwords** are stored only as argon2id hashes (OWASP-tuned parameters).
- **Access tokens** are stored only as SHA-256 hashes; the plaintext is returned
  to the client once and never persisted.
- **No user enumeration:** login failures are indistinguishable and timed
  uniformly whether or not the account exists.
- **Brute-force resistance:** per-IP token-bucket rate limiting on auth.
- **Hardened transport:** slowloris-resistant timeouts, capped request bodies,
  conservative security headers, panic recovery, spoof-resistant client-IP
  handling.

### Two-factor authentication (TOTP)

2FA is opt-in per account and works with **unmodified Jellyfin clients** — they
only have a password field, so once enabled you simply append your current
6-digit code to your password when logging in (e.g. `mypassword123456`).

Enrollment and recovery use Cubozoa-native endpoints (authenticate first to get
a token):

| Endpoint | Purpose |
| --- | --- |
| `POST /Cubozoa/2FA/Setup` | Returns a `Secret` and an `otpauth://` URI to add to an authenticator app. |
| `POST /Cubozoa/2FA/Activate` `{"Code":"123456"}` | Confirms the secret and returns one-time **recovery codes** (shown once). |
| `GET /Cubozoa/2FA/Status` | Whether 2FA is enabled for the caller. |
| `POST /Cubozoa/2FA/Disable` `{"Code":"123456"}` | Turns 2FA off (requires a current code, so a stolen session can't). |
| `POST /Cubozoa/2FA/Recover` `{"Username","Pw","RecoveryCode"}` | Unauthenticated lost-device recovery: password + a recovery code disables 2FA. |

Secrets and recovery-code hashes are stored at rest; recovery codes are
single-use. The implementation is RFC 6238 (SHA-1, 6 digits, 30 s) and
interoperates with standard authenticator apps.

## Roadmap

- [x] **M1 — Client handshake & login.** Discovery, branding, full
      authentication and session lifecycle. Existing Jellyfin clients connect
      and log in. *(done)*
- [x] **M2 — Libraries & browse.** Filesystem scanner, media/metadata model,
      and `Items`/`Views` endpoints so clients can browse a library. Libraries
      auto-register from `CUBOZOA_MEDIA_DIR`; titles/years are parsed from
      filenames. *(done)*
- [x] **M3 — Local artwork.** Posters and backdrops are discovered next to
      media (name-matched, plus `poster`/`fanart`/`folder` for single-video
      folders) and served via `/Items/{id}/Images/{type}` with ETag/conditional
      requests. *(done)*
- [x] **M3b — External metadata.** Optional TMDb provider enriches movies and
      series with overviews, ratings, genres and artwork (downloaded and cached)
      when a key is configured. *(done)*
- [x] **Subtitles.** External sidecar subtitles are discovered and delivered,
      and embedded text subtitle tracks are extracted on demand — both delivered
      as WebVTT. *(done)*
- [x] **M4 — Direct-play playback.** `PlaybackInfo` negotiation and raw media
      streaming via `/Videos/{id}/stream` with HTTP Range (seek) support, plus
      playback progress reporting. *(done)*
- [x] **M4c — Resume & watched state.** Per-user resume position, played status
      and favorites persist across restarts and surface in item DTOs and a
      "Continue Watching" (`/Users/{id}/Items/Resume`) row. *(done)*
- [x] **M5 — TV hierarchy.** "tvshows" libraries are organized into
      Series → Season → Episode from filenames (`SxxEyy`/`NxM`/`Season NN`),
      with `/Shows/{id}/Seasons`, `/Shows/{id}/Episodes`, and a real
      `/Shows/NextUp`. *(done)*
- [x] **Music hierarchy.** "music" libraries are organized into
      MusicArtist → MusicAlbum → Audio (track) from the `Artist/Album/NN Title`
      layout, with an `/Artists` endpoint. *(done)*
- [x] **Edge hardening.** Direct HTTPS (TLS 1.2+) with HSTS, persistent account
      lockout (no user enumeration), and structured audit logging. *(done)*
- [x] **Packaging.** Multi-stage Docker image (ffmpeg bundled, non-root),
      Compose example, and a tagged-release workflow publishing static
      cross-platform binaries + a multi-arch image. *(done)*
- [x] **Two-factor auth (TOTP).** Opt-in per account, compatible with stock
      Jellyfin clients (append the 6-digit code to your password), with
      authenticator-app enrollment and single-use recovery codes. *(done)*
- [x] **Live TV (IPTV).** An M3U playlist becomes browsable, playable channels
      in clients' Live TV section (upstream remuxed to HLS via ffmpeg, with a
      re-encode fallback), plus channel logos, an XMLTV program guide
      (gzip/auto-discovery/multi-source) with now-playing on tiles, and
      **DVR** — scheduled recordings that persist and play back. *(done)*
- [x] **M4b — Transcoding & metadata.** ffprobe-sourced durations/codecs in
      browse and `PlaybackInfo`, plus on-demand HLS transcoding via ffmpeg —
      **seek-aware** and **adaptive (multi-bitrate)** with an ABR master
      playlist — advertised only when ffmpeg is present. *(done)*
- [ ] **M5 — Multi-user & sharing.** User management UI, per-library access,
      the Plex-grade onboarding experience.

## License

License to be finalized. Until then, all rights reserved by the project authors.
