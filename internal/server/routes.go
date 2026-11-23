package server

import (
	"log/slog"
	"net/http"
)

// addRoutes adds each route of the service to mux. All routes are here, so
// that one read shows the full API.
//
// For a path or a method that has no route, http.ServeMux answers 404 or
// 405 with a plain text body. That answer stays: RFC 9457 says that a plain
// status code is sufficient when the status tells the full problem.
func addRoutes(mux *http.ServeMux, logger *slog.Logger, db Pinger) {
	mux.Handle(healthPattern, handle(logger, health(db)))
}
