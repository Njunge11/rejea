package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/documents"
	"github.com/Njunge11/rejea/internal/pdf"
	"github.com/Njunge11/rejea/internal/queue"
	"github.com/Njunge11/rejea/internal/storage"
)

// parseWorker does the jobs of the parse queue: the work that turns the
// file of a document into pages and blocks.
type parseWorker struct {
	river.WorkerDefaults[queue.ParseArgs]

	docs   *documents.Service
	store  *storage.Store
	pdfs   *pdf.Reader
	logger *slog.Logger
}

// Work does one stage: it makes an image of each page of a PDF. A Word
// file has no page image.
//
// An error makes River run the job again after a wait. A new attempt skips
// the stage when it is complete.
func (w *parseWorker) Work(ctx context.Context, job *river.Job[queue.ParseArgs]) error {
	id := job.Args.DocumentID
	doc, err := w.docs.Get(ctx, id)
	if errors.Is(err, documents.ErrNotFound) {
		// A new attempt cannot find a document that does not exist.
		return river.JobCancel(fmt.Errorf("the document %s does not exist", id))
	}
	if err != nil {
		return err
	}
	if doc.Status != documents.StatusParsing {
		w.logger.DebugContext(ctx, "parse job skipped: the document is not in the parse", "document", id, "status", doc.Status)
		return nil
	}
	if !documents.IsPDF(doc.StorageKey) {
		return nil
	}
	if doc.PageCount > 0 {
		w.logger.DebugContext(ctx, "page render stage skipped: it is complete", "document", id)
		return nil
	}
	err = w.renderPages(ctx, doc)
	if pdf.IsContentError(err) {
		// A new attempt reads the same PDF and finds the same fault.
		return river.JobCancel(err)
	}
	return err
}

// renderPages makes an image of each page of a PDF, sends each image to
// the file storage, and records it. The service uses no disk: PDFium reads
// the PDF from the file storage in pieces.
//
// A page is recorded when its image is in the file storage. Thus a new
// attempt, after a fault or a stop of the service, does only the pages
// that are not recorded. The page count is recorded last: it tells that
// the stage is complete.
func (w *parseWorker) renderPages(ctx context.Context, doc documents.Document) error {
	start := time.Now()
	file, err := w.store.OpenRanges(ctx, doc.StorageKey)
	if err != nil {
		return err
	}
	opened, err := w.pdfs.Open(ctx, file, file.Size())
	if err != nil {
		return err
	}
	defer opened.Close()

	numbers, err := w.docs.PagesWithImage(ctx, doc.ID)
	if err != nil {
		return err
	}
	done := make(map[int]bool, len(numbers))
	for _, number := range numbers {
		done[number] = true
	}
	rendered := 0
	for number := 1; number <= opened.Pages(); number++ {
		if done[number] {
			continue
		}
		image, err := opened.Render(number)
		if err != nil {
			return err
		}
		key := documents.PageImageKey(doc.ID, number)
		if err := w.store.Upload(ctx, key, "image/png", bytes.NewReader(image.PNG)); err != nil {
			return err
		}
		err = w.docs.SavePageImage(ctx, doc.ID, documents.PageImage{
			Number: number, ImageKey: key, Width: image.Width, Height: image.Height,
		})
		if err != nil {
			return err
		}
		rendered++
	}
	if err := w.docs.SetPageCount(ctx, doc.ID, opened.Pages()); err != nil {
		return err
	}
	w.logger.InfoContext(ctx, "pages rendered",
		"document", doc.ID,
		"pages", opened.Pages(),
		"rendered", rendered,
		"duration", time.Since(start),
	)
	return nil
}
