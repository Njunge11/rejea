package documents

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/parse"
)

// PagesToParse gives the pages of a document that the parser did not read,
// in the order of their numbers. A new attempt at the parse does only
// these pages.
func (s *Service) PagesToParse(ctx context.Context, id uuid.UUID) ([]PageImage, error) {
	return database.ListPagesToParse(ctx, s.pool, id)
}

// SaveBlocks records the blocks that the parser gave for one page of a
// document, in their reading order, and records that the page is parsed.
// Both are one transaction: a page is never parsed with a part of its
// blocks. A page can have no block.
//
// A second call for the same page puts the new blocks in the place of the
// old ones.
func (s *Service) SaveBlocks(ctx context.Context, id uuid.UUID, page int, blocks []parse.Block) error {
	rows, err := blockRows(blocks, true)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := database.ReplaceBlocks(ctx, tx, id, &page, rows); err != nil {
			return err
		}
		return database.SetPageParsed(ctx, tx, id, page, time.Now())
	})
}

// SaveWordBlocks records the blocks of a Word file, in their reading
// order, and the name of the reader that gave them. Both are one
// transaction: a document that has a parser has its blocks. The blocks
// have no page and no box.
//
// A second call puts the new blocks in the place of the old ones.
func (s *Service) SaveWordBlocks(ctx context.Context, id uuid.UUID, blocks []parse.Block, reader string) error {
	rows, err := blockRows(blocks, false)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := database.ReplaceBlocks(ctx, tx, id, nil, rows); err != nil {
			return err
		}
		return database.SetParser(ctx, tx, id, reader)
	})
}

// blockRows makes the rows of the blocks of one page, or of one Word file.
// Each block gets its ID and its place in the reading order.
func blockRows(blocks []parse.Block, withBox bool) ([]database.Block, error) {
	rows := make([]database.Block, len(blocks))
	for i, block := range blocks {
		blockID, err := newID()
		if err != nil {
			return nil, err
		}
		rows[i] = database.Block{
			ID:    blockID,
			Seq:   i + 1,
			Type:  string(block.Type),
			Level: block.Level,
			Text:  block.Text,
		}
		if withBox {
			rows[i].Box = []float32{
				float32(block.Box.Left), float32(block.Box.Top),
				float32(block.Box.Right), float32(block.Box.Bottom),
			}
		}
	}
	return rows, nil
}

// SetParser records the name of the parser that read a document. A
// document that has a parser has the blocks of each page: SetParser
// refuses when the document has no page, and when the parser did not read
// a page.
func (s *Service) SetParser(ctx context.Context, id uuid.UUID, parser string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		doc, err := database.GetDocument(ctx, tx, id)
		if err != nil {
			return err
		}
		if doc.PageCount < 1 {
			return fmt.Errorf("the document %s has no page", id)
		}
		left, err := database.ListPagesToParse(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(left) > 0 {
			return fmt.Errorf("the parser did not read %d pages of the document %s", len(left), id)
		}
		return database.SetParser(ctx, tx, id, parser)
	})
}
