package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/chunk"
	"github.com/Njunge11/rejea/internal/documents"
	"github.com/Njunge11/rejea/internal/embed"
	"github.com/Njunge11/rejea/internal/parse"
	"github.com/Njunge11/rejea/internal/queue"
)

// indexWorker does the jobs of the index queue: the work that turns the
// blocks of a document into chunks that the search can find.
type indexWorker struct {
	river.WorkerDefaults[queue.IndexArgs]

	docs    *documents.Service
	chunker *chunk.Chunker
	// embedder is nil when the service has no embedding provider.
	embedder embed.Embedder
	logger   *slog.Logger
}

// Work cuts the blocks of a document into chunks, gets the vector of each
// chunk from the embedding model, and records the chunks.
//
// An error makes River run the job again after a wait. The chunks are
// recorded together at the end, so a new attempt does all of the work
// again and leaves no part of an old attempt.
func (w *indexWorker) Work(ctx context.Context, job *river.Job[queue.IndexArgs]) error {
	start := time.Now()
	id := job.Args.DocumentID
	doc, err := w.docs.Get(ctx, id)
	if errors.Is(err, documents.ErrNotFound) {
		// A new attempt cannot find a document that does not exist.
		return river.JobCancel(fmt.Errorf("the document %s does not exist", id))
	}
	if err != nil {
		return err
	}
	if doc.Status != documents.StatusIndexing {
		w.logger.DebugContext(ctx, "index job skipped: the document is not in the index work", "document", id, "status", doc.Status)
		return nil
	}
	if w.embedder == nil {
		// A new attempt succeeds after a person gives the credentials and
		// starts the service again.
		return fmt.Errorf("%w: set REJEA_EMBED_API_KEY and REJEA_EMBED_API_URL in .env and start the service again", embed.ErrNotSet)
	}

	blocks, err := w.docs.Blocks(ctx, id)
	if err != nil {
		return err
	}
	input := make([]chunk.Block, len(blocks))
	for i, block := range blocks {
		input[i] = chunk.Block{ID: block.ID, Page: block.Page, Type: parse.Type(block.Type), Level: block.Level, Text: block.Text}
	}
	chunks, err := w.chunker.Split(input)
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		// A new attempt reads the same blocks and finds no text again.
		return river.JobCancel(errors.New("the document has no text: the parser found no words in it"))
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text()
	}
	vectors, err := w.embedder.Documents(ctx, texts)
	if errors.Is(err, embed.ErrInput) || errors.Is(err, embed.ErrTooLarge) {
		// The provider refuses a text. A new attempt sends the same text.
		return river.JobCancel(err)
	}
	if err != nil {
		return err
	}

	rows := make([]documents.Chunk, len(chunks))
	for i, c := range chunks {
		rows[i] = documents.Chunk{
			HeadingPath: c.Headings,
			PageStart:   c.PageStart,
			PageEnd:     c.PageEnd,
			BlockIDs:    c.Blocks,
			Text:        texts[i],
			Embedding:   vectors[i],
		}
	}
	if err := w.docs.SaveChunks(ctx, id, rows); err != nil {
		return err
	}
	w.logger.InfoContext(ctx, "document indexed",
		"document", id,
		"blocks", len(blocks),
		"chunks", len(chunks),
		"duration", time.Since(start),
	)
	return nil
}
