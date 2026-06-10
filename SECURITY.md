# Security Policy

Security is the reason Cubozoa exists. This document describes the project's
security posture and how to report issues.

## Reporting a vulnerability

Please report suspected vulnerabilities privately rather than opening a public
issue. Open a [GitHub security advisory](https://github.com/obtuseaglet/cubozoa/security/advisories/new)
for the repository. We aim to acknowledge reports promptly and to coordinate
disclosure.

Do not include working exploit details in public channels until a fix is
available.

## Design principles

These are the rules the codebase is held to. A change that violates one of them
should not merge.

1. **Safe defaults only.** Every default value is the secure one. There are no
   configuration toggles that trade security for convenience.
2. **Secrets are never stored in the clear.** Passwords are argon2id hashes;
   access tokens are stored as SHA-256 hashes. Plaintext secrets exist only
   transiently in memory.
3. **Constant-time comparison for secrets.** Token and password comparisons use
   constant-time primitives to avoid timing oracles.
4. **No information leakage.** Authentication responses do not reveal whether an
   account exists, and timing is equalized. Internal errors are logged, never
   returned to clients.
5. **Minimal attack surface.** Dependencies are kept to a minimum and audited.
   The current server depends only on the Go standard library and
   `golang.org/x/crypto`.
6. **Defense in depth at the edge.** Conservative HTTP timeouts (slowloris
   defense), capped request bodies, rate-limited authentication, security
   response headers, and panic recovery are always on.
7. **Least privilege.** Non-administrative users cannot read other users'
   records; admin-only endpoints enforce the administrator role.

## Current hardening checklist

| Control | Status |
| --- | --- |
| argon2id password hashing (salted, OWASP params) | ✅ |
| Access tokens hashed at rest (SHA-256) | ✅ |
| Constant-time secret comparison | ✅ |
| User-enumeration & timing resistance on login | ✅ |
| Per-IP auth rate limiting | ✅ |
| Slowloris-resistant server timeouts | ✅ |
| Request body size limits | ✅ |
| Security response headers | ✅ |
| Panic recovery middleware | ✅ |
| Spoof-resistant client IP (proxy headers opt-in) | ✅ |
| Atomic, 0600-permissioned datastore writes | ✅ |
| TLS termination / HTTPS guidance | ⏳ planned |
| Account lockout / 2FA | ⏳ planned |
| Audit logging | ⏳ planned |
