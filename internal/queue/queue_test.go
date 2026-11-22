package queue_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/queue"
	"github.com/Njunge11/rejea/internal/testdb"
)

// addTypeArgs is a job that adds one row to doc_types.
type addTypeArgs struct {
	ID string `json:"id"`
}

func (addTypeArgs) Kind() string { return "add_type" }

type addTypeWorker struct {
	river.WorkerDefaults[addTypeArgs]

	pool *pgxpool.Pool
	// timeLeft receives the time that the job had when it started.
	timeLeft chan time.Duration
}

func (w *addTypeWorker) Work(ctx context.Context, job *river.Job[addTypeArgs]) error {
	deadline, _ := ctx.Deadline()
	select {
	case w.timeLeft <- time.Until(deadline):
	default:
	}
	_, err := w.pool.Exec(ctx,
		`insert into doc_types (id, description, property_schema) values ($1, 'Added by a job', '{}')`, job.Args.ID)
	return err
}

func TestClientRunsJob(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	worker := &addTypeWorker{pool: pool, timeLeft: make(chan time.Duration, 1)}
	workers := river.NewWorkers()
	river.AddWorker(workers, worker)

	const jobTimeout = 10 * time.Minute
	client, err := queue.NewClient(pool, workers, jobTimeout)
	if err != nil {
		t.Fatal(err)
	}
	events, cancel := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Stop(stopCtx); err != nil {
			t.Error(err)
		}
	})

	// The job is added in a transaction, as the upload path will add it with
	// the document row.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.InsertTx(ctx, tx, addTypeArgs{ID: "sop"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-events:
		if event.Kind != river.EventKindJobCompleted {
			t.Fatalf("job ended as %s: %v", event.Kind, event.Job.Errors)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the job did not run in 10 seconds")
	}

	var rows int
	if err := pool.QueryRow(ctx, `select count(*) from doc_types where id = 'sop'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("rows written by the job: got %d, want 1", rows)
	}

	// River cancels a job after 1 minute by default. The client must give
	// the job the timeout that the caller set.
	if left := <-worker.timeLeft; left <= time.Minute || left > jobTimeout {
		t.Errorf("time that the job had: got %s, want more than 1m and at most %s", left, jobTimeout)
	}
}
