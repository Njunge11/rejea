package database

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Block is one row of the blocks table: one piece of a page, or of a Word
// file.
type Block struct {
	ID uuid.UUID
	// Page is the number of the page of the block, and 0 for a document
	// with no pages. ListBlocks gives it. ReplaceBlocks gets the page as
	// an argument.
	Page int
	// Seq is the place of the block in the reading order: of its page, or
	// of its Word file. The first block has the number 1.
	Seq  int
	Type string
	// Level is the level of a heading. 0 is saved as no level.
	Level int
	// Box is left, top, right, bottom, each as a fraction of the page from
	// 0 to 1. A block with no box has nil.
	Box  []float32
	Text string
}

// ListPagesToParse gives the pages of a document that the parser did not
// read, in the order of their numbers.
func ListPagesToParse(ctx context.Context, db DB, documentID uuid.UUID) ([]Page, error) {
	rows, err := db.Query(ctx,
		`select page_number, image_key, width, height from pages
		 where document_id = $1 and parsed_at is null
		 order by page_number`,
		documentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list the pages to parse of the document %s: %w", documentID, err)
	}
	pages, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Page, error) {
		var page Page
		err := row.Scan(&page.Number, &page.ImageKey, &page.Width, &page.Height)
		return page, err
	})
	if err != nil {
		return nil, fmt.Errorf("list the pages to parse of the document %s: %w", documentID, err)
	}
	return pages, nil
}

// ReplaceBlocks removes the blocks of one page of a document and adds the
// given blocks. A nil page is for a document with no pages, a Word file:
// the blocks are all those of the document.
func ReplaceBlocks(ctx context.Context, db DB, documentID uuid.UUID, page *int, blocks []Block) error {
	batch := &pgx.Batch{}
	batch.Queue(`delete from blocks where document_id = $1 and page_number is not distinct from $2`, documentID, page)
	for _, block := range blocks {
		batch.Queue(
			`insert into blocks (id, document_id, page_number, seq, type, level, bbox, text)
			 values ($1, $2, $3, $4, $5, nullif($6, 0), $7, $8)`,
			block.ID, documentID, page, block.Seq, block.Type, block.Level, block.Box, block.Text,
		)
	}
	// Close runs each query that is left, and gives the first error.
	if err := db.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("save %d blocks of the document %s: %w", len(blocks), documentID, err)
	}
	return nil
}

// SetPageParsed saves the time at which the parser read one page of a
// document.
func SetPageParsed(ctx context.Context, db DB, documentID uuid.UUID, page int, at time.Time) error {
	_, err := db.Exec(ctx,
		`update pages set parsed_at = $3 where document_id = $1 and page_number = $2`,
		documentID, page, at,
	)
	if err != nil {
		return fmt.Errorf("save the parse of page %d of the document %s: %w", page, documentID, err)
	}
	return nil
}

// SetParser saves the name of the parser that read a document.
func SetParser(ctx context.Context, db DB, id uuid.UUID, parser string) error {
	_, err := db.Exec(ctx, `update documents set parser = $2 where id = $1`, id, parser)
	if err != nil {
		return fmt.Errorf("save the parser of the document %s: %w", id, err)
	}
	return nil
}
