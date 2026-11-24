package documents

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
