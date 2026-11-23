package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The functions in this file read and write the documents table. They make
// no decision: each value that they write comes from the caller, and the
// rules of the service are in the package documents.

// DB is a connection pool or a transaction. Each function takes one, so
// the caller decides if a piece of work is part of a transaction.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, batch *pgx.Batch) pgx.BatchResults
}

// ErrNotFound tells that no document has the ID.
var ErrNotFound = errors.New("the document does not exist")

// uniqueViolation is the PostgreSQL error code for a value that a unique
// constraint refuses.
const uniqueViolation = "23505"

// Document is one row of the documents table.
type Document struct {
	ID         uuid.UUID
	UploadedBy uuid.UUID
	Filename   string
	StorageKey string
	Metadata   map[string]any
	Status     string
	// FileHash is "" when the document has no file hash.
	FileHash string
	// ParentID is the archive that held the file. It is uuid.Nil for a
	// file that a client uploaded.
	ParentID uuid.UUID
	// Error is the reason that the work on the document stopped. It is ""
	// when the document has no error.
	Error string
	// CreatedAt is set by the database when the row is added.
	CreatedAt time.Time
}

// documentColumns is the columns of a Document, in the order that
// scanDocument reads them.
const documentColumns = `id, uploaded_by, filename, storage_key, metadata, status,
	coalesce(file_hash, ''), parent_id, coalesce(error, ''), created_at`

// scanDocument reads one row that has the columns of documentColumns.
func scanDocument(row pgx.Row) (Document, error) {
	var doc Document
	var parent *uuid.UUID
	err := row.Scan(&doc.ID, &doc.UploadedBy, &doc.Filename, &doc.StorageKey, &doc.Metadata, &doc.Status,
		&doc.FileHash, &parent, &doc.Error, &doc.CreatedAt)
	if err != nil {
		return Document{}, err
	}
	if parent != nil {
		doc.ParentID = *parent
	}
	return doc, nil
}

