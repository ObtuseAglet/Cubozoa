// Command cubozoa is the Cubozoa media server: a secure-by-design,
// single-binary media server that speaks the Jellyfin client API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/config"
	"github.com/obtuseaglet/cubozoa/internal/server"
	"github.com/obtuseaglet/cubozoa/internal/store"
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

	authSvc := auth.New(st, log)

	// First-run: seed the administrator account. If no password was supplied,
	// a strong random one is generated and printed exactly once.
	generated, created, err := authSvc.SeedAdmin(cfg.AdminUsername, cfg.AdminPassword)
	if err != nil {
		return err
	}
	if created {
		announceAdmin(log, cfg.AdminUsername, generated)
	}

	srv := server.New(cfg, st, authSvc, log)

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

	// Run the server until a signal asks us to stop, then drain gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.BindAddress)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
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
