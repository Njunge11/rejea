// Package ingest has the workers that take an uploaded file to a document
// that the service can search. Each worker does the jobs of one queue.
package ingest

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/storage"
)

// Workers gives River the worker of each kind of job.
func Workers(pool *pgxpool.Pool, store *storage.Store, logger *slog.Logger) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, &fileWorker{pool: pool, store: store, logger: logger})
	return workers
}
