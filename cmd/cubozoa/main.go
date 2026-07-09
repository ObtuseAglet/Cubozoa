// Command cubozoa is the Cubozoa media server: a secure-by-design,
// single-binary media server that speaks the Jellyfin client API.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/audit"
	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/config"
	"github.com/obtuseaglet/cubozoa/internal/livetv"
	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/metadata"
	"github.com/obtuseaglet/cubozoa/internal/server"
	"github.com/obtuseaglet/cubozoa/internal/store"
	"github.com/obtuseaglet/cubozoa/internal/transcode"
	"github.com/obtuseaglet/cubozoa/internal/userdata"
)

// version is Cubozoa's own product version, distinct from the Jellyfin API
// version it emulates. Overridable at build time with -ldflags.
var version = "0.1.0-dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log.Info("starting cubozoa",
		"version", version,
		"data_dir", cfg.DataDir,
		"bind", cfg.BindAddress,
	)

	st, err := store.OpenJSON(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	// Audit log: structured security events, optionally to a dedicated file.
	auditWriter, auditClose, err := openAuditWriter(cfg.AuditLogFile)
	if err != nil {
		return err
	}
	defer auditClose()
	auditLog := audit.New(auditWriter)

	authSvc := auth.New(st, log)
	authSvc.SetLockoutPolicy(cfg.LockoutThreshold, cfg.LockoutDuration)

	// First-run: seed the administrator account. If no password was supplied,
	// a strong random one is generated and printed exactly once.
	generated, created, err := authSvc.SeedAdmin(cfg.AdminUsername, cfg.AdminPassword)
	if err != nil {
		return err
	}
	if created {
		announceAdmin(log, cfg.AdminUsername, generated)
	}

	// Optional media tooling. ffprobe enriches scans with duration/codec
	// metadata; ffmpeg enables on-demand HLS transcoding. Both are optional —
	// when absent, direct play still works.
	mediaSvc := media.NewService(st, log)
	if prober, ok := transcode.NewProber(cfg.FFprobePath); ok {
		mediaSvc.SetProber(prober)
		log.Info("ffprobe available; media will be probed during scans")
	} else {
		log.Info("ffprobe not found; scans will record titles/years only")
	}
	if tmdb, ok := newTMDb(cfg); ok {
		mediaSvc.SetEnricher(tmdb, filepath.Join(cfg.DataDir, "metadata-images"))
		log.Info("TMDb metadata enrichment enabled")
	}

	// Libraries: register any present under the configured media directory,
	// then scan in the background so startup stays fast even for large media
	// collections. Browse results fill in as scans complete.
	if cfg.MediaDir != "" {
		if err := mediaSvc.SyncLibrariesFromMediaDir(cfg.MediaDir); err != nil {
			log.Warn("syncing libraries from media dir", "dir", cfg.MediaDir, "err", err)
		}
	}
	go func() {
		if err := mediaSvc.ScanAll(); err != nil {
			log.Warn("initial library scan", "err", err)
		}
	}()

	userDataSvc := userdata.New(st)
	srv := server.New(cfg, st, authSvc, mediaSvc, userDataSvc, log)
	srv.SetAudit(auditLog)

	if mgr, ok := transcode.NewManager(cfg.FFmpegPath, filepath.Join(cfg.DataDir, "transcodes"), log); ok {
		srv.SetTranscoder(mgr)
		defer mgr.Close()
		log.Info("ffmpeg available; HLS transcoding enabled")
	} else {
		log.Info("ffmpeg not found; transcoding disabled (direct play only)")
	}

	// Optional IPTV / Live TV from an M3U playlist.
	if ltv, ok := livetv.NewService(cfg.IPTVPlaylist, cfg.IPTVRefresh, log); ok {
		ltv.Start(context.Background())
		srv.SetLiveTV(ltv)
		defer ltv.Close()
		log.Info("Live TV enabled", "playlist", cfg.IPTVPlaylist)
	}

	httpServer := &http.Server{
		Addr:    cfg.BindAddress,
		Handler: srv.Handler(),
		// Conservative timeouts protect against slowloris-style attacks and
		// leaked connections. ReadHeaderTimeout is the key slowloris defense.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	tlsEnabled := cfg.TLSCertFile != "" && cfg.TLSKeyFile != ""
	if tlsEnabled {
		// Modern TLS floor: refuse anything below 1.2.
		httpServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	// Run the server until a signal asks us to stop, then drain gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.BindAddress, "tls", tlsEnabled)
		var serveErr error
		if tlsEnabled {
			serveErr = httpServer.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			log.Warn("serving plaintext HTTP; terminate TLS at a proxy or set CUBOZOA_TLS_CERT_FILE/KEY_FILE for HTTPS")
			serveErr = httpServer.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received, draining connections")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Info("stopped cleanly")
	return nil
}

// openAuditWriter returns the destination for audit events. When a path is
// configured, events are appended to that file (created 0600); otherwise they
// go to stdout. The returned close function releases a file if one was opened.
func openAuditWriter(path string) (io.Writer, func(), error) {
	if path == "" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("opening audit log: %w", err)
	}
	return f, func() { f.Close() }, nil
}

// newTMDb builds a TMDb metadata provider from configuration, applying any
// base-URL overrides. It returns ok=false when no API key is configured.
func newTMDb(cfg *config.Config) (*metadata.TMDb, bool) {
	var opts []metadata.TMDbOption
	if cfg.TMDbBaseURL != "" {
		opts = append(opts, metadata.WithBaseURL(cfg.TMDbBaseURL))
	}
	if cfg.TMDbImageBase != "" {
		opts = append(opts, metadata.WithImageBase(cfg.TMDbImageBase))
	}
	return metadata.NewTMDb(cfg.TMDbAPIKey, opts...)
}

// announceAdmin prints the seeded administrator credentials prominently. A
// generated password is shown once and never persisted in plaintext, so the
// operator must capture it now.
func announceAdmin(log *slog.Logger, username, generated string) {
	if generated == "" {
		log.Info("created initial administrator account", "username", username)
		return
	}
	banner := strings.Repeat("=", 64)
	log.Warn("created initial administrator account with a generated password")
	// Use plain stderr for the secret so it is not interleaved into structured
	// log aggregation by accident, and is obvious in an interactive console.
	os.Stderr.WriteString("\n" + banner + "\n")
	os.Stderr.WriteString("  Cubozoa initial administrator credentials\n")
	os.Stderr.WriteString("  username: " + username + "\n")
	os.Stderr.WriteString("  password: " + generated + "\n")
	os.Stderr.WriteString("  (shown once — store it now; set CUBOZOA_ADMIN_PASSWORD to override)\n")
	os.Stderr.WriteString(banner + "\n\n")
}
