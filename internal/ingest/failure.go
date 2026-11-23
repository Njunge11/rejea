package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/Njunge11/rejea/internal/documents"
)

// FailureRule is the failure rule of all workers, as a River middleware.
//
// A job that returns an error runs again after a wait: River does that. A
// worker that finds a fault that a new attempt cannot correct returns
// river.JobCancel, and River stops the job at once. When a job of a
// document fails with no new attempt, the rule gives the document the
// status failed and the reason. River deletes its own record of the job
// after some days, so the document must keep the reason.
//
// The rule is a middleware and not a River ErrorHandler, because River does
// not call the ErrorHandler for a job that a worker cancels.
func FailureRule(docs *documents.Service, logger *slog.Logger, saveTimeout time.Duration) rivertype.WorkerMiddleware {
	return &failureRule{docs: docs, logger: logger, saveTimeout: saveTimeout}
}

type failureRule struct {
	river.WorkerMiddlewareDefaults

	docs        *documents.Service
	logger      *slog.Logger
	saveTimeout time.Duration
}

// Work runs the job and saves the failure of its document when the job has
// no new attempt.
func (rule *failureRule) Work(ctx context.Context, job *rivertype.JobRow, doInner func(context.Context) error) (err error) {
	// River catches a panic and counts it as an error. The rule lets the
	// panic continue to River after it saved the failure.
	defer func() {
		if value := recover(); value != nil {
			if job.Attempt >= job.MaxAttempts {
				rule.save(ctx, job, fmt.Sprintf("panic: %v", value))
			}
			panic(value)
		}
	}()

	err = doInner(ctx)
	if err == nil {
		return nil
	}
	cancelErr, cancelled := errors.AsType[*rivertype.JobCancelError](err)
	switch {
	case cancelled:
		// The worker found a fault that a new attempt cannot correct.
		rule.save(ctx, job, cancelErr.Unwrap().Error())
	case errors.Is(context.Cause(ctx), rivertype.ErrJobCancelledRemotely):
		// A person cancelled the job, and River then cancels it for good.
		rule.save(ctx, job, "the job was cancelled")
	case stopped(ctx, err):
		// The service stops. River puts the job back in its queue and does
		// not count the attempt.
	case job.Attempt >= job.MaxAttempts:
		rule.save(ctx, job, err.Error())
	}
	return err
}

// stopped tells if a job returned an error because the service stops. It
// uses the same check as River: the context of the job was cancelled with
// the stop of River as its cause, and the error is that cancel.
func stopped(ctx context.Context, err error) bool {
	if !errors.Is(ctx.Err(), context.Canceled) {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.Cause(ctx))
}

// save writes the failure on the document of the job. A job with no
// document in its arguments has nothing to save.
func (rule *failureRule) save(ctx context.Context, job *rivertype.JobRow, reason string) {
	var args struct {
		DocumentID *uuid.UUID `json:"document_id"`
	}
	if err := json.Unmarshal(job.EncodedArgs, &args); err != nil || args.DocumentID == nil {
		return
	}
	// The context of a job that ran out of time is cancelled, but the
	// failure must be saved all the same.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rule.saveTimeout)
	defer cancel()
	found, err := rule.docs.Fail(ctx, *args.DocumentID, reason)
	if err != nil {
		rule.logger.ErrorContext(ctx, "the failure of a document was not saved",
			"document", *args.DocumentID, "job", job.ID, "reason", reason, "err", err)
		return
	}
	if found {
		rule.logger.WarnContext(ctx, "document failed",
			"document", *args.DocumentID, "job", job.ID, "kind", job.Kind, "attempt", job.Attempt, "reason", reason)
	}
}
