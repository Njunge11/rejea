package database

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StatusUploading is the status of a document from the request for its
// upload URL until the client confirms the upload.
const StatusUploading = "uploading"

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
