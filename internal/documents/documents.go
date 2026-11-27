// Package documents has the rules of the service for documents: what a
// client can upload, when an upload is complete, when a document is a
// duplicate, and when it fails.
//
// Each way into the service calls this package: the HTTP server and the
// workers. A caller reads its input and shows the result, and makes no
// decision. This package has no HTTP and no SQL: the package database
// reads and writes the rows, and the package storage keeps the files.
package documents

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/storage"
)

// The statuses of a document.
const (
	// StatusUploading is the status from the request for the upload URL
	// until the client confirms the upload.
	StatusUploading = "uploading"
	// StatusUploaded is the status after the client confirmed the upload.
	// The work on the file starts from here.
	StatusUploaded = "uploaded"
	// StatusUnpacked is the status of an archive after each file in it
	// became a document of its own. The work on the archive stops there.
	StatusUnpacked = "unpacked"
	// StatusParsing is the status of a PDF, a Word file or a Markdown file
	// from the end of the work on its file until its text is in the
	// database.
	StatusParsing = "parsing"
	// StatusIndexing is the status from the end of the parse until the
	// chunks of the document and their embeddings are in the database.
	StatusIndexing = "indexing"
	// StatusIndexed is the status of a document that the search can find.
	// The work on the document is complete.
	StatusIndexed = "indexed"
	// StatusDuplicate is the status of a file that has the same content as
	// a file of a different document. Its work stops there.
	StatusDuplicate = "duplicate"
	// StatusFailed is the status of a document whose work stopped with a
	// fault. The error column has the reason.
	StatusFailed = "failed"
)

// statuses is each status that a document can have now.
var statuses = []string{
	StatusUploading, StatusUploaded, StatusUnpacked, StatusParsing, StatusIndexing, StatusIndexed,
	StatusDuplicate, StatusFailed,
}

// Config is the limits of the rules.
type Config struct {
	// MaxUploadFiles is the largest number of files in one request for
	// upload URLs.
	MaxUploadFiles int
	// MaxFileBytes is the largest size of one uploaded file.
	MaxFileBytes int64
	// UploadMaxAge is the longest time that a document can wait for the
	// confirm of its upload. After it, the document fails.
	UploadMaxAge time.Duration
	// ListLimit is the number of documents in one page of the list when
	// the caller gives no number.
	ListLimit int
	// MaxListLimit is the largest number of documents that a caller can
	// ask for in one page of the list.
	MaxListLimit int
	// PagesLimit is the number of pages in one list of the pages of a
	// document when the caller gives no number.
	PagesLimit int
	// MaxPagesLimit is the largest number of pages that a caller can ask
	// for in one list.
	MaxPagesLimit int
	// ReindexBatchSize is the number of documents that go to the index
	// work again in one transaction.
	ReindexBatchSize int
	// SearchLimit is the number of results of a search when the caller
	// gives no number.
	SearchLimit int
	// MaxSearchLimit is the largest number of results that a caller can
	// ask for.
	MaxSearchLimit int
	// SearchCandidates is the number of chunks that the keyword search and
	// the vector search each give to the merge.
	SearchCandidates int
	// SearchRankConstant is the constant k of reciprocal rank fusion: the
	// score of a chunk is the sum of 1 / (k + rank).
	SearchRankConstant int
	// MaxQueryLength is the largest number of characters of a question.
	MaxQueryLength int
	// MaxSearchDocuments is the largest number of document IDs in the
	// filter of a search.
	MaxSearchDocuments int
	// SearchTimeout is the longest time of one search: the vector of the
	// question, the query of the database and the scores of the reranker.
	SearchTimeout time.Duration
	// Rerank tells if a search uses the reranker when the caller does not
	// say.
	Rerank bool
	// RerankCandidates is the number of chunks of the merge that the
	// reranker reads.
	RerankCandidates int
}

// Document is a document as the database keeps it.
type Document = database.Document

// ErrNotFound tells that no document has the ID.
var ErrNotFound = database.ErrNotFound

// Invalid is a request that breaks a rule for its content. The client can
// correct the request and send it again.
type Invalid struct {
	Faults []Fault
}