// GetDocument reads one document. The error is ErrNotFound when no
// document has the ID.
func GetDocument(ctx context.Context, db DB, id uuid.UUID) (Document, error) {
	doc, err := scanDocument(db.QueryRow(ctx, `select `+documentColumns+` from documents where id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, fmt.Errorf("read the document %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Document{}, fmt.Errorf("read the document %s: %w", id, err)
	}
	return doc, nil
}

// ListDocuments reads the documents from the newest to the oldest: the
// order of their IDs, which have the time of their making at the start.
// It gives limit documents at most.
//
// A status that is not "" keeps only the documents with that status. An
// after that is not uuid.Nil keeps only the documents that come after
// that ID in the order: the older ones.
//
// The position is an ID and not a number of rows to skip, because
// PostgreSQL must compute each row that it skips.
func ListDocuments(ctx context.Context, db DB, status string, after uuid.UUID, limit int) ([]Document, error) {
	query := `select ` + documentColumns + ` from documents`
	var conditions []string
	var args []any
	if status != "" {
		args = append(args, status)
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	if after != uuid.Nil {
		args = append(args, after)
		conditions = append(conditions, fmt.Sprintf("id < $%d", len(args)))
	}
	if len(conditions) > 0 {
		query += ` where ` + strings.Join(conditions, ` and `)
	}
	args = append(args, limit)
	query += fmt.Sprintf(` order by id desc limit $%d`, len(args))

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list the documents: %w", err)
	}
	docs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Document, error) { return scanDocument(row) })
	if err != nil {
		return nil, fmt.Errorf("list the documents: %w", err)
	}
	return docs, nil
}

// InsertDocuments adds one row for each document. The file hash is unique
// in the table, so the database adds no row for a document whose hash a
// different document has. InsertDocuments tells, for each document, if its
// row was added.
func InsertDocuments(ctx context.Context, db DB, docs []Document) ([]bool, error) {
	batch := &pgx.Batch{}
	for _, doc := range docs {
		var parent *uuid.UUID
		if doc.ParentID != uuid.Nil {
			parent = &doc.ParentID
		}
		batch.Queue(
			`insert into documents (id, uploaded_by, filename, storage_key, metadata, status, file_hash, parent_id)
			 values ($1, $2, $3, $4, $5, $6, nullif($7, ''), $8)
			 on conflict (file_hash) do nothing`,
			doc.ID, doc.UploadedBy, doc.Filename, doc.StorageKey, doc.Metadata, doc.Status, doc.FileHash, parent,
		)
	}
	results := db.SendBatch(ctx, batch)
	added := make([]bool, len(docs))
	for i := range docs {
		tag, err := results.Exec()
		if err != nil {
			results.Close() //nolint:errcheck,gosec // The first error is the cause.
			return nil, fmt.Errorf("insert %d documents: %w", len(docs), err)
		}
		added[i] = tag.RowsAffected() == 1
	}
	if err := results.Close(); err != nil {
		return nil, fmt.Errorf("insert %d documents: %w", len(docs), err)
	}
	return added, nil
}

// SetFileHash saves the hash of the file of a document. It reports false,
// and saves nothing, when a different document has the same hash.
//
// PostgreSQL stops a transaction at a refused value. Thus a caller that
// must continue after a false result calls SetFileHash with the pool.
func SetFileHash(ctx context.Context, db DB, id uuid.UUID, hash string) (bool, error) {
	_, err := db.Exec(ctx, `update documents set file_hash = $2 where id = $1`, id, hash)
	if err == nil {
		return true, nil
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == uniqueViolation {
		return false, nil
	}
	return false, fmt.Errorf("save the file hash of the document %s: %w", id, err)
}

// ClearFileHash removes the file hash of a document.
func ClearFileHash(ctx context.Context, db DB, id uuid.UUID) error {
	_, err := db.Exec(ctx, `update documents set file_hash = null where id = $1`, id)
	if err != nil {
		return fmt.Errorf("remove the file hash of the document %s: %w", id, err)
	}
	return nil
}

// ClearError removes the text of the error of a document.
func ClearError(ctx context.Context, db DB, id uuid.UUID) error {
	_, err := db.Exec(ctx, `update documents set error = null where id = $1`, id)
	if err != nil {
		return fmt.Errorf("remove the error of the document %s: %w", id, err)
	}
	return nil
}

// SetStatus gives a document a status.
func SetStatus(ctx context.Context, db DB, id uuid.UUID, status string) error {
	_, err := db.Exec(ctx, `update documents set status = $2 where id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("give the document %s the status %s: %w", id, status, err)
	}
	return nil
}

// ChangeStatus moves a document from one status to a different status. It
// reports false, and changes nothing, when the document does not have the
// status from.
func ChangeStatus(ctx context.Context, db DB, id uuid.UUID, from, to string) (bool, error) {
	tag, err := db.Exec(ctx, `update documents set status = $3 where id = $1 and status = $2`, id, from, to)
	if err != nil {
		return false, fmt.Errorf("move the document %s from %s to %s: %w", id, from, to, err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetStatusAndError gives a document a status and the text of its error.
// It reports false when the document does not exist.
func SetStatusAndError(ctx context.Context, db DB, id uuid.UUID, status, reason string) (bool, error) {
	tag, err := db.Exec(ctx, `update documents set status = $2, error = $3 where id = $1`, id, status, reason)
	if err != nil {
		return false, fmt.Errorf("give the document %s the status %s: %w", id, status, err)
	}
	return tag.RowsAffected() == 1, nil
}

// ChangeStatusOfOld moves each document that has the status from, and
// that is older than age, to the status to with the text of an error. The
// clock is the clock of the database, which also set the time of each row.
// It gives the storage keys of the documents that it moved.
func ChangeStatusOfOld(ctx context.Context, db DB, from string, age time.Duration, to, reason string) ([]string, error) {
	rows, err := db.Query(ctx,
		`update documents set status = $3, error = $4
		 where status = $1 and created_at < now() - make_interval(secs => $2)
		 returning storage_key`,
		from, age.Seconds(), to, reason,
	)
	if err != nil {
		return nil, fmt.Errorf("move the old documents from %s to %s: %w", from, to, err)
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("move the old documents from %s to %s: %w", from, to, err)
	}
	return keys, nil
}
