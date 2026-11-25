package documents

import (
	"context"
	"fmt"
	"strconv"
	"time"

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

// pageImageFolder gives the folder of the page images of a document. It has
// the page images only, so a URL that reads the folder reads no other file
// of the document.
func pageImageFolder(id uuid.UUID) string {
	return "documents/" + id.String() + "/pages/"
}

// PageImageKey gives the storage key of the image of a page. A new attempt
// at the render makes the same keys, so it writes over the images of the
// attempt before it.
func PageImageKey(id uuid.UUID, number int) string {
	return pageImageFolder(id) + strconv.Itoa(number) + ".png"
}

// PagesRequest tells which pages of a document a caller wants.
type PagesRequest struct {
	// DocumentID is the document.
	DocumentID uuid.UUID
	// After is the number of the last page of the list before this one.
	// With 0, the list starts at the first page.
	After int
	// Limit is the largest number of pages in the list. With 0, the list
	// has the default number.
	Limit int
}

// ViewPage is one page of a document for a viewer.
type ViewPage struct {
	// Number is the number of the page. The first page has the number 1.
	Number int
	// Width and Height are the size of the image in pixels.
	Width, Height int
	// ImageURL reads the image with no credentials, until the list ends.
	ImageURL string
}

// PageList is one part of the pages of a document.
type PageList struct {
	// Pages are in the order of their numbers.
	Pages []ViewPage
	// HasMore tells that pages come after this list. The caller gets them
	// with the number of the last page as After.
	HasMore bool
	// ExpiresAt is the time at which the image URLs of the list stop. The
	// caller asks for the list again to get new URLs.
	ExpiresAt time.Time
}

// Pages gives the pages of a document that have an image, each with a URL
// that reads the image. A document with no page images (a Word file, or a
// PDF before its parse) has an empty list. The error is ErrNotFound when no
// document has the ID, and an InvalidArgument when the request has a value
// that the rules refuse.
func (s *Service) Pages(ctx context.Context, request PagesRequest) (PageList, error) {
	limit := request.Limit
	if limit == 0 {
		limit = s.cfg.PagesLimit
	}
	if limit < 0 || limit > s.cfg.MaxPagesLimit {
		return PageList{}, &InvalidArgument{Detail: fmt.Sprintf("The limit must be a number from 1 to %d.", s.cfg.MaxPagesLimit)}
	}
	if request.After < 0 {
		return PageList{}, &InvalidArgument{Detail: "The starting_after must be the number of a page."}
	}
	if _, err := database.GetDocument(ctx, s.pool, request.DocumentID); err != nil {
		return PageList{}, err
	}
	// One page more than the list tells if a next list exists.
	rows, err := database.ListPages(ctx, s.pool, request.DocumentID, request.After, limit+1)
	if err != nil {
		return PageList{}, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	keys := make([]string, len(rows))
	for i, row := range rows {
		keys[i] = row.ImageKey
	}
	urls, expires, err := s.store.ViewURLs(ctx, pageImageFolder(request.DocumentID), keys)
	if err != nil {
		return PageList{}, err
	}
	list := PageList{Pages: make([]ViewPage, len(rows)), HasMore: hasMore, ExpiresAt: expires}
	for i, row := range rows {
		list.Pages[i] = ViewPage{Number: row.Number, Width: row.Width, Height: row.Height, ImageURL: urls[i]}
	}
	return list, nil
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
