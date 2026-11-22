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

// maxWorkers is the number of jobs that run at the same time. The parser is
// a local model that takes one file at a time.
const maxWorkers = 1

// NewClient makes the River client of the one queue. A job that runs longer
// than jobTimeout is cancelled: the default of River is 1 minute, which is
// too short to parse a large file. River refuses to start a client that has
// no worker. River logs through logger.
func NewClient(pool *pgxpool.Pool, logger *slog.Logger, workers *river.Workers, jobTimeout time.Duration) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		JobTimeout: jobTimeout,
		Logger:     logger,
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: maxWorkers},
		},
		Workers: workers,
	})
}
