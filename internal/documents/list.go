package documents

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/Njunge11/rejea/internal/database"
)

// ListRequest tells which documents a caller wants.
type ListRequest struct {
	// Status keeps only the documents with this status. With "", the list
	// has the documents of each status.
	Status string
	// After is the ID of the last document of the page before this one.
	// With uuid.Nil, the list starts at the newest document.
	After uuid.UUID
	// Limit is the largest number of documents in the page. With 0, the
	// page has the default number.
	Limit int
}

// Page is one page of the list of documents.
type Page struct {
	// Documents are in the order from the newest to the oldest.
	Documents []Document
	// HasMore tells that older documents come after this page. The caller
	// gets them with the ID of the last document as After.
	HasMore bool
}

// List gives one page of the documents, from the newest to the oldest. The
// error is an InvalidArgument when the request has a value that the rules
// refuse.
func (s *Service) List(ctx context.Context, request ListRequest) (Page, error) {
	if request.Status != "" && !slices.Contains(statuses, request.Status) {
		return Page{}, &InvalidArgument{Detail: "The status must be one of: " + strings.Join(statuses, ", ") + "."}
	}
	limit := request.Limit
	if limit == 0 {
		limit = s.cfg.ListLimit
	}
	if limit < 0 || limit > s.cfg.MaxListLimit {
		return Page{}, &InvalidArgument{Detail: fmt.Sprintf("The limit must be a number from 1 to %d.", s.cfg.MaxListLimit)}
	}
	// One document more than the page tells if a next page exists.
	docs, err := database.ListDocuments(ctx, s.pool, request.Status, request.After, limit+1)
	if err != nil {
		return Page{}, err
	}
	if len(docs) > limit {
		return Page{Documents: docs[:limit], HasMore: true}, nil
	}
	return Page{Documents: docs}, nil
}
