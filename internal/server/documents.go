package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

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
	return startWork(docs.ConfirmUpload)
}

// retryDocument is called by the client to run a failed document again.
// The answer has the same form as the answer of confirmUpload.
func retryDocument(docs *documents.Service) endpoint {
	return startWork(docs.Retry)
}

// startWork makes the endpoint of a call that starts the work on one
// document. The answer is 202 Accepted with the place of the status.
func startWork(start func(ctx context.Context, id uuid.UUID) (status string, err error)) endpoint {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		status, err := start(r.Context(), id)
		if err != nil {
			return err
		}
		w.Header().Set("Location", "/documents/"+id.String())
		return writeJSON(w, http.StatusAccepted, documentStatus{ID: id, Status: status})
	}
}

// pathID reads the ID of a document from the path of the request. Text
// that is not an ID is the ID of no document.
func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, documents.ErrNotFound
	}
	return id, nil
}

// document is a document in an answer.
type document struct {
	ID       uuid.UUID `json:"id"`
	Filename string    `json:"filename"`
	Status   string    `json:"status"`
	// Error is the reason that the work on the document stopped. It is
	// null when the document has no error.
	Error *string `json:"error"`
	// ParentID is the archive that held the file. It is null for a file
	// that a client uploaded.
	ParentID *uuid.UUID `json:"parent_id"`
	// CreatedAt is in the format of RFC 3339, in UTC.
	CreatedAt time.Time `json:"created_at"`
}

func documentOf(doc documents.Document) document {
	out := document{ID: doc.ID, Filename: doc.Filename, Status: doc.Status, CreatedAt: doc.CreatedAt.UTC()}
	if doc.Error != "" {
		out.Error = &doc.Error
	}
	if doc.ParentID != uuid.Nil {
		out.ParentID = &doc.ParentID
	}
	return out
}

// getDocument answers with one document: its status and its error.
func getDocument(docs *documents.Service) endpoint {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		doc, err := docs.Get(r.Context(), id)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, documentOf(doc))
	}
}

// listDocumentsResponse is one page of the list of documents.
type listDocumentsResponse struct {
	// Documents are in the order from the newest to the oldest.
	Documents []document `json:"documents"`
	// HasMore tells that older documents come after this page. The client
	// gets them with the id of the last document as starting_after.
	HasMore bool `json:"has_more"`
}

// listDocuments answers with one page of the documents. The query can
// have three parameters, each optional: status, limit and starting_after.
func listDocuments(docs *documents.Service) endpoint {
	return func(w http.ResponseWriter, r *http.Request) error {
		query, err := readQuery(r, "status", "limit", "starting_after")
		if err != nil {
			return err
		}
		request := documents.ListRequest{Status: query["status"]}
		if text, given := query["limit"]; given {
			request.Limit, err = strconv.Atoi(text)
			if err != nil {
				return &apiError{status: http.StatusBadRequest, detail: "The limit must be a whole number."}
			}
		}
		if text, given := query["starting_after"]; given {
			request.After, err = uuid.Parse(text)
			if err != nil {
				return &apiError{status: http.StatusBadRequest, detail: "The starting_after must be the id of a document."}
			}
		}
		page, err := docs.List(r.Context(), request)
		if err != nil {
			return err
		}
		response := listDocumentsResponse{Documents: make([]document, len(page.Documents)), HasMore: page.HasMore}
		for i, doc := range page.Documents {
			response.Documents[i] = documentOf(doc)
		}
		return writeJSON(w, http.StatusOK, response)
	}
}
