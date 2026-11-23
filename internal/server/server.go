// Package server is the HTTP server of the service.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/storage"
)

// Config is the limits of the HTTP server and of its API.
type Config struct {
	// The time limits of the server. A net/http server has none by default.
	// Without them, a slow or silent client holds a connection open for as
	// long as it wants.
	ReadHeaderTimeout time.Duration // to read the request headers
	ReadTimeout       time.Duration // to read the full request
	WriteTimeout      time.Duration // to write the response
	IdleTimeout       time.Duration // to wait for the next request on a kept connection
	// ShutdownTimeout is the time that open requests get to finish when
	// the server stops.
	ShutdownTimeout time.Duration
	// HealthTimeout is the time that the database gets to answer the
	// health check.
	HealthTimeout time.Duration

	// MaxUploadFiles is the largest number of files in one request for
	// upload URLs.
	MaxUploadFiles int
	// MaxFileBytes is the largest size of one uploaded file.
	MaxFileBytes int64
	// MaxRequestBytes is the largest size of the JSON body of a request.
	MaxRequestBytes int64
}

// NewHandler makes the handler of the service: the routes, with one log
// line for each request. The endpoints add their jobs through jobs.
func NewHandler(logger *slog.Logger, pool *pgxpool.Pool, store *storage.Store, jobs *river.Client[pgx.Tx], cfg Config) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, logger, pool, store, jobs, cfg)
	return logRequests(logger, mux)
}

// Run serves handler on the listener until ctx is cancelled. Then it lets
// the open requests finish, for cfg.ShutdownTimeout at most.
func Run(ctx context.Context, listener net.Listener, handler http.Handler, logger *slog.Logger, cfg Config) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		// The errors that net/http finds in a connection or a handler.
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	failed := make(chan error, 1)
	go func() { failed <- srv.Serve(listener) }()

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	logger.Info("http server shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-failed; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
