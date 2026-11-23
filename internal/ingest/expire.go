package ingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/queue"
	"github.com/Njunge11/rejea/internal/storage"
)

// Schedule gives River the jobs that it adds at a fixed interval. River
// adds each of them when the service starts too, because River keeps the
// schedule in memory only: a service that starts again before the interval
// ends would not run the job at all.
//
// When many copies of the service run, only one of them adds the jobs:
// the one that River elects as the leader.
func Schedule(cfg Config) []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(cfg.UploadCheckInterval),
			func() (river.JobArgs, *river.InsertOpts) { return queue.ExpireUploadsArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
	}
}

// expireWorker fails the uploads that no client confirmed. A client that
// asks for an upload URL and then goes away leaves a document with the
// status uploading, and nothing else changes that status.
type expireWorker struct {
	river.WorkerDefaults[queue.ExpireUploadsArgs]

	pool   *pgxpool.Pool
	store  *storage.Store
	logger *slog.Logger
	maxAge time.Duration
}

// Work fails each document that has the status uploading for longer than
// the age limit, and removes its file from the file storage: a client can
// send the file and not confirm it.
//
// The change of the status and the removal of the files are one unit. If
// the removal fails, the documents keep the status uploading, and the next
// run does the work again.
func (w *expireWorker) Work(ctx context.Context, _ *river.Job[queue.ExpireUploadsArgs]) error {
	var keys []string
	err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		var err error
		keys, err = database.FailOldUploads(ctx, tx, w.maxAge)
		if err != nil || len(keys) == 0 {
			return err
		}
		return w.store.Delete(ctx, keys)
	})
	if err != nil {
		return err
	}
	if len(keys) > 0 {
		w.logger.InfoContext(ctx, "old uploads failed", "documents", len(keys), "age_limit", w.maxAge)
	}
	return nil
}
