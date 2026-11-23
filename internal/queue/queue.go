// Package queue runs the background jobs of the service on River. The jobs
// are rows in PostgreSQL and the workers are goroutines in this process.
package queue

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// The queues. Each queue has one kind of work and its own workers, so a
// slow kind of work does not stop the other kinds.
const (
	File  = "file"  // download, hash, duplicate check, unpack an archive
	Parse = "parse" // render the pages, parse
	Index = "index" // classify, chunk, embed, graph
)

// Config is the settings of the queues.
type Config struct {
	// FileWorkers, ParseWorkers and IndexWorkers are the numbers of jobs
	// that each queue runs at the same time.
	FileWorkers  int
	ParseWorkers int
	IndexWorkers int
	// JobTimeout is the longest time that one attempt of a job can run. The
	// default of River is 1 minute, which is too short to parse a large
	// file.
	JobTimeout time.Duration
	// MaxAttempts is the number of times that a job runs before it fails
	// with no new attempt.
	MaxAttempts int
}

// NewClient makes the River client of the three queues. River refuses to
// start a client that has no worker. River logs through logger.
func NewClient(pool *pgxpool.Pool, logger *slog.Logger, workers *river.Workers, cfg Config) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		JobTimeout:  cfg.JobTimeout,
		Logger:      logger,
		MaxAttempts: cfg.MaxAttempts,
		Queues: map[string]river.QueueConfig{
			File:  {MaxWorkers: cfg.FileWorkers},
			Parse: {MaxWorkers: cfg.ParseWorkers},
			Index: {MaxWorkers: cfg.IndexWorkers},
		},
		Workers: workers,
	})
}
