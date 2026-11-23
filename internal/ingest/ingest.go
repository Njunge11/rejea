// Package ingest has the workers that take an uploaded file to a document
// that the service can search. Each worker does the jobs of one queue.
package ingest

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/storage"
)

// Config is the limits of the work on a file.
type Config struct {
	// UploadMaxAge is the longest time that a document can have the status
	// uploading. After it, the document fails.
	UploadMaxAge time.Duration
	// UploadCheckInterval is the time between two looks for such documents.
	UploadCheckInterval time.Duration
	// MaxFileBytes is the largest size of one file.
	MaxFileBytes int64
	// MaxArchiveFiles is the largest number of PDF and Word files in one
	// archive.
	MaxArchiveFiles int
	// MaxUnpackedBytes is the largest size of the content of one archive
	// when it is unpacked.
	MaxUnpackedBytes int64
	// MaxArchiveListBytes is the largest size of the list of the files in
	// one zip.
	MaxArchiveListBytes int64
}

// Workers gives River the worker of each kind of job.
func Workers(pool *pgxpool.Pool, store *storage.Store, logger *slog.Logger, cfg Config) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, &fileWorker{pool: pool, store: store, logger: logger, cfg: cfg})
	river.AddWorker(workers, &expireWorker{pool: pool, store: store, logger: logger, maxAge: cfg.UploadMaxAge})
	return workers
}
