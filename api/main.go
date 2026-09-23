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
	"syscall"
	"time"

	// Distroless carries no zoneinfo, so APP_TIMEZONE would fail to load there.
	_ "time/tzdata"

	"github.com/caarlos0/env/v11"
)

const (
	readHeaderTimeout = 5 * time.Second
	// Polling several feeds in one request can take a few seconds.
	writeTimeout  = 45 * time.Second
	idleTimeout   = 60 * time.Second
	shutdownGrace = 10 * time.Second

	dbStartupWait  = 30 * time.Second
	dbRetryDelay   = time.Second
	probeTimeout   = 5 * time.Second
	probeEndpoint  = "/healthz"
	probeLocalHost = "127.0.0.1"
)

type config struct {
	DatabaseURL string `env:"DATABASE_URL" envDefault:"postgres://docketiq:docketiq@postgres:5432/docketiq?sslmode=disable"`
	APIAddr     string `env:"API_ADDR" envDefault:":8080"`
	AppTimezone string `env:"APP_TIMEZONE" envDefault:"America/New_York"`
}

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz endpoint and exit")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	if err := run(*healthcheck); err != nil {
		slog.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run(healthcheck bool) error {
	var cfg config
	if err := env.Parse(&cfg); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if healthcheck {
		return probe(cfg.APIAddr)
	}

	loc, err := time.LoadLocation(cfg.AppTimezone)
	if err != nil {
		return fmt.Errorf("load timezone %s: %w", cfg.AppTimezone, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := NewDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := waitForDB(ctx, db); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           newServer(db, loc, time.Now).routes(),
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	listening := make(chan error, 1)
	go func() { listening <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", cfg.APIAddr, "timezone", loc.String())

	select {
	case err := <-listening:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	// A fresh context: the signal already cancelled ctx, and in-flight
	// requests still get their grace period.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// waitForDB keeps pinging until Postgres answers. Compose waits for the
// container to report healthy, but a restarting database can still refuse
// connections for a few seconds.
func waitForDB(ctx context.Context, db *DB) error {
	deadline := time.Now().Add(dbStartupWait)
	for {
		err := db.Ping(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("database unreachable after %s: %w", dbStartupWait, err)
		}
		slog.Warn("database not ready, retrying", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(dbRetryDelay):
		}
	}
}

// probe backs the -healthcheck flag. Distroless has no shell or curl, so the
// container healthcheck runs this binary against its own endpoint.
func probe(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("parse %s: %w", addr, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	url := "http://" + net.JoinHostPort(probeLocalHost, port) + probeEndpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}
	return nil
}
