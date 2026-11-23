package server

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/Njunge11/rejea/internal/documents"
)

// The endpoints in this file do HTTP work only: they read the request,
// call the package documents, and write the answer. The package documents
// has each rule, and writeError turns its errors into answers.

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

// createDocuments answers with one new document and one upload URL for
// each file in the request.
func createDocuments(docs *documents.Service, maxRequestBytes int64) endpoint {
	return func(w http.ResponseWriter, r *http.Request) error {
		request, err := readJSON[createDocumentsRequest](w, r, maxRequestBytes)
		if err != nil {
			return err
		}
		files := make([]documents.FileRequest, len(request.Files))
		for i, file := range request.Files {
			files[i] = documents.FileRequest{Filename: file.Filename, Size: file.Size, Metadata: file.Metadata}
		}
		uploads, err := docs.RequestUploads(r.Context(), files)
		if err != nil {
			return err
		}
		created := make([]createdDocument, len(uploads))
		for i, upload := range uploads {
			created[i] = createdDocument{
				ID:        upload.ID,
				Filename:  upload.Filename,
				Status:    upload.Status,
				UploadURL: upload.UploadURL,
			}
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
// upload URL.
//
// The answer is 202 Accepted, because the work on the file continues after
// the answer. The Location header has the place of the status.
func confirmUpload(docs *documents.Service) endpoint {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			// Text that is not an ID is the ID of no document.
			return documents.ErrNotFound
		}
		status, err := docs.ConfirmUpload(r.Context(), id)
		if err != nil {
			return err
		}
		w.Header().Set("Location", "/documents/"+id.String())
		return writeJSON(w, http.StatusAccepted, documentStatus{ID: id, Status: status})
	}
}
