// Command sharect is the Share CT server: one Go binary, one container, one SQLite file.
//
// Configuration is environment only (no flags, no files, no secrets):
//
//	DATABASE_PATH  required  SQLite file, e.g. /data/leaderboard.db (exit 2 if unset)
//	LISTEN_ADDR    optional  default 0.0.0.0:8080
//	LOG_LEVEL      optional  default info (debug|info|warn|error)
//
// Logs are JSON lines on stdout. Startup writes nothing outside the database's directory
// and /tmp, which is all the host's read-only root filesystem allows.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/LoweKeyFlexin/share-ct/internal/namefilter"
	"github.com/LoweKeyFlexin/share-ct/internal/server"
	"github.com/LoweKeyFlexin/share-ct/internal/store"
	"github.com/LoweKeyFlexin/share-ct/migrations"
)

// Stamped by -ldflags "-X main.version=… -X main.commit=…" (see Dockerfile).
var (
	version = "dev"
	commit  = "unknown"
)

const (
	defaultListenAddr = "0.0.0.0:8080"
	defaultLogLevel   = "info"

	// shutdownTimeout stays under Docker's 10 s stop grace so a SIGTERM never
	// escalates to SIGKILL while requests drain.
	shutdownTimeout = 8 * time.Second

	exitFailure = 1
	exitConfig  = 2
)

// configError is a misconfiguration the operator must fix; it exits with exitConfig.
type configError struct{ msg string }

func (e configError) Error() string { return e.msg }

func main() {
	healthcheck := flag.Bool("healthcheck", false,
		"GET this server's own /healthz and exit 0 on ok, 1 otherwise (the container HEALTHCHECK)")
	flag.Parse()

	listenAddr := envOr("LISTEN_ADDR", defaultListenAddr)
	if *healthcheck {
		os.Exit(runHealthcheck(listenAddr))
	}

	level, levelErr := parseLevel(envOr("LOG_LEVEL", defaultLogLevel))
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	if levelErr != nil {
		log.Warn("unknown LOG_LEVEL, using info", "error", levelErr.Error())
	}

	if err := run(log, listenAddr); err != nil {
		code := exitFailure
		var ce configError
		if errors.As(err, &ce) {
			code = exitConfig
		}
		log.Error("fatal", "error", err.Error(), "exit", code)
		os.Exit(code)
	}
}

// run opens the database, applies migrations, serves until SIGTERM/SIGINT, then drains.
func run(log *slog.Logger, listenAddr string) error {
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		return configError{"DATABASE_PATH is required (e.g. /data/leaderboard.db)"}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	log.Info("starting",
		"version", version, "commit", commit, "go", runtime.Version(),
		"database_path", dbPath, "listen_addr", listenAddr,
		"name_policy_version", namefilter.Version,
		"name_policy_normalization", namefilter.Normalization,
		"name_policy_sha256", namefilter.AssetSHA256())

	db, err := store.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	res, err := db.Migrate(ctx, migrations.FS)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	log.Info("migrations", "applied", res.Applied, "from", res.From, "to", res.To)

	return serve(ctx, log, listenAddr, server.New(log, db, time.Now))
}

// serve binds listenAddr, logs the bound address, and shuts down gracefully when ctx ends.
func serve(ctx context.Context, log *slog.Logger, listenAddr string, handler http.Handler) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}
	log.Info("listening", "addr", ln.Addr().String())

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutting down", "timeout", shutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	log.Info("stopped")
	return nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// parseLevel maps LOG_LEVEL to a slog level; unknown values fall back to info.
func parseLevel(s string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo, fmt.Errorf("LOG_LEVEL %q: %w", s, err)
	}
	return level, nil
}
