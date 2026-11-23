package server

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/queue"
	"github.com/Njunge11/rejea/internal/storage"
)

// createDocumentsRequest is the body of a request for upload URLs.
type createDocumentsRequest struct {
	Files []uploadFile `json:"files"`
}

// uploadFile is one file that the client wants to upload.
type uploadFile struct {
	Filename string `json:"filename"`
	// Size is the size of the file in bytes.
	Size int64 `json:"size"`
	// Metadata is optional: data about the file that a person gave.
	Metadata map[string]any `json:"metadata"`
}

type createDocumentsResponse struct {
	Documents []createdDocument `json:"documents"`
}

// createdDocument is a new document and the URL for its file. The client
// sends the file to the URL with an HTTP PUT.
type createdDocument struct {
	ID        uuid.UUID `json:"id"`
	Filename  string    `json:"filename"`
	Status    string    `json:"status"`
	UploadURL string    `json:"upload_url"`
}

// createDocuments makes one document with the status uploading and one
// upload URL for each file in the request.
func createDocuments(pool *pgxpool.Pool, store *storage.Store, cfg Config) endpoint {
	return func(w http.ResponseWriter, r *http.Request) error {
		request, err := readJSON[createDocumentsRequest](w, r, cfg.MaxRequestBytes)
		if err != nil {
			return err
		}
		if faults := request.faults(cfg); len(faults) > 0 {
			return &apiError{
				status: http.StatusUnprocessableEntity,
				detail: "The request is not valid.",
				faults: faults,
			}
		}

		// The URLs are signed before the rows are written. Thus a failure
		// leaves no row that has no URL.
		docs := make([]database.NewDocument, len(request.Files))
		created := make([]createdDocument, len(request.Files))
		for i, file := range request.Files {
			id, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("make a document id: %w", err)
			}
			// The key has the id, not the name of the file, so each
			// character in a file name is safe.
			key := "documents/" + id.String() + "/original" + fileType(file.Filename)
			url, err := store.UploadURL(r.Context(), key, file.Size)
			if err != nil {
				return err
			}
			metadata := file.Metadata
			if metadata == nil {
				metadata = map[string]any{}
			}
			docs[i] = database.NewDocument{
				ID:         id,
				UploadedBy: uuid.Nil, // The system: the service has no login yet.
				Filename:   file.Filename,
				StorageKey: key,
				Metadata:   metadata,
			}
			created[i] = createdDocument{
				ID:        id,
				Filename:  file.Filename,
				Status:    database.StatusUploading,
				UploadURL: url,
			}
		}
		if err := database.InsertUploadingDocuments(r.Context(), pool, docs); err != nil {
			return err
		}
		return writeJSON(w, http.StatusCreated, createDocumentsResponse{Documents: created})
	}
}

// documentStatus is the answer of an endpoint that changes one document.
type documentStatus struct {
	ID     uuid.UUID `json:"id"`
	Status string    `json:"status"`
}

// confirmUpload is called by the client after it sent the file to the
// upload URL. It gives the document the status uploaded and adds the file
// job, both in one transaction: a document is never uploaded with no job,
// and a job never exists for a document that is not uploaded.
//
// The answer is 202 Accepted, because the work on the file continues after
// the answer. The Location header has the place of the status.
//
// A second confirm of the same document changes nothing and gets the same
// answer, so a client can send the request again after a network fault.
func confirmUpload(pool *pgxpool.Pool, store *storage.Store, jobs *river.Client[pgx.Tx]) endpoint {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			return noDocument()
		}
		state, err := database.GetFileState(ctx, pool, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return noDocument()
		}
		if err != nil {
			return err
		}
		switch state.Status {
		case database.StatusUploading:
			inStorage, err := store.Exists(ctx, state.StorageKey)
			if err != nil {
				return err
			}
			if !inStorage {
				return &apiError{
					status: http.StatusConflict,
					detail: "The file is not in the storage. Send the file to its upload URL, then confirm.",
				}
			}
			err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
				confirmed, err := database.ConfirmUpload(ctx, tx, id)
				if err != nil || !confirmed {
					// A different request confirmed the document first.
					return err
				}
				if _, err := jobs.InsertTx(ctx, tx, queue.FileArgs{DocumentID: id}, nil); err != nil {
					return fmt.Errorf("add the file job of the document %s: %w", id, err)
				}
				return nil
			})
			if err != nil {
				return err
			}
			state.Status = database.StatusUploaded
		case database.StatusFailed:
			return &apiError{
				status: http.StatusConflict,
				detail: "The document has the status failed. Ask for a new upload URL.",
			}
		}
		w.Header().Set("Location", "/documents/"+id.String())
		return writeJSON(w, http.StatusAccepted, documentStatus{ID: id, Status: state.Status})
	}
}

// noDocument is the answer for an id that is not the id of a document.
func noDocument() *apiError {
	return &apiError{status: http.StatusNotFound, detail: "No document has this id."}
}

// faults finds each fault in the request.
func (request createDocumentsRequest) faults(cfg Config) []fault {
	if len(request.Files) == 0 {
		return []fault{{Detail: "Give one file or more.", Pointer: "#/files"}}
	}
	if len(request.Files) > cfg.MaxUploadFiles {
		return []fault{{
			Detail:  fmt.Sprintf("One request can have %d files at most, and this one has %d.", cfg.MaxUploadFiles, len(request.Files)),
			Pointer: "#/files",
		}}
	}
	var faults []fault
	for i, file := range request.Files {
		at := fmt.Sprintf("#/files/%d/", i)
		switch {
		case file.Filename == "":
			faults = append(faults, fault{Detail: "Give the name of the file.", Pointer: at + "filename"})
		case strings.ContainsFunc(file.Filename, unicode.IsControl):
			faults = append(faults, fault{Detail: "The name of the file has a control character.", Pointer: at + "filename"})
		case fileType(file.Filename) == "":
			faults = append(faults, fault{Detail: "The file must be a .pdf, .docx, .doc, .zip or .tar file.", Pointer: at + "filename"})
		}
		switch {
		case file.Size < 1:
			faults = append(faults, fault{Detail: "Give the size of the file in bytes, 1 or more.", Pointer: at + "size"})
		case file.Size > cfg.MaxFileBytes:
			faults = append(faults, fault{
				Detail:  fmt.Sprintf("A file can have %d bytes at most, and this one has %d.", cfg.MaxFileBytes, file.Size),
				Pointer: at + "size",
			})
		}
		if hasNUL(file.Metadata) {
			faults = append(faults, fault{Detail: "The metadata has a NUL character, which the database cannot keep.", Pointer: at + "metadata"})
		}
	}
	return faults
}

// fileType gives the ending of a file name in lower case when the service
// accepts that type of file. If not, it gives "".
func fileType(filename string) string {
	switch ending := strings.ToLower(path.Ext(filename)); ending {
	case ".pdf", ".docx", ".doc", ".zip", ".tar":
		return ending
	}
	return ""
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
