package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"time"

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
}

// Work computes the hash of the file and saves it. A file with the same
// content as the file of a different document becomes a duplicate.
//
// An error makes River run the job again after a wait.
func (w *fileWorker) Work(ctx context.Context, job *river.Job[queue.FileArgs]) error {
	start := time.Now()
	id := job.Args.DocumentID
	key, err := database.StorageKey(ctx, w.pool, id)
	if err != nil {
		return err
	}
	hash, size, err := w.hash(ctx, key)
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
	return nil
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
		return "", 0, fmt.Errorf("read %q: %w", key, err)
	}
	return hex.EncodeToString(sum.Sum(nil)), size, nil
}
