package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/queue"
	"github.com/Njunge11/rejea/internal/storage"
)

// fileWorker does the jobs of the file queue: the first work on an
// uploaded file.
type fileWorker struct {
	river.WorkerDefaults[queue.FileArgs]

	pool   *pgxpool.Pool
	store  *storage.Store
	logger *slog.Logger
	cfg    Config
}

// Work does two stages. The first stage computes the hash of the file and
// saves it: a file with the same content as the file of a different
// document becomes a duplicate. The second stage is for an archive only:
// each PDF and Word file in it becomes a document of its own, with its own
// file job.
//
// An error makes River run the job again after a wait. A new attempt skips
// each stage that is complete.
func (w *fileWorker) Work(ctx context.Context, job *river.Job[queue.FileArgs]) error {
	start := time.Now()
	id := job.Args.DocumentID
	state, err := database.GetFileState(ctx, w.pool, id)
	if errors.Is(err, pgx.ErrNoRows) {
		// A new attempt cannot find a document that does not exist.
		return river.JobCancel(fmt.Errorf("the document %s does not exist", id))
	}
	if err != nil {
		return err
	}
	if state.Status == database.StatusDuplicate || state.Status == database.StatusUnpacked {
		w.logger.DebugContext(ctx, "file job skipped: each stage is complete", "document", id)
		return nil
	}
	if state.Hashed {
		w.logger.DebugContext(ctx, "file hash stage skipped: it is complete", "document", id)
	} else {
		hash, size, err := w.hash(ctx, state.StorageKey)
		if err != nil {
			return err
		}
		duplicate, err := database.SetFileHash(ctx, w.pool, id, hash)
		if err != nil {
			return err
		}
		w.logger.InfoContext(ctx, "file hashed",
			"document", id,
			"bytes", size,
			"duplicate", duplicate,
			"duration", time.Since(start),
		)
		if duplicate {
			return nil
		}
	}
	if endingFrom(state.StorageKey, archiveEndings) == "" {
		return nil
	}

	start = time.Now()
	files, added, err := w.unpackAndSave(ctx, id, state.StorageKey)
	if err != nil {
		// With a fault in the content of the archive, a new attempt reads
		// the same archive and finds the same fault.
		final := isContentFault(err)
		if final || job.Attempt >= job.MaxAttempts {
			// No new attempt writes over the files that this attempt sent
			// to the file storage, and no document points to them.
			w.discard(ctx, id, files)
		}
		if final {
			return river.JobCancel(err)
		}
		return err
	}
	w.logger.InfoContext(ctx, "archive unpacked",
		"document", id,
		"files", len(files),
		"duplicates", len(files)-len(added),
		"duration", time.Since(start),
	)
	return nil
}

// unpackAndSave takes the files out of an archive and saves them as
// documents. It gives the files that are in the file storage, also with an
// error, and the IDs of the documents that are not duplicates.
func (w *fileWorker) unpackAndSave(ctx context.Context, id uuid.UUID, key string) ([]database.UnpackedFile, []uuid.UUID, error) {
	files, err := w.unpack(ctx, id, key)
	if err != nil {
		return files, nil, err
	}
	// The documents, their jobs and the status of the archive change
	// together or not at all.
	var added []uuid.UUID
	err = pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		var err error
		added, err = database.SaveUnpacked(ctx, tx, id, files)
		if err != nil || len(added) == 0 {
			return err
		}
		jobs := make([]river.InsertManyParams, len(added))
		for i, document := range added {
			jobs[i] = river.InsertManyParams{Args: queue.FileArgs{DocumentID: document}}
		}
		if _, err := river.ClientFromContext[pgx.Tx](ctx).InsertManyTx(ctx, tx, jobs); err != nil {
			return fmt.Errorf("add the file jobs of the archive %s: %w", id, err)
		}
		return nil
	})
	return files, added, err
}

// discard removes the files of an archive from the file storage after the
// unpack failed for good.
func (w *fileWorker) discard(ctx context.Context, id uuid.UUID, files []database.UnpackedFile) {
	if len(files) == 0 {
		return
	}
	keys := make([]string, len(files))
	for i, file := range files {
		keys[i] = file.StorageKey
	}
	if err := w.store.Delete(ctx, keys); err != nil {
		w.logger.WarnContext(ctx, "the files of a failed archive stay in the file storage",
			"document", id, "files", len(keys), "err", err)
	}
}

// hash downloads a file and computes its SHA-256 hash in one pass. The file
// goes through the hash as a stream, so the memory in use does not grow
// with the size of the file.
func (w *fileWorker) hash(ctx context.Context, key string) (hash string, size int64, err error) {
	file, err := w.store.Download(ctx, key)
	if err != nil {
		return "", 0, err
	}
	defer file.Close() //nolint:errcheck // The read of the file found each error.

	sum := sha256.New()
	// The read to the end of the file is also the integrity check: the AWS
	// SDK compares the bytes with the checksum that S3 keeps for the file.
	size, err = io.Copy(sum, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(sum.Sum(nil)), size, nil
}
