package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The statuses of a document.
const (
	// StatusUploading is the status from the request for the upload URL
	// until the client confirms the upload.
	StatusUploading = "uploading"
	// StatusDuplicate is the status of a file that has the same content as
	// a file of a different document. Its work stops there.
	StatusDuplicate = "duplicate"
	// StatusFailed is the status of a document whose work stopped with a
	// fault. The error column has the reason.
	StatusFailed = "failed"
)

// uniqueViolation is the PostgreSQL error code for a value that a unique
// constraint refuses.
const uniqueViolation = "23505"

// NewDocument is a file that a client is about to upload.
type NewDocument struct {
	ID         uuid.UUID
	UploadedBy uuid.UUID
	Filename   string
	StorageKey string
	Metadata   map[string]any
}

// InsertUploadingDocuments adds one row with the status uploading for each
// document. It adds all rows or none.
func InsertUploadingDocuments(ctx context.Context, pool *pgxpool.Pool, docs []NewDocument) error {
	batch := &pgx.Batch{}
	for _, doc := range docs {
		batch.Queue(
			`insert into documents (id, uploaded_by, filename, storage_key, metadata, status)
			 values ($1, $2, $3, $4, $5, $6)`,
			doc.ID, doc.UploadedBy, doc.Filename, doc.StorageKey, doc.Metadata, StatusUploading,
		)
	}
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return tx.SendBatch(ctx, batch).Close()
	})
	if err != nil {
		return fmt.Errorf("insert %d documents: %w", len(docs), err)
	}
	return nil
}

// FileState is what the file worker must know about a document before it
// starts.
type FileState struct {
	// StorageKey is the key of the file in the file storage.
	StorageKey string
	// Hashed is true when the document has its file hash.
	Hashed bool
	Status string
}

// GetFileState reads the FileState of a document. When the document does
// not exist, the error is pgx.ErrNoRows.
func GetFileState(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (FileState, error) {
	var state FileState
	err := pool.QueryRow(ctx,
		`select storage_key, file_hash is not null, status from documents where id = $1`, id,
	).Scan(&state.StorageKey, &state.Hashed, &state.Status)
	if err != nil {
		return FileState{}, fmt.Errorf("read the document %s: %w", id, err)
	}
	return state, nil
}

// FailDocument gives a document the status failed and the reason. It
// reports false when the document does not exist.
func FailDocument(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, reason string) (bool, error) {
	tag, err := pool.Exec(ctx, `update documents set status = $2, error = $3 where id = $1`, id, StatusFailed, reason)
	if err != nil {
		return false, fmt.Errorf("mark the document %s as failed: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetFileHash saves the hash of the file of a document. The hash is unique
// in the table. Thus, when a different document has the same hash, the
// database refuses it: the document then gets the status duplicate and no
// hash, and SetFileHash reports true.
func SetFileHash(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, hash string) (duplicate bool, err error) {
	_, err = pool.Exec(ctx, `update documents set file_hash = $2 where id = $1`, id, hash)
	if err == nil {
		return false, nil
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != uniqueViolation {
		return false, fmt.Errorf("save the file hash of the document %s: %w", id, err)
	}
	_, err = pool.Exec(ctx, `update documents set status = $2 where id = $1`, id, StatusDuplicate)
	if err != nil {
		return false, fmt.Errorf("mark the document %s as a duplicate: %w", id, err)
	}
	return true, nil
}
