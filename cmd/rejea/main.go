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
	"time"

	"github.com/riverqueue/river/rivertype"

	"github.com/Njunge11/rejea/internal/config"
	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/ingest"
	"github.com/Njunge11/rejea/internal/queue"
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

	store, err := storage.New(ctx, logger, storage.Config{
		Bucket:        cfg.S3Bucket,
		Region:        cfg.S3Region,
		UploadURLLife: cfg.UploadURLLife,
		MaxFileBytes:  cfg.MaxFileBytes,
		ReadTimeout:   cfg.S3ReadTimeout,
		RangeBytes:    cfg.S3RangeBytes,
	})
	if err != nil {
		return err
	}

	// The workers run in this process. They stop when ctx is cancelled:
	// River takes no new job and waits for the jobs in work.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	failureRule := []rivertype.Middleware{ingest.FailureRule(pool, logger, cfg.FailureSaveTimeout)}
	ingestCfg := ingest.Config{
		UploadMaxAge:        cfg.UploadMaxAge,
		UploadCheckInterval: cfg.UploadCheckInterval,
		MaxFileBytes:        cfg.MaxFileBytes,
		MaxArchiveFiles:     cfg.MaxArchiveFiles,
		MaxUnpackedBytes:    cfg.MaxUnpackedBytes,
		MaxArchiveListBytes: cfg.MaxArchiveListBytes,
	}
	workers := ingest.Workers(pool, store, logger, ingestCfg)
	jobs, err := queue.NewClient(pool, logger, workers, failureRule, ingest.Schedule(ingestCfg), queue.Config{
		FileWorkers:  cfg.FileWorkers,
		ParseWorkers: cfg.ParseWorkers,
		IndexWorkers: cfg.IndexWorkers,
		JobTimeout:   cfg.JobTimeout,
		MaxAttempts:  cfg.JobMaxAttempts,
		StopTimeout:  cfg.JobStopTimeout,
	})
	if err != nil {
		return err
	}
	if err := jobs.Start(ctx); err != nil {
		return err
	}
	defer func() {
		cancel()
		// After the stop timeout River cancels the jobs. A job that does
		// not obey gets the shutdown timeout, then the service stops.
		select {
		case <-jobs.Stopped():
		case <-time.After(cfg.JobStopTimeout + cfg.ShutdownTimeout):
			logger.Error("the jobs did not stop in time")
		}
	}()
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
	handler := server.NewHandler(logger, pool, store, jobs, serverCfg)

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
