package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Njunge11/rejea/internal/config"
	"github.com/Njunge11/rejea/internal/documents"
	"github.com/Njunge11/rejea/internal/queue"
)

const usage = `usage:
  rejea migrate                       bring the database schema up to date
  rejea serve                         run the service
  rejea health                        ask the service of this machine if it is ready
  rejea reindex --all                 make the chunks of each parsed document again
  rejea reindex <document-id>...      make the chunks of these documents again`

// validCommand tells if the arguments are a command of the service.
func validCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "migrate", "serve", "health":
		return len(args) == 1
	case "reindex":
		return len(args) > 1
	}
	return false
}

// errRefused tells that the command changed no document, or not each one.
var errRefused = errors.New("not each document went to the index queue")

// reindex puts parsed documents in the index queue again. The command
// changes rows and adds jobs. The service that runs does the work: it
// makes the chunks and their vectors again from the saved blocks, with its
// own settings. The parser does not run.
func reindex(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg config.Config, args []string, stdout io.Writer) error {
	inserter, err := queue.NewInserter(pool, logger, cfg.Queue)
	if err != nil {
		return err
	}
	// The rules for the index work use no file, so they get no file store.
	docs := documents.New(pool, nil, inserter, nil, nil, cfg.Documents)

	const next = "The service makes the new chunks: a document has the status indexing until they are saved. If the service does not run, the jobs wait for it."
	if len(args) == 1 && args[0] == "--all" {
		count, err := docs.ReindexAll(ctx)
		if err != nil {
			return err
		}
		if count == 0 {
			_, err = fmt.Fprintln(stdout, "No document has a complete parse: no document went to the index queue.")
			return err
		}
		_, err = fmt.Fprintf(stdout, "Documents in the index queue: %d\n%s\n", count, next)
		return err
	}

	// Each ID is examined before a document changes.
	ids := make([]uuid.UUID, len(args))
	for i, arg := range args {
		id, err := uuid.Parse(arg)
		if err != nil {
			return fmt.Errorf("%q is not a document ID. An ID has the form 01a20e0e-81f5-7043-922b-4ed3749f5436: GET /documents lists the documents. For all documents: rejea reindex --all", arg)
		}
		ids[i] = id
	}
	refused := 0
	for _, id := range ids {
		err := docs.Reindex(ctx, id)
		conflict, isConflict := errors.AsType[*documents.Conflict](err)
		switch {
		case err == nil:
			_, err = fmt.Fprintf(stdout, "%s  in the index queue\n", id)
		case errors.Is(err, documents.ErrNotFound):
			refused++
			_, err = fmt.Fprintf(stdout, "%s  refused: no document has this ID. GET /documents lists the documents.\n", id)
		case isConflict:
			refused++
			_, err = fmt.Fprintf(stdout, "%s  refused: %s\n", id, conflict.Detail)
		}
		if err != nil {
			return err
		}
	}
	if refused > 0 {
		return fmt.Errorf("%w: %d of %d refused, with the reason on each line above", errRefused, refused, len(ids))
	}
	_, err = fmt.Fprintln(stdout, next)
	return err
}