func (e *Invalid) Error() string { return "the request is not valid" }

// Fault is one fault in a request for upload URLs.
type Fault struct {
	// File is the position of the file in the request. It is -1 when the
	// fault is in the list of files as a whole.
	File int
	// Field is the part of the file that has the fault: "filename", "size"
	// or "metadata".
	Field string
	// Detail tells the client how to correct the fault.
	Detail string
}

// InvalidArgument is a request with a value that the rules refuse. The
// detail tells the client which value, and what the rules permit.
type InvalidArgument struct {
	Detail string
}

func (e *InvalidArgument) Error() string { return e.Detail }

// Conflict is a request that the state of the document refuses. The
// detail tells the client what to do.
type Conflict struct {
	Detail string
}

func (e *Conflict) Error() string { return e.Detail }

// Service does the work of the rules. It is safe for use by many
// goroutines.
type Service struct {
	pool  *pgxpool.Pool
	store *storage.Store
	// jobs adds the jobs of the workers. It runs no job.
	jobs *river.Client[pgx.Tx]
	// embedder gives the vector of a search question. It is nil when the
	// service has no embedding model: the search then tells why.
	embedder QueryEmbedder
	// reranker gives the scores that put the results of a search in a
	// better order. It is nil when the service has no reranker model: a
	// search that asks for it then tells why.
	reranker Reranker
	cfg      Config
}

// New makes a Service. The store, the embedder and the reranker can be nil
// for a caller that reads no file and runs no search.
func New(pool *pgxpool.Pool, store *storage.Store, jobs *river.Client[pgx.Tx], embedder QueryEmbedder, reranker Reranker, cfg Config) *Service {
	return &Service{pool: pool, store: store, jobs: jobs, embedder: embedder, reranker: reranker, cfg: cfg}
}

// Get reads one document. The error is ErrNotFound when no document has
// the ID.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Document, error) {
	return database.GetDocument(ctx, s.pool, id)
}

// ByHash reads the documents that have one of the file hashes. A document
// is the value of its hash. A hash with no document is not in the result.
func (s *Service) ByHash(ctx context.Context, hashes []string) (map[string]Document, error) {
	docs, err := database.DocumentsByHash(ctx, s.pool, hashes)
	if err != nil {
		return nil, err
	}
	found := make(map[string]Document, len(docs))
	for _, doc := range docs {
		found[doc.FileHash] = doc
	}
	return found, nil
}

// ChunkCount gives the number of chunks of the documents.
func (s *Service) ChunkCount(ctx context.Context, ids []uuid.UUID) (int, error) {
	return database.CountChunks(ctx, s.pool, ids)
}

// Fail stops the work on a document: it gets the status failed and the
// reason. Fail reports false when the document does not exist.
//
// A failed document gives up its file hash. If it kept the hash, the same
// file in a new upload became a duplicate of a document that failed, and
// the user had no way to get the file into the service.
func (s *Service) Fail(ctx context.Context, id uuid.UUID, reason string) (bool, error) {
	var found bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		found, err = database.SetStatusAndError(ctx, tx, id, StatusFailed, reason)
		if err != nil || !found {
			return err
		}
		return database.ClearFileHash(ctx, tx, id)
	})
	return found, err
}

// originalKey gives the storage key of the file that a client uploads for
// a document. The key has the ID, not the name of the file, so each
// character in a file name is safe.
func originalKey(id uuid.UUID, filename string) string {
	return "documents/" + id.String() + "/original" + Ending(filename)
}

// InnerKey gives the storage key of file number n from an archive. A new
// attempt at the unpack makes the same keys, so it writes over the files
// of the attempt before it and leaves no file with no document.
func InnerKey(archive uuid.UUID, n int, filename string) string {
	return "documents/" + archive.String() + "/files/" + strconv.Itoa(n) + Ending(filename)
}

// newID makes the ID of a document or of a block. The IDs are in the order
// of their time, which keeps new rows together in the index of the table.
func newID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("make an ID: %w", err)
	}
	return id, nil
}
