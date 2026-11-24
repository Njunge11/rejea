package database

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Block is one row of the blocks table: one piece of a page.
type Block struct {
	ID uuid.UUID
	// Seq is the place of the block in the reading order of its page. The
	// first block has the number 1.
	Seq  int
	Type string
	// Box is left, top, right, bottom, each as a fraction of the page from
	// 0 to 1.
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

// ReplacePageBlocks removes the blocks of one page of a document and adds
// the given blocks.
func ReplacePageBlocks(ctx context.Context, db DB, documentID uuid.UUID, page int, blocks []Block) error {
	batch := &pgx.Batch{}
	batch.Queue(`delete from blocks where document_id = $1 and page_number = $2`, documentID, page)
	for _, block := range blocks {
		batch.Queue(
			`insert into blocks (id, document_id, page_number, seq, type, bbox, text)
			 values ($1, $2, $3, $4, $5, $6, $7)`,
			block.ID, documentID, page, block.Seq, block.Type, block.Box, block.Text,
		)
	}
	// Close runs each query that is left, and gives the first error.
	if err := db.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("save %d blocks of page %d of the document %s: %w", len(blocks), page, documentID, err)
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
