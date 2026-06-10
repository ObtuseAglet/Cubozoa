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

Early development. **Milestone 1 is complete:** an unmodified Jellyfin client can
discover Cubozoa, render the login screen, authenticate, and hold an
authenticated session. See [the roadmap](#roadmap) for what is next.

This is not yet a usable media server — there is no library scanning or
playback yet. It is a solid, tested foundation with the compatibility and
security model proven end-to-end.

## Quick start

Requires Go 1.25+.

```bash
# Build the single binary
go build -o cubozoa ./cmd/cubozoa

# Run it (binds :8096, the Jellyfin default port)
./cubozoa
```

On first run with no configured password, Cubozoa creates an `admin` account and
prints a generated password **once** — capture it. Then point any Jellyfin
client at `http://<host>:8096`.

### Configuration

All configuration is via environment variables; every one has a safe default.

| Variable | Default | Description |
| --- | --- | --- |
| `CUBOZOA_BIND_ADDRESS` | `:8096` | Listen address (`host:port`). |
| `CUBOZOA_DATA_DIR` | OS config dir `/cubozoa` | Where the datastore and server identity live. |
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
- [ ] **M2 — Libraries & browse.** Filesystem scanner, media/metadata model,
      `Items`/`Views` endpoints so clients can browse a library.
- [ ] **M3 — Images & metadata.** Artwork serving and external metadata
      providers.
- [ ] **M4 — Playback.** Direct play, then HLS transcoding via ffmpeg.
- [ ] **M5 — Multi-user & sharing.** User management UI, per-library access,
      the Plex-grade onboarding experience.

## License

License to be finalized. Until then, all rights reserved by the project authors.
