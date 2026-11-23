package ingest

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/documents"
	"github.com/Njunge11/rejea/internal/queue"
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

	docs   *documents.Service
	logger *slog.Logger
}

// Work fails each upload that is too old.
func (w *expireWorker) Work(ctx context.Context, _ *river.Job[queue.ExpireUploadsArgs]) error {
	failed, err := w.docs.FailOldUploads(ctx)
	if err != nil {
		return err
	}
	if failed > 0 {
		w.logger.InfoContext(ctx, "old uploads failed", "documents", failed)
	}
	return nil
}
