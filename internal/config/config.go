// Package config loads Cubozoa's runtime configuration from environment
// variables with safe, self-hosting-friendly defaults.
//
// Configuration is deliberately small. The guiding principle is that a fresh
// install should boot and be usable with zero configuration (the Plex-like
// "it just works" experience), while every security-relevant default is the
// safe one.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds the fully resolved runtime configuration.
type Config struct {
	// BindAddress is the host:port the HTTP server listens on. Defaults to
	// :8096, the port existing Jellyfin clients probe by default.
	BindAddress string

	// DataDir is where Cubozoa persists its datastore and server identity.
	DataDir string

	// StoreBackend selects the persistence engine: "bolt" (default) is the
	// embedded bbolt B+tree store; "json" is the legacy single-file store, kept
	// for small deployments and debugging.
	StoreBackend string

	// MediaDir, if set, is a root directory whose immediate subdirectories are
	// auto-registered as libraries on startup (the Plex-like "point it at a
	// folder" experience). Library type is inferred from the folder name.
	MediaDir string

	// ServerName is the friendly name shown to clients on the login screen.
	ServerName string

	// PublicBaseURL, if set, is advertised to clients as the server's address.
	// Leave empty to let clients use the address they connected on.
	PublicBaseURL string

	// AdminUsername seeds the initial administrator account on first run.
	AdminUsername string

	// AdminPassword seeds the initial administrator password on first run. If
	// empty, a strong random password is generated and printed once to the log.
	AdminPassword string

	// TrustedProxies enables honoring X-Forwarded-* headers. Off by default:
	// spoofable client-address headers are ignored unless an operator running
	// behind a reverse proxy explicitly opts in.
	TrustedProxies bool

	// FFmpegPath and FFprobePath locate the ffmpeg/ffprobe binaries used for
	// transcoding and media probing. Empty means "look up on PATH"; if not
	// found, those features are simply disabled (direct play still works).
	FFmpegPath  string
	FFprobePath string

	// TMDbAPIKey enables external metadata enrichment (overviews, ratings,
	// genres, artwork) from themoviedb.org. Empty disables it. TMDbBaseURL and
	// TMDbImageBase exist mainly to support self-hosted proxies and tests.
	TMDbAPIKey    string
	TMDbBaseURL   string
	TMDbImageBase string

	// TLSCertFile and TLSKeyFile, when both set, make the server listen over
	// HTTPS directly (otherwise it serves plain HTTP, e.g. behind a TLS proxy).
	TLSCertFile string
	TLSKeyFile  string

	// AuditLogFile, when set, appends structured security events to this file
	// (otherwise they go to stdout alongside operational logs).
	AuditLogFile string

	// LockoutThreshold is the number of consecutive failed logins that locks an
	// account; 0 disables lockout. LockoutDuration is the cool-off window.
	LockoutThreshold int
	LockoutDuration  time.Duration

	// IPTVPlaylist is an M3U playlist (URL or file path) whose channels are
	// exposed as Live TV. Empty disables Live TV. IPTVGuide is an optional XMLTV
	// EPG (URL or path); IPTVRefresh is how often both are reloaded.
	IPTVPlaylist string
	IPTVGuide    string
	IPTVRefresh  time.Duration

	// IPTVAliases is a JSON file mapping channel name -> guide tvg-id, an
	// operator override for channels that neither tvg-id nor name-matching join
	// to the guide correctly.
	IPTVAliases string
}

// Load resolves configuration from the environment, applying defaults.
func Load() (*Config, error) {
	c := &Config{
		BindAddress:      env("CUBOZOA_BIND_ADDRESS", ":8096"),
		DataDir:          env("CUBOZOA_DATA_DIR", defaultDataDir()),
		StoreBackend:     strings.ToLower(env("CUBOZOA_STORE", "bolt")),
		MediaDir:         env("CUBOZOA_MEDIA_DIR", ""),
		ServerName:       env("CUBOZOA_SERVER_NAME", defaultServerName()),
		PublicBaseURL:    strings.TrimRight(env("CUBOZOA_PUBLIC_BASE_URL", ""), "/"),
		AdminUsername:    env("CUBOZOA_ADMIN_USERNAME", "admin"),
		AdminPassword:    env("CUBOZOA_ADMIN_PASSWORD", ""),
		TrustedProxies:   envBool("CUBOZOA_TRUST_PROXY_HEADERS", false),
		FFmpegPath:       env("CUBOZOA_FFMPEG_PATH", ""),
		FFprobePath:      env("CUBOZOA_FFPROBE_PATH", ""),
		TMDbAPIKey:       env("CUBOZOA_TMDB_API_KEY", ""),
		TMDbBaseURL:      env("CUBOZOA_TMDB_BASE_URL", ""),
		TMDbImageBase:    env("CUBOZOA_TMDB_IMAGE_BASE", ""),
		TLSCertFile:      env("CUBOZOA_TLS_CERT_FILE", ""),
		TLSKeyFile:       env("CUBOZOA_TLS_KEY_FILE", ""),
		AuditLogFile:     env("CUBOZOA_AUDIT_LOG_FILE", ""),
		LockoutThreshold: envInt("CUBOZOA_LOCKOUT_THRESHOLD", 5),
		LockoutDuration:  time.Duration(envInt("CUBOZOA_LOCKOUT_MINUTES", 15)) * time.Minute,
		IPTVPlaylist:     env("CUBOZOA_IPTV_M3U", ""),
		IPTVGuide:        env("CUBOZOA_IPTV_EPG", ""),
		IPTVRefresh:      time.Duration(envInt("CUBOZOA_IPTV_REFRESH_HOURS", 12)) * time.Hour,
		IPTVAliases:      env("CUBOZOA_IPTV_EPG_ALIASES", ""),
	}

	if c.AdminUsername == "" {
		return nil, fmt.Errorf("config: CUBOZOA_ADMIN_USERNAME must not be empty")
	}

	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return nil, fmt.Errorf("config: CUBOZOA_TLS_CERT_FILE and CUBOZOA_TLS_KEY_FILE must be set together")
	}

	if c.StoreBackend != "bolt" && c.StoreBackend != "json" {
		return nil, fmt.Errorf("config: CUBOZOA_STORE must be \"bolt\" or \"json\", got %q", c.StoreBackend)
	}

	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return nil, fmt.Errorf("config: resolving data dir: %w", err)
	}
	c.DataDir = abs

	if c.MediaDir != "" {
		if abs, err := filepath.Abs(c.MediaDir); err == nil {
			c.MediaDir = abs
		}
	}

	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}

func defaultDataDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "cubozoa")
	}
	return filepath.Join(".", "cubozoa-data")
}

func defaultServerName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "Cubozoa"
}
