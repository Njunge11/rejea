package server

import (
	"context"
	"net/http"
	"time"
)

// healthPattern is the route of the health check.
const healthPattern = "GET /healthz"

// Pinger tells if the database answers. A *pgxpool.Pool is a Pinger.
type Pinger interface {
	Ping(ctx context.Context) error
}

// health answers "ok" when the database answers before the timeout.
func health(db Pinger, timeout time.Duration) endpoint {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			return &apiError{
				status: http.StatusServiceUnavailable,
				detail: "The database does not answer.",
				cause:  err,
			}
		}
		_, _ = w.Write([]byte("ok\n"))
		return nil
	}
}
