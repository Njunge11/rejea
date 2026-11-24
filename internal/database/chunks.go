package database

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

// Chunk is one row of the chunks table: one piece of a document, with the
// vector of its text.
type Chunk struct {
	ID uuid.UUID
	// HeadingPath is the headings above the text: the title of the
	// document first, the nearest heading last.
	HeadingPath []string
	// PageStart and PageEnd are the first and the last page of the text. 0
	// is saved as no page: a Word file has none.
	PageStart, PageEnd int
	// BlockIDs is the blocks that the text came from, in their order.
	BlockIDs []uuid.UUID
	// Text is the heading path, then the text of the blocks.
	Text string
	// Embedding is the vector that the embedding model gave for Text.
	Embedding []float32
}

// ListBlocks gives the blocks of a document in the order in which a person
// reads them. Each block has its page: 0 for a document with no pages.
func ListBlocks(ctx context.Context, db DB, documentID uuid.UUID) ([]Block, error) {
	rows, err := db.Query(ctx,
		`select id, coalesce(page_number, 0), seq, type, coalesce(level, 0), text from blocks
		 where document_id = $1
		 order by page_number nulls first, seq`,
		documentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list the blocks of the document %s: %w", documentID, err)
	}
	blocks, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Block, error) {
		var block Block
		err := row.Scan(&block.ID, &block.Page, &block.Seq, &block.Type, &block.Level, &block.Text)
		return block, err
	})
	if err != nil {
		return nil, fmt.Errorf("list the blocks of the document %s: %w", documentID, err)
	}
	return blocks, nil
}

// ReplaceChunks removes the chunks of a document and adds the given
// chunks.
func ReplaceChunks(ctx context.Context, db DB, documentID uuid.UUID, chunks []Chunk) error {
	batch := &pgx.Batch{}
	batch.Queue(`delete from chunks where document_id = $1`, documentID)
	for _, chunk := range chunks {
		batch.Queue(
			`insert into chunks (id, document_id, heading_path, page_start, page_end, block_ids, text, embedding)
			 values ($1, $2, coalesce($3::text[], '{}'), nullif($4::int, 0), nullif($5::int, 0), $6, $7, $8)`,
			chunk.ID, documentID, chunk.HeadingPath, chunk.PageStart, chunk.PageEnd, chunk.BlockIDs, chunk.Text,
			pgvector.NewVector(chunk.Embedding),
		)
	}
	// Close runs each query that is left, and gives the first error.
	if err := db.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("save %d chunks of the document %s: %w", len(chunks), documentID, err)
	}
	return nil
}

// EmbeddingDimensions gives the number of dimensions of the column
// chunks.embedding: each vector in it has that number of numbers.
func EmbeddingDimensions(ctx context.Context, db DB) (int, error) {
	var dimensions int
	err := db.QueryRow(ctx,
		`select atttypmod from pg_attribute where attrelid = 'chunks'::regclass and attname = 'embedding'`,
	).Scan(&dimensions)
	if err != nil {
		return 0, fmt.Errorf("read the dimensions of the column chunks.embedding: %w", err)
	}
	return dimensions, nil
}
