package server

import (
	"log/slog"
	"net/http"
	"time"
)

// logRequests writes one log line for each request, after its answer. A
// server error is logged as an error. The health check is logged at the
// debug level, because a monitor calls it again and again.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)

		status := sw.status
		if status == 0 {
			// The handler wrote nothing, and net/http then sends 200.
			status = http.StatusOK
		}
		level := slog.LevelInfo
		switch {
		case status >= http.StatusInternalServerError:
			level = slog.LevelError
		case r.Pattern == healthPattern:
			level = slog.LevelDebug
		}
		logger.Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration", time.Since(start),
		)
	})
}

// statusWriter keeps the status that a handler sends.
type statusWriter struct {
	http.ResponseWriter

	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the original writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
