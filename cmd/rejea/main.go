// Command rejea is the Rejea service.
//
//	rejea migrate   brings the database schema up to date
//	rejea serve     runs the HTTP server
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Njunge11/rejea/internal/config"
	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/server"
	"github.com/Njunge11/rejea/internal/storage"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rejea:", err)
		os.Exit(1)
	}
}

// run writes the result of a command to stdout and the log to stderr.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	if len(args) != 1 || (args[0] != "migrate" && args[0] != "serve") {
		return errors.New("usage: rejea migrate | rejea serve")
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	logger := newLogger(cfg, stderr)
	// A dependency that logs through the default logger of slog or through
	// the log package then writes in the same format.
	slog.SetDefault(logger)

	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if args[0] == "migrate" {
		applied, err := database.Migrate(ctx, pool)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "migrations applied: %d\n", applied)
		return err
	}

	store, err := storage.New(ctx, storage.Config{
		Bucket:        cfg.S3Bucket,
		Region:        cfg.S3Region,
		UploadURLLife: cfg.UploadURLLife,
		MaxFileBytes:  cfg.MaxFileBytes,
	})
	if err != nil {
		return err
	}
	serverCfg := server.Config{
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
		ShutdownTimeout:   cfg.ShutdownTimeout,
		HealthTimeout:     cfg.HealthTimeout,
		MaxUploadFiles:    cfg.MaxUploadFiles,
		MaxFileBytes:      cfg.MaxFileBytes,
		MaxRequestBytes:   cfg.MaxRequestBytes,
	}
	handler := server.NewHandler(logger, pool, store, serverCfg)

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}
	logger.Info("http server listening", "addr", listener.Addr().String())
	return server.Run(ctx, listener, handler, logger, serverCfg)
}

// newLogger makes the one logger of the service. Each part that logs gets it
// as an argument.
func newLogger(cfg config.Config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
