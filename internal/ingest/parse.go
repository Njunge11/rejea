package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/documents"
	"github.com/Njunge11/rejea/internal/parse"
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
	parser parse.Parser
	words  *parse.Word
	logger *slog.Logger
}

// Work does two stages for a PDF: it makes an image of each page, then the
// parser reads each image and gives the blocks of the page. A Word file
// has one stage: the Word reader gives its blocks from the file itself,
// with no page image.
//
// An error makes River run the job again after a wait. A new attempt skips
// a stage that is complete, and does only the pages that are left of a
// stage that is not.
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
		if doc.Parser != "" {
			return nil
		}
		err = w.readWord(ctx, doc)
		if parse.IsContentError(err) {
			// A new attempt reads the same file and finds the same fault.
			return river.JobCancel(err)
		}
		return err
	}
	if doc.PageCount == 0 {
		err = w.renderPages(ctx, doc)
		if pdf.IsContentError(err) {
			// A new attempt reads the same PDF and finds the same fault.
			return river.JobCancel(err)
		}
		if err != nil {
			return err
		}
	}
	if doc.Parser == "" {
		return w.parsePages(ctx, doc)
	}
	return nil
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

// parsePages sends the image of each page to the parser and records the
// blocks that the parser gives.
//
// The blocks of a page are recorded when the parser answers. Thus a new
// attempt, after a fault or a stop of the service, does only the pages
// that the parser did not read. The name of the parser is recorded last:
// it tells that the stage is complete.
//
// Each error is one that a new attempt can correct: the images are those
// of the render stage, so the parser has no reason to refuse one.
func (w *parseWorker) parsePages(ctx context.Context, doc documents.Document) error {
	start := time.Now()
	pages, err := w.docs.PagesToParse(ctx, doc.ID)
	if err != nil {
		return err
	}
	blockCount := 0
	for _, page := range pages {
		image, err := w.pageImage(ctx, page.ImageKey)
		if err != nil {
			return err
		}
		blocks, err := w.parser.Parse(ctx, image)
		if err != nil {
			return fmt.Errorf("parse page %d: %w", page.Number, err)
		}
		if err := w.docs.SaveBlocks(ctx, doc.ID, page.Number, blocks); err != nil {
			return err
		}
		blockCount += len(blocks)
	}
	if err := w.docs.SetParser(ctx, doc.ID, w.parser.Name()); err != nil {
		return err
	}
	w.logger.InfoContext(ctx, "pages parsed",
		"document", doc.ID,
		"parsed", len(pages),
		"blocks", blockCount,
		"parser", w.parser.Name(),
		"duration", time.Since(start),
	)
	return nil
}

// pageImage reads the image of one page from the file storage. The image
// is in memory as a whole, because the parser takes it in one request. The
// render stage made the image, and its pixel limit is the limit of the
// size.
func (w *parseWorker) pageImage(ctx context.Context, key string) ([]byte, error) {
	file, err := w.store.Download(ctx, key)
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck // The read found each error.
	image, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read the page image %s: %w", key, err)
	}
	return image, nil
}

// readWord gives a Word file to the Word reader and records its blocks.
// The name of the reader is recorded with the blocks: it tells that the
// stage is complete.
func (w *parseWorker) readWord(ctx context.Context, doc documents.Document) error {
	start := time.Now()
	reader, err := w.words.Name(ctx)
	if err != nil {
		return err
	}
	file, err := w.store.Download(ctx, doc.StorageKey)
	if err != nil {
		return err
	}
	defer file.Close() //nolint:errcheck // The read found each error.
	blocks, err := w.words.Read(ctx, file, documents.Ending(doc.StorageKey))
	if err != nil {
		return err
	}
	if err := w.docs.SaveWordBlocks(ctx, doc.ID, blocks, reader); err != nil {
		return err
	}
	w.logger.InfoContext(ctx, "word file read",
		"document", doc.ID,
		"blocks", len(blocks),
		"reader", reader,
		"duration", time.Since(start),
	)
	return nil
}
