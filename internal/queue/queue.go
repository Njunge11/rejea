// Package queue runs the background jobs of the service on River. The jobs
// are rows in PostgreSQL and the workers are goroutines in this process.
package queue

import (
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

// The queues. Each queue has one kind of work and its own workers, so a
// slow kind of work does not stop the other kinds.
const (
	File  = "file"  // download, hash, duplicate check, unpack an archive
	Parse = "parse" // render the pages, parse
	Index = "index" // classify, chunk, embed, graph
	// Maintenance has the jobs that River adds at a fixed interval to keep
	// the data clean. Such a job is one short database statement, so the
	// queue has one worker.
	Maintenance = "maintenance"
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
	// StopTimeout is the time that the jobs in work get to finish when the
	// service stops. After it, River cancels them, and they run again at
	// the next start.
	StopTimeout time.Duration
}

// FileArgs is the job of the file queue: the first work on an uploaded
// file. The package documents adds it and the file worker does it.
type FileArgs struct {
	DocumentID uuid.UUID `json:"document_id"`
}

// Kind is the name of the job in the database.
func (FileArgs) Kind() string { return "file" }

// InsertOpts puts the job in the file queue.
func (FileArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{Queue: File} }

// ExpireUploadsArgs is the job that fails the uploads that no client
// confirmed. River adds it at a fixed interval.
type ExpireUploadsArgs struct{}

// Kind is the name of the job in the database.
func (ExpireUploadsArgs) Kind() string { return "expire_uploads" }

// InsertOpts puts the job in the maintenance queue.
func (ExpireUploadsArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{Queue: Maintenance} }

// NewInserter makes a River client that adds jobs and runs none. The rules
// of the service add their jobs through it. The workers call those rules,
// so the rules cannot use the client that runs the workers: that client is
// made from the workers.
//
// A job gets its number of attempts from the client that adds it, thus the
// inserter has the same MaxAttempts as the client of the queues.
func NewInserter(pool *pgxpool.Pool, logger *slog.Logger, cfg Config) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:      logger,
		MaxAttempts: cfg.MaxAttempts,
	})
}

// NewClient makes the River client of the queues. River refuses to start a
// client that has no worker. River logs through logger. Each job runs
// inside the middleware. River adds the periodic jobs at their intervals.
func NewClient(pool *pgxpool.Pool, logger *slog.Logger, workers *river.Workers, middleware []rivertype.Middleware, periodic []*river.PeriodicJob, cfg Config) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		JobTimeout:   cfg.JobTimeout,
		Logger:       logger,
		MaxAttempts:  cfg.MaxAttempts,
		Middleware:   middleware,
		PeriodicJobs: periodic,
		// When the context of Start is cancelled, River takes no new job
		// and waits this long for the jobs in work.
		SoftStopTimeout: cfg.StopTimeout,
		Queues: map[string]river.QueueConfig{
			File:  {MaxWorkers: cfg.FileWorkers},
			Parse: {MaxWorkers: cfg.ParseWorkers},
			Index: {MaxWorkers: cfg.IndexWorkers},

			Maintenance: {MaxWorkers: 1},
		},
		Workers: workers,
	})
}
