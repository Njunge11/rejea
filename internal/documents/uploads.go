package documents

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/queue"
)

// FileRequest is one file that a client wants to upload.
type FileRequest struct {
	Filename string
	// Size is the size of the file in bytes.
	Size int64
	// Metadata is optional: data about the file that a person gave.
	Metadata map[string]any
}

// Upload is a new document and the URL for its file. The client sends the
// file to the URL with an HTTP PUT, and then confirms the upload.
type Upload struct {
	ID        uuid.UUID
	Filename  string
	Status    string
	UploadURL string
}

// RequestUploads makes one document with the status uploading and one
// upload URL for each file. It makes all the documents or none. The error
// is an Invalid when the files break a rule.
func (s *Service) RequestUploads(ctx context.Context, files []FileRequest) ([]Upload, error) {
	if faults := s.check(files); len(faults) > 0 {
		return nil, &Invalid{Faults: faults}
	}
	// The URLs are signed before the rows are written. Thus a failure
	// leaves no row that has no URL.
	docs := make([]Document, len(files))
	uploads := make([]Upload, len(files))
	for i, file := range files {
		id, err := newID()
		if err != nil {
			return nil, err
		}
		key := originalKey(id, file.Filename)
		url, err := s.store.UploadURL(ctx, key, file.Size)
		if err != nil {
			return nil, err
		}
		metadata := file.Metadata
		if metadata == nil {
			metadata = map[string]any{}
		}
		docs[i] = Document{
			ID:         id,
			UploadedBy: uuid.Nil, // The system: the service has no login yet.
			Filename:   file.Filename,
			StorageKey: key,
			Metadata:   metadata,
			Status:     StatusUploading,
		}
		uploads[i] = Upload{ID: id, Filename: file.Filename, Status: StatusUploading, UploadURL: url}
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := database.InsertDocuments(ctx, tx, docs)
		return err
	})
	if err != nil {
		return nil, err
	}
	return uploads, nil
}

// check finds each fault in a request for upload URLs.
func (s *Service) check(files []FileRequest) []Fault {
	if len(files) == 0 {
		return []Fault{{File: -1, Detail: "Give one file or more."}}
	}
	if len(files) > s.cfg.MaxUploadFiles {
		return []Fault{{
			File:   -1,
			Detail: fmt.Sprintf("One request can have %d files at most, and this one has %d.", s.cfg.MaxUploadFiles, len(files)),
		}}
	}
	var faults []Fault
	for i, file := range files {
		switch {
		case file.Filename == "":
			faults = append(faults, Fault{File: i, Field: "filename", Detail: "Give the name of the file."})
		case strings.ContainsFunc(file.Filename, unicode.IsControl):
			faults = append(faults, Fault{File: i, Field: "filename", Detail: "The name of the file has a control character."})
		case Ending(file.Filename) == "":
			faults = append(faults, Fault{File: i, Field: "filename", Detail: "The file must be a " + FileTypes + " file."})
		}
		switch {
		case file.Size < 1:
			faults = append(faults, Fault{File: i, Field: "size", Detail: "Give the size of the file in bytes, 1 or more."})
		case file.Size > s.cfg.MaxFileBytes:
			faults = append(faults, Fault{
				File: i, Field: "size",
				Detail: fmt.Sprintf("A file can have %d bytes at most, and this one has %d.", s.cfg.MaxFileBytes, file.Size),
			})
		}
		if hasNUL(file.Metadata) {
			faults = append(faults, Fault{File: i, Field: "metadata", Detail: "The metadata has a NUL character, which the database cannot keep."})
		}
	}
	return faults
}

// hasNUL tells if a decoded JSON value has a NUL character in a string or
// in a key. PostgreSQL refuses such a value in a jsonb column.
func hasNUL(value any) bool {
	switch value := value.(type) {
	case string:
		return strings.ContainsRune(value, 0)
	case []any:
		for _, element := range value {
			if hasNUL(element) {
				return true
			}
		}
	case map[string]any:
		for key, element := range value {
			if strings.ContainsRune(key, 0) || hasNUL(element) {
				return true
			}
		}
	}
	return false
}

