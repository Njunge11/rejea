package documents

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/queue"
)

// StartIndex ends the parse of a document and starts the work that makes
// its chunks: the document moves from the status parsing to the status
// indexing and gets its index job, both in one transaction. A document is
// never indexing with no job.
//
// It reports false, and changes nothing, when the document does not have
// the status parsing. It refuses a document that has no parser: the
// parser did not read each page.
func (s *Service) StartIndex(ctx context.Context, id uuid.UUID) (bool, error) {
	var started bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		doc, err := database.GetDocument(ctx, tx, id)
		if err != nil {
			return err
		}
		if doc.Status != StatusParsing {
			return nil
		}
		if doc.Parser == "" {
			return fmt.Errorf("the parse of the document %s is not complete", id)
		}
		started, err = database.ChangeStatus(ctx, tx, id, StatusParsing, StatusIndexing)
		if err != nil || !started {
			return err
		}
		if _, err := s.jobs.InsertTx(ctx, tx, queue.IndexArgs{DocumentID: id}, nil); err != nil {
			return fmt.Errorf("add the index job of the document %s: %w", id, err)
		}
		return nil
	})
	return started, err
}

// Block is one block of a document, as the database keeps it.
type Block = database.Block

// Blocks gives the blocks of a document in the order in which a person
// reads them.
func (s *Service) Blocks(ctx context.Context, id uuid.UUID) ([]Block, error) {
	return database.ListBlocks(ctx, s.pool, id)
}

// Chunk is one piece of a document, with the vector of its text.
type Chunk = database.Chunk

// SaveChunks records the chunks of a document, in their order, and gives
// the document the status indexed. Both are one transaction: an indexed
// document has each of its chunks, and each chunk has its vector.
//
// The chunks take the place of the chunks that the document had. Each
// chunk gets its ID: the IDs are in the order of the chunks.
//
// It refuses a chunk with no text, with no block or with no vector, and a
// document that does not have the status indexing.
func (s *Service) SaveChunks(ctx context.Context, id uuid.UUID, chunks []Chunk) error {
	for i := range chunks {
		chunk := &chunks[i]
		if strings.TrimSpace(chunk.Text) == "" || len(chunk.BlockIDs) == 0 || len(chunk.Embedding) == 0 {
			return fmt.Errorf("chunk %d of the document %s has no text, no block or no vector", i+1, id)
		}
		chunkID, err := newID()
		if err != nil {
			return err
		}
		chunk.ID = chunkID
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		indexed, err := database.ChangeStatus(ctx, tx, id, StatusIndexing, StatusIndexed)
		if err != nil {
			return err
		}
		if !indexed {
			return fmt.Errorf("the document %s does not have the status %s", id, StatusIndexing)
		}
		return database.ReplaceChunks(ctx, tx, id, chunks)
	})
}

// reindexFrom is the statuses from which a parsed document can go to the
// index work again: it is indexed, or its parse ended before the service
// had an index worker.
var reindexFrom = []string{StatusIndexed, StatusParsing}

// Reindex puts a parsed document in the index work again: the document
// gets the status indexing and a new index job, both in one transaction.
// The index worker then makes its chunks and their vectors again from the
// blocks that the parse saved. The parser does not run. The document keeps
// its old chunks until the new chunks take their place.
//
// The error is ErrNotFound when no document has the ID. It is a Conflict
// when the document has no complete parse, or when it is in the index work
// already.
func (s *Service) Reindex(ctx context.Context, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		doc, err := database.GetDocument(ctx, tx, id)
		if err != nil {
			return err
		}
		switch {
		case doc.Status == StatusIndexing:
			return &Conflict{Detail: "The document is in the index work already."}
		case doc.Status == StatusFailed:
			return &Conflict{Detail: "The document has the status failed. Send POST /documents/" + id.String() + "/retry: it starts the document again and skips each stage that is complete."}
		case doc.Parser == "" || !slices.Contains(reindexFrom, doc.Status):
			return &Conflict{Detail: fmt.Sprintf("The document has the status %s and no complete parse. Only a parsed document can get new chunks.", doc.Status)}
		}
		moved, err := database.ChangeStatus(ctx, tx, id, doc.Status, StatusIndexing)
		if err != nil {
			return err
		}
		if !moved {
			return &Conflict{Detail: "The status of the document changed during the request. Send the request again."}
		}
		if _, err := s.jobs.InsertTx(ctx, tx, queue.IndexArgs{DocumentID: id}, nil); err != nil {
			return fmt.Errorf("add the index job of the document %s: %w", id, err)
		}
		return nil
	})
}

// ReindexAll puts each parsed document in the index work again, as Reindex
// does for one document. It gives the number of documents.
//
// It works in groups of ReindexBatchSize documents, each in a transaction
// of its own: a document has the status indexing and its job, or neither.
// It leaves out a document that is in the index work already, and a
// document that failed.
func (s *Service) ReindexAll(ctx context.Context) (int, error) {
	total := 0
	after := uuid.Nil
	for {
		var ids []uuid.UUID
		err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			var err error
			ids, err = database.LockParsedIDs(ctx, tx, reindexFrom, after, s.cfg.ReindexBatchSize)
			if err != nil || len(ids) == 0 {
				return err
			}
			if err := database.SetStatusOfMany(ctx, tx, ids, StatusIndexing); err != nil {
				return err
			}
			jobs := make([]river.InsertManyParams, len(ids))
			for i, id := range ids {
				jobs[i] = river.InsertManyParams{Args: queue.IndexArgs{DocumentID: id}}
			}
			if _, err := s.jobs.InsertManyTx(ctx, tx, jobs); err != nil {
				return fmt.Errorf("add the index jobs of %d documents: %w", len(ids), err)
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		total += len(ids)
		// The next group starts after the last document of this one. A
		// document that the worker indexed during the loop is thus not
		// taken a second time.
		after = ids[len(ids)-1]
	}
}
