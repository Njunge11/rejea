package server

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Njunge11/rejea/internal/documents"
)

// addRoutes adds each route of the service to mux. All routes are here, so
// that one read shows the full API.
//
// For a path or a method that has no route, http.ServeMux answers 404 or
// 405 with a plain text body. That answer stays: RFC 9457 says that a plain
// status code is sufficient when the status tells the full problem.
func addRoutes(mux *http.ServeMux, logger *slog.Logger, pool *pgxpool.Pool, docs *documents.Service, cfg Config) {
	mux.Handle(healthPattern, handle(logger, health(pool, cfg.HealthTimeout)))
	mux.Handle("POST /documents", handle(logger, createDocuments(docs, cfg.MaxRequestBytes)))
	mux.Handle("GET /documents", handle(logger, listDocuments(docs)))
	mux.Handle("GET /documents/{id}", handle(logger, getDocument(docs)))
	mux.Handle("POST /documents/{id}/confirm", handle(logger, confirmUpload(docs)))
	mux.Handle("POST /documents/{id}/retry", handle(logger, retryDocument(docs)))
}
