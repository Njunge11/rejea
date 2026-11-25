package database

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Page is one row of the pages table: the image of one page of a document.
type Page struct {
	// Number is the number of the page in the document. The first page
	// has the number 1.
	Number int
	// ImageKey is the storage key of the image.
	ImageKey string
	// Width and Height are the size of the image in pixels.
	Width, Height int
}

// SavePage writes the row of one page of a document. A row that the page
// has already gets the new values.
func SavePage(ctx context.Context, db DB, documentID uuid.UUID, page Page) error {
	_, err := db.Exec(ctx,
		`insert into pages (document_id, page_number, image_key, width, height)
		 values ($1, $2, $3, $4, $5)
		 on conflict (document_id, page_number) do update
		 set image_key = excluded.image_key, width = excluded.width, height = excluded.height`,
		documentID, page.Number, page.ImageKey, page.Width, page.Height,
	)
	if err != nil {
		return fmt.Errorf("save page %d of the document %s: %w", page.Number, documentID, err)
	}
	return nil
}

// ListPageNumbers gives the numbers of the pages of a document that have a
// row.
func ListPageNumbers(ctx context.Context, db DB, documentID uuid.UUID) ([]int, error) {
	rows, err := db.Query(ctx, `select page_number from pages where document_id = $1`, documentID)
	if err != nil {
		return nil, fmt.Errorf("list the pages of the document %s: %w", documentID, err)
	}
	numbers, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return nil, fmt.Errorf("list the pages of the document %s: %w", documentID, err)
	}
	return numbers, nil
}

// ListPages gives the pages of a document in the order of their numbers,
// from the page after the given number. It gives limit pages at most.
func ListPages(ctx context.Context, db DB, documentID uuid.UUID, after, limit int) ([]Page, error) {
	rows, err := db.Query(ctx,
		`select page_number, image_key, width, height from pages
		 where document_id = $1 and page_number > $2
		 order by page_number
		 limit $3`,
		documentID, after, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list the pages of the document %s: %w", documentID, err)
	}
	pages, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Page, error) {
		var page Page
		err := row.Scan(&page.Number, &page.ImageKey, &page.Width, &page.Height)
		return page, err
	})
	if err != nil {
		return nil, fmt.Errorf("list the pages of the document %s: %w", documentID, err)
	}
	return pages, nil
}

// SetPageCount saves the number of pages of a document.
func SetPageCount(ctx context.Context, db DB, id uuid.UUID, count int) error {
	_, err := db.Exec(ctx, `update documents set page_count = $2 where id = $1`, id, count)
	if err != nil {
		return fmt.Errorf("save the page count of the document %s: %w", id, err)
	}
	return nil
}
