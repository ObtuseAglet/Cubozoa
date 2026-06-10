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

Early development. **Milestones 1 and 2 are complete:** an unmodified Jellyfin
client can discover Cubozoa, log in, and **browse libraries** that Cubozoa scans
from disk. Point it at a folder of movies and the items show up in the client,
with titles and years parsed from filenames. See [the roadmap](#roadmap) for
what is next.

Playback is not implemented yet (that is M4), so items browse but do not yet
stream. The compatibility and security model are proven end-to-end with tests.

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

### Configuration

All configuration is via environment variables; every one has a safe default.

| Variable | Default | Description |
| --- | --- | --- |
| `CUBOZOA_BIND_ADDRESS` | `:8096` | Listen address (`host:port`). |
| `CUBOZOA_DATA_DIR` | OS config dir `/cubozoa` | Where the datastore and server identity live. |
| `CUBOZOA_MEDIA_DIR` | _(empty)_ | Root folder whose subdirectories are auto-registered as libraries (type inferred from the folder name). |
| `CUBOZOA_SERVER_NAME` | hostname | Friendly name shown on the login screen. |
| `CUBOZOA_PUBLIC_BASE_URL` | _(empty)_ | Address advertised to clients; otherwise reflected from the request. |
| `CUBOZOA_ADMIN_USERNAME` | `admin` | Username for the seeded first-run admin. |
| `CUBOZOA_ADMIN_PASSWORD` | _(generated)_ | Password for the seeded admin. If unset, a strong one is generated. |
| `CUBOZOA_TRUST_PROXY_HEADERS` | `false` | Honor `X-Forwarded-*` (only enable behind a trusted reverse proxy). |

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
  jellyfin            # Jellyfin wire DTOs + auth-header protocol parsing
  server              # routing, middleware, and the Jellyfin-compatible handlers
```

The layers depend inward: handlers use the `auth` service, which uses the
`store` interface and `security` primitives. The `store` interface means the
current dependency-free JSON store can be swapped for SQLite/Postgres without
touching business logic as the data model grows.

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

## Roadmap

- [x] **M1 — Client handshake & login.** Discovery, branding, full
      authentication and session lifecycle. Existing Jellyfin clients connect
      and log in. *(done)*
- [x] **M2 — Libraries & browse.** Filesystem scanner, media/metadata model,
      and `Items`/`Views` endpoints so clients can browse a library. Libraries
      auto-register from `CUBOZOA_MEDIA_DIR`; titles/years are parsed from
      filenames. *(done)*
- [ ] **M3 — Images & metadata.** Artwork serving and external metadata
      providers.
- [ ] **M4 — Playback.** Direct play, then HLS transcoding via ffmpeg.
- [ ] **M5 — Multi-user & sharing.** User management UI, per-library access,
      the Plex-grade onboarding experience.

## License

License to be finalized. Until then, all rights reserved by the project authors.
