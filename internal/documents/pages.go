package documents

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/queue"
)

// StartParse ends the work on the file of a document and starts the work
// on its content: the document moves from the status uploaded to the
// status parsing and gets its parse job, both in one transaction. A
// document is never parsing with no job.
//
// It reports false, and changes nothing, when the document does not have
// the status uploaded.
func (s *Service) StartParse(ctx context.Context, id uuid.UUID) (bool, error) {
	var started bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		started, err = database.ChangeStatus(ctx, tx, id, StatusUploaded, StatusParsing)
		if err != nil || !started {
			return err
		}
		if _, err := s.jobs.InsertTx(ctx, tx, queue.ParseArgs{DocumentID: id}, nil); err != nil {
			return fmt.Errorf("add the parse job of the document %s: %w", id, err)
		}
		return nil
	})
	return started, err
}

// PageImage is the image of one page of a document.
type PageImage = database.Page

// PageImageKey gives the storage key of the image of a page. A new attempt
// at the render makes the same keys, so it writes over the images of the
// attempt before it.
func PageImageKey(id uuid.UUID, number int) string {
	return "documents/" + id.String() + "/pages/" + strconv.Itoa(number) + ".png"
}

// SavePageImage records the image of one page of a document. The image is
// in the file storage before the call.
func (s *Service) SavePageImage(ctx context.Context, id uuid.UUID, page PageImage) error {
	return database.SavePage(ctx, s.pool, id, page)
}

// PagesWithImage gives the numbers of the pages of a document that have an
// image. A new attempt at the render does only the other pages.
func (s *Service) PagesWithImage(ctx context.Context, id uuid.UUID) ([]int, error) {
	return database.ListPageNumbers(ctx, s.pool, id)
}

// SetPageCount records the number of pages of a document. A document that
// has a page count has the image of each page: SetPageCount refuses a
// count when a page has no image.
func (s *Service) SetPageCount(ctx context.Context, id uuid.UUID, count int) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		numbers, err := database.ListPageNumbers(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(numbers) != count {
			return fmt.Errorf("the document %s has %d pages and %d page images", id, count, len(numbers))
		}
		return database.SetPageCount(ctx, tx, id, count)
	})
}
