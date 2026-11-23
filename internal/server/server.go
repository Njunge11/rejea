// Package server is the HTTP server of the service.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Njunge11/rejea/internal/storage"
)

// Config is the limits of the HTTP API.
type Config struct {
	// MaxUploadFiles is the largest number of files in one request for
	// upload URLs.
	MaxUploadFiles int
	// MaxFileBytes is the largest size of one uploaded file.
	MaxFileBytes int64
	// MaxRequestBytes is the largest size of the JSON body of a request.
	MaxRequestBytes int64
}

// NewHandler makes the handler of the service: the routes, with one log
// line for each request.
func NewHandler(logger *slog.Logger, pool *pgxpool.Pool, store *storage.Store, cfg Config) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, logger, pool, store, cfg)
	return logRequests(logger, mux)
}

// Run serves handler on the listener until ctx is cancelled. Then it lets
// the open requests finish, for 10 seconds at most.
func Run(ctx context.Context, listener net.Listener, handler http.Handler, logger *slog.Logger) error {
	// A net/http server has no timeouts by default. Without them, a slow or
	// silent client holds a connection open for as long as it wants.
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second, // to read the request headers
		ReadTimeout:       30 * time.Second, // to read the full request
		WriteTimeout:      30 * time.Second, // to write the response
		IdleTimeout:       5 * time.Minute,  // to wait for the next request on a kept connection
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
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-failed; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
