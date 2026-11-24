// Command rejea is the Rejea service.
//
//	rejea migrate   brings the database schema up to date
//	rejea serve     runs the HTTP server
//	rejea health    asks the HTTP server of this machine if it is ready
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/riverqueue/river/rivertype"

	"github.com/Njunge11/rejea/internal/chunk"
	"github.com/Njunge11/rejea/internal/config"
	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/documents"
	"github.com/Njunge11/rejea/internal/ingest"
	"github.com/Njunge11/rejea/internal/parse"
	"github.com/Njunge11/rejea/internal/pdf"
	"github.com/Njunge11/rejea/internal/queue"
	"github.com/Njunge11/rejea/internal/server"
	"github.com/Njunge11/rejea/internal/storage"
	"github.com/Njunge11/rejea/internal/tokens"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rejea:", err)
		if fix := advice(err); fix != "" {
			fmt.Fprintln(os.Stderr, "  To correct:", fix)
		}
		os.Exit(1)
	}
}

// run writes the result of a command to stdout and the log to stderr.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	if !validCommand(args) {
		return errors.New(usage)
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		return fmt.Errorf("%w:\n%w", errSettings, err)
	}
	if args[0] == "health" {
		return health(ctx, cfg)
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

	// The service stops here, with the reason, when a part that it cannot
	// work without is missing or wrong: the schema, the file store, the
	// port. If not, the reason shows only at the first request.
	if err := database.Current(ctx, pool); err != nil {
		return err
	}
	if args[0] == "reindex" {
		return reindex(ctx, pool, logger, cfg, args[1:], stdout)
	}
	store, err := storage.New(ctx, logger, cfg.Storage)
	if err != nil {
		return err
	}

	pdfs, err := pdf.New(logger, cfg.PDF)
	if err != nil {
		return err
	}
	// The jobs stop before PDFium: this call runs after the wait for them.
	defer pdfs.Close() //nolint:errcheck // The service stops.

	// The workers run in this process. They stop when ctx is cancelled:
	// River takes no new job and waits for the jobs in work.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The rules for documents. The HTTP server and the workers call them.
	inserter, err := queue.NewInserter(pool, logger, cfg.Queue)
	if err != nil {
		return err
	}
	docs := documents.New(pool, store, inserter, cfg.Documents)

	failureRule := []rivertype.Middleware{ingest.FailureRule(docs, logger, cfg.Ingest.FailureSaveTimeout)}
	parser, err := parse.NewPaddleOCR(cfg.Parse)
	if err != nil {
		return err
	}
	words, err := parse.NewWord(cfg.Word)
	if err != nil {
		return err
	}
	warnOfMissingReaders(ctx, logger, cfg, parser, words)
	// The tokenizer of the embedding model: the chunker and the embedding
	// client count tokens with it.
	counter, err := tokens.New(cfg.Tokens)
	if err != nil {
		return err
	}
	defer counter.Close() //nolint:errcheck // The service stops.
	chunker, err := chunk.New(counter, cfg.Chunk)
	if err != nil {
		return err
	}
	if err := checkDimensions(ctx, pool, cfg); err != nil {
		return err
	}
	embedder, err := startEmbedder(ctx, logger, cfg, counter)
	if err != nil {
		return err
	}
	workers := ingest.Workers(docs, store, pdfs, parser, words, chunker, embedder, logger, cfg.Ingest)
	jobs, err := queue.NewClient(pool, logger, workers, failureRule, ingest.Schedule(cfg.Ingest), cfg.Queue)
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
		case <-time.After(cfg.Queue.StopTimeout + cfg.Server.ShutdownTimeout):
			logger.Error("the jobs did not stop in time")
		}
	}()
	handler := server.NewHandler(logger, pool, docs, cfg.Server)

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("the HTTP server cannot listen on %s: %w", cfg.HTTPAddr, err)
	}
	logger.Info("http server listening", "addr", listener.Addr().String())
	return server.Run(ctx, listener, handler, logger, cfg.Server)
}

// health asks the HTTP server of this machine if it is ready. It is the
// health check of the container of the service: the image has no program
// that sends an HTTP request. The caller gives the time limit.
func health(ctx context.Context, cfg config.Config) error {
	_, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("the address of the HTTP server: %w", err)
	}
	address := "http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close() //nolint:errcheck // The answer is read.
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answers %s", address, response.Status)
	}
	return nil
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