// ConfirmUpload is what a client calls after it sent the file to the
// upload URL. The document gets the status uploaded and its file job, both
// in one transaction: a document is never uploaded with no job, and a job
// never exists for a document that is not uploaded.
//
// ConfirmUpload gives the status that the document has after the call. A
// second confirm of the same document changes nothing and gives its
// status, so a client can send the request again after a network fault.
//
// The error is ErrNotFound when no document has the ID, and a Conflict
// when the file is not in the file storage or the document failed.
func (s *Service) ConfirmUpload(ctx context.Context, id uuid.UUID) (string, error) {
	doc, err := database.GetDocument(ctx, s.pool, id)
	if err != nil {
		return "", err
	}
	if doc.Status == StatusUploading {
		inStorage, err := s.store.Exists(ctx, doc.StorageKey)
		if err != nil {
			return "", err
		}
		if !inStorage {
			return "", &Conflict{Detail: "The file is not in the storage. Send the file to its upload URL, then confirm."}
		}
		// When the status changed after the read, a different request
		// confirmed the document, or the document failed because it was
		// too old. The read below then gives that status.
		if _, err := s.startWork(ctx, id, StatusUploading); err != nil {
			return "", err
		}
		// The answer has the status that the document has now.
		doc, err = database.GetDocument(ctx, s.pool, id)
		if err != nil {
			return "", err
		}
	}
	if doc.Status == StatusFailed {
		return "", &Conflict{Detail: "The document has the status failed. Ask for a new upload URL."}
	}
	return doc.Status, nil
}

// startWork moves a document from the status from to the status uploaded
// and adds its file job, both in one transaction: a document is never
// uploaded with no job, and a job never exists for a document that is not
// uploaded. A document that was failed loses its error.
//
// It reports false, and changes nothing, when the document does not have
// the status from. Thus, of many calls at the same time, one starts the
// work.
func (s *Service) startWork(ctx context.Context, id uuid.UUID, from string) (bool, error) {
	var started bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		started, err = database.ChangeStatus(ctx, tx, id, from, StatusUploaded)
		if err != nil || !started {
			return err
		}
		if from == StatusFailed {
			if err := database.ClearError(ctx, tx, id); err != nil {
				return err
			}
		}
		if _, err := s.jobs.InsertTx(ctx, tx, queue.FileArgs{DocumentID: id}, nil); err != nil {
			return fmt.Errorf("add the file job of the document %s: %w", id, err)
		}
		return nil
	})
	return started, err
}

// Retry starts the work on a failed document again, from its first stage.
// It gives the status that the document has after the call.
//
// The document gets a new job. River can run a job again that ended, but
// River deletes such a job after some days, and a failed document must
// stay able to run again.
//
// The error is ErrNotFound when no document has the ID. It is a Conflict
// when the document is not failed, and when its file is not in the file
// storage: the service removes the file of an upload that no client
// confirmed.
func (s *Service) Retry(ctx context.Context, id uuid.UUID) (string, error) {
	doc, err := database.GetDocument(ctx, s.pool, id)
	if err != nil {
		return "", err
	}
	if doc.Status != StatusFailed {
		return "", notFailed(doc.Status)
	}
	inStorage, err := s.store.Exists(ctx, doc.StorageKey)
	if err != nil {
		return "", err
	}
	if !inStorage {
		return "", &Conflict{Detail: "The file is not in the storage. Ask for a new upload URL."}
	}
	started, err := s.startWork(ctx, id, StatusFailed)
	if err != nil {
		return "", err
	}
	if !started {
		// A different request started the work after the read.
		doc, err = database.GetDocument(ctx, s.pool, id)
		if err != nil {
			return "", err
		}
		return "", notFailed(doc.Status)
	}
	return StatusUploaded, nil
}

// notFailed is the refusal of a retry for a document that is not failed.
func notFailed(status string) *Conflict {
	return &Conflict{Detail: fmt.Sprintf("The document has the status %s. Only a failed document can run again.", status)}
}

// FailOldUploads fails each document that has the status uploading for
// longer than the age limit: no client confirmed its upload. It also
// removes the file of each one from the file storage, because a client can
// send the file and not confirm it. It gives the number of documents.
//
// The change of the status and the removal of the files are one unit. If
// the removal fails, the documents keep the status uploading, and the next
// call does the work again.
func (s *Service) FailOldUploads(ctx context.Context) (int, error) {
	var keys []string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		reason := fmt.Sprintf("the client did not confirm the upload in %s", s.cfg.UploadMaxAge)
		keys, err = database.ChangeStatusOfOld(ctx, tx, StatusUploading, s.cfg.UploadMaxAge, StatusFailed, reason)
		if err != nil || len(keys) == 0 {
			return err
		}
		return s.store.Delete(ctx, keys)
	})
	return len(keys), err
}
