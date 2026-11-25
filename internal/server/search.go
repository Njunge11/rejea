package server

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Njunge11/rejea/internal/documents"
)

// searchRequest is the body of a search.
type searchRequest struct {
	// Query is the question.
	Query string `json:"query"`
	// Limit is optional: the largest number of results.
	Limit int `json:"limit"`
	// Filters is optional: it keeps the search in a part of the documents.
	Filters searchFilters `json:"filters"`
	// Rerank is optional: true or false tells if the search uses the
	// reranker. With no value, the setting of the service decides.
	Rerank *bool `json:"rerank"`
}

type searchFilters struct {
	// DocumentIDs keeps the search in these documents.
	DocumentIDs []string `json:"document_ids"`
	// Metadata keeps the search in the documents whose metadata has each
	// name and value of this object.
	Metadata map[string]any `json:"metadata"`
}

type searchResponse struct {
	// Results are in the order from the best to the worst.
	Results []searchResult `json:"results"`
}

// searchResult is one chunk that the search found, with the document and
// the pages that it came from.
type searchResult struct {
	ChunkID  uuid.UUID      `json:"chunk_id"`
	Document searchDocument `json:"document"`
	// PageStart and PageEnd are the first and the last page of the text.
	// They are null for a document with no pages, such as a Word file.
	PageStart *int `json:"page_start"`
	PageEnd   *int `json:"page_end"`
	// HeadingPath is the headings above the text, the title first.
	HeadingPath []string `json:"heading_path"`
	Text        string   `json:"text"`
	// BlockIDs is the blocks of the page that the text came from: a viewer
	// draws a box on each.
	BlockIDs []uuid.UUID `json:"block_ids"`
	// Score is the sum of 1 / (k + rank) for the keyword search and the
	// vector search. It puts the results in order when the search does not
	// use the reranker, and it is not a measure of how good a result is.
	Score float64 `json:"score"`
	// RerankScore is the score of the reranker, from 0 to 1: how well the
	// text answers the question. It puts the results in order when the
	// search uses the reranker. It is null for a search without it.
	RerankScore *float64 `json:"rerank_score"`
	// KeywordRank and VectorRank are the positions of the chunk in the two
	// searches, from 1. A rank is null when that search did not find the
	// chunk.
	KeywordRank *int `json:"keyword_rank"`
	VectorRank  *int `json:"vector_rank"`
}

type searchDocument struct {
	ID       uuid.UUID `json:"id"`
	Filename string    `json:"filename"`
	// Title is null for a document with no title.
	Title *string `json:"title"`
}

// search answers with the chunks that agree best with a question.
//
// The method is POST because the question and its filters are a JSON
// body. The call changes nothing.
func search(docs *documents.Service, maxRequestBytes int64) endpoint {
	return func(w http.ResponseWriter, r *http.Request) error {
		request, err := readJSON[searchRequest](w, r, maxRequestBytes)
		if err != nil {
			return err
		}
		ids := make([]uuid.UUID, len(request.Filters.DocumentIDs))
		for i, text := range request.Filters.DocumentIDs {
			id, err := uuid.Parse(text)
			if err != nil {
				return &apiError{
					status: http.StatusUnprocessableEntity,
					detail: "The request is not valid.",
					faults: []fault{{
						Detail:  "This value is not the ID of a document: an ID is a UUID.",
						Pointer: fmt.Sprintf("#/filters/document_ids/%d", i),
					}},
				}
			}
			ids[i] = id
		}
		found, err := docs.Search(r.Context(), documents.SearchRequest{
			Query:       request.Query,
			Limit:       request.Limit,
			DocumentIDs: ids,
			Metadata:    request.Filters.Metadata,
			Rerank:      request.Rerank,
		})
		if err != nil {
			return err
		}
		results := make([]searchResult, len(found))
		for i, f := range found {
			results[i] = searchResult{
				ChunkID:     f.ChunkID,
				Document:    searchDocument{ID: f.DocumentID, Filename: f.Filename, Title: positiveText(f.Title)},
				PageStart:   positive(f.PageStart),
				PageEnd:     positive(f.PageEnd),
				HeadingPath: f.HeadingPath,
				Text:        f.Text,
				BlockIDs:    f.BlockIDs,
				Score:       f.Score,
				RerankScore: f.RerankScore,
				KeywordRank: positive(f.KeywordRank),
				VectorRank:  positive(f.VectorRank),
			}
		}
		return writeJSON(w, http.StatusOK, searchResponse{Results: results})
	}
}

// positive gives a number for an answer: null in place of 0, which means
// "none" in the results of the package documents.
func positive(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

// positiveText gives a text for an answer: null in place of "".
func positiveText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
