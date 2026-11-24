// Package ingest has the workers that take an uploaded file to a document
// that the service can search. Each worker does the jobs of one queue.
package ingest

import (
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/documents"
	"github.com/Njunge11/rejea/internal/pdf"
	"github.com/Njunge11/rejea/internal/storage"
)

// Config is the limits of the work on a file.
type Config struct {
	// FailureSaveTimeout is the time that the database gets to save the
	// failure of a document.
	FailureSaveTimeout time.Duration
	// UploadCheckInterval is the time between two looks for the uploads
	// that no client confirmed.
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

// Workers gives River the worker of each kind of job. A worker does the
// work on the file and calls docs for each rule and each change of a
// document.
func Workers(docs *documents.Service, store *storage.Store, pdfs *pdf.Reader, logger *slog.Logger, cfg Config) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, &fileWorker{docs: docs, store: store, logger: logger, cfg: cfg})
	river.AddWorker(workers, &parseWorker{docs: docs, store: store, pdfs: pdfs, logger: logger})
	river.AddWorker(workers, &expireWorker{docs: docs, logger: logger})
	return workers
}
