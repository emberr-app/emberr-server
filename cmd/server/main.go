package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emberr-app/emberr-server/internal/api"
	"github.com/emberr-app/emberr-server/internal/store"
)

const shutdownTimeout = 10 * time.Second

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stopListeningForSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopListeningForSignals()

	settings, err := loadSettings()
	if err != nil {
		return err
	}

	pool, err := pgxpool.New(ctx, settings.databaseURL())
	if err != nil {
		return fmt.Errorf("configure database pool: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	slog.Info("connected to database")

	if err := store.ApplyMigrations(pool, "migrations"); err != nil {
		return err
	}

	server := &http.Server{
		Addr:    net.JoinHostPort("", settings.httpPort),
		Handler: api.NewRouter(pool),
	}

	serverStopped := make(chan error, 1)
	go func() {
		slog.Info("server listening", "address", server.Addr)
		serverStopped <- server.ListenAndServe()
	}()

	select {
	case err := <-serverStopped:
		return fmt.Errorf("serve http: %w", err)
	case <-ctx.Done():
	}

	stopListeningForSignals()
	slog.Info("shutting down")

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()

	if err := server.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shut down http server: %w", err)
	}
	slog.Info("server stopped cleanly")
	return nil
}
