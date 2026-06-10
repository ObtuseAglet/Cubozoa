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
)

// Config holds the fully resolved runtime configuration.
type Config struct {
	// BindAddress is the host:port the HTTP server listens on. Defaults to
	// :8096, the port existing Jellyfin clients probe by default.
	BindAddress string

	// DataDir is where Cubozoa persists its datastore and server identity.
	DataDir string

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
}

// Load resolves configuration from the environment, applying defaults.
func Load() (*Config, error) {
	c := &Config{
		BindAddress:    env("CUBOZOA_BIND_ADDRESS", ":8096"),
		DataDir:        env("CUBOZOA_DATA_DIR", defaultDataDir()),
		MediaDir:       env("CUBOZOA_MEDIA_DIR", ""),
		ServerName:     env("CUBOZOA_SERVER_NAME", defaultServerName()),
		PublicBaseURL:  strings.TrimRight(env("CUBOZOA_PUBLIC_BASE_URL", ""), "/"),
		AdminUsername:  env("CUBOZOA_ADMIN_USERNAME", "admin"),
		AdminPassword:  env("CUBOZOA_ADMIN_PASSWORD", ""),
		TrustedProxies: envBool("CUBOZOA_TRUST_PROXY_HEADERS", false),
	}

	if c.AdminUsername == "" {
		return nil, fmt.Errorf("config: CUBOZOA_ADMIN_USERNAME must not be empty")
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
