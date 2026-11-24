package database

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

// chunksTextIndex is the name of the BM25 index on chunks.text. PostgreSQL
// gave the name in the first migration. pg_textsearch needs the name in a
// query that has parameters: its README says that the planner cannot find
// the index of a prepared query.
const chunksTextIndex = "chunks_text_idx"

// Search is one search of the chunks. The caller gives each value.
type Search struct {
	// Text is the question, for the keyword search.
	Text string
	// Vector is the vector of the question, for the vector search.
	Vector []float32
	// Statuses keeps the chunks of the documents with one of these
	// statuses.
	Statuses []string
	// DocumentIDs keeps the chunks of these documents. With no ID, each
	// document is in the search.
	DocumentIDs []uuid.UUID
	// Metadata keeps the chunks of the documents whose metadata has each
	// name and value of this JSON object. "{}" keeps each document.
	Metadata []byte
	// Candidates is the number of chunks that each of the two searches
	// gives to the merge.
	Candidates int
	// RankConstant is the constant k of reciprocal rank fusion.
	RankConstant int
	// Limit is the number of chunks in the result.
	Limit int
}

// SearchResult is one chunk that a search found.
type SearchResult struct {
	ChunkID    uuid.UUID
	DocumentID uuid.UUID
	Filename   string
	// Title is "" for a document with no title.
	Title       string
	HeadingPath []string
	// PageStart and PageEnd are 0 for a document with no pages.
	PageStart, PageEnd int
	BlockIDs           []uuid.UUID
	Text               string
	// Score is the sum of 1 / (k + rank) for the two searches.
	Score float64
	// KeywordRank and VectorRank are the positions of the chunk in the two
	// searches, from 1. A rank is 0 when the search did not find the chunk.
	KeywordRank, VectorRank int
}

// SetVectorScan makes the vector index give the wanted number of chunks to
// the search that follows in the same transaction.
//
// The pgvector README says two things about its HNSW index. One: the index
// gives hnsw.ef_search rows at most, 40 by default. Two: it applies a
// WHERE condition after the scan, so a search with a filter can get fewer
// rows than its LIMIT, and hnsw.iterative_scan makes the index scan more
// until the LIMIT is full.
func SetVectorScan(ctx context.Context, tx pgx.Tx, candidates int) error {
	_, err := tx.Exec(ctx,
		`select set_config('hnsw.ef_search', $1::int::text, true),
		        set_config('hnsw.iterative_scan', 'strict_order', true)`,
		candidates,
	)
	if err != nil {
		return fmt.Errorf("set the scan of the vector index: %w", err)
	}
	return nil
}

// SearchChunks runs the keyword search and the vector search of one
// question and merges the two lists with reciprocal rank fusion (Cormack,
// Clarke and Büttcher, 2009): the score of a chunk is the sum of
// 1 / (k + rank) for each list that has it. The form of the query is the
// hybrid search example of pgvector.
//
//   - keyword: BM25 of pg_textsearch. Its score is negative, and lower is
//     better. A chunk with no word of the question has the score 0, so only
//     a score below 0 is a match.
//   - vector: cosine distance of pgvector. ORDER BY has the distance
//     operator and nothing more, because the index needs that form.
func SearchChunks(ctx context.Context, db DB, search Search) ([]SearchResult, error) {
	rows, err := db.Query(ctx,
		`with keyword as materialized (
		   select c.id, c.text <@> to_bm25query($1, $2) as score
		   from chunks c
		   join documents d on d.id = c.document_id
		   where d.status = any($4)
		     and (cardinality($5::uuid[]) = 0 or d.id = any($5))
		     and d.metadata @> $6::jsonb
		   order by c.text <@> to_bm25query($1, $2)
		   limit $7
		 ),
		 keyword_ranked as (
		   select id, rank() over (order by score) as rank from keyword where score < 0
		 ),
		 vector as materialized (
		   select c.id, c.embedding <=> $3 as distance
		   from chunks c
		   join documents d on d.id = c.document_id
		   where d.status = any($4)
		     and (cardinality($5::uuid[]) = 0 or d.id = any($5))
		     and d.metadata @> $6::jsonb
		   order by c.embedding <=> $3
		   limit $7
		 ),
		 vector_ranked as (
		   select id, rank() over (order by distance) as rank from vector
		 ),
		 merged as (
		   select coalesce(k.id, v.id) as id,
		          coalesce(k.rank, 0) as keyword_rank,
		          coalesce(v.rank, 0) as vector_rank,
		          coalesce(1.0 / ($8::int + k.rank), 0) + coalesce(1.0 / ($8::int + v.rank), 0) as score
		   from keyword_ranked k
		   full outer join vector_ranked v on v.id = k.id
		   order by score desc, id
		   limit $9
		 )
		 select c.id, c.document_id, d.filename, coalesce(d.title, '') as title, c.heading_path,
		        coalesce(c.page_start, 0) as page_start, coalesce(c.page_end, 0) as page_end,
		        c.block_ids, c.text, m.score::float8 as score, m.keyword_rank, m.vector_rank
		 from merged m
		 join chunks c on c.id = m.id
		 join documents d on d.id = c.document_id
		 order by m.score desc, m.id`,
		search.Text, chunksTextIndex, pgvector.NewVector(search.Vector), search.Statuses,
		search.DocumentIDs, search.Metadata, search.Candidates, search.RankConstant, search.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search the chunks: %w", err)
	}
	results, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (SearchResult, error) {
		var r SearchResult
		err := row.Scan(&r.ChunkID, &r.DocumentID, &r.Filename, &r.Title, &r.HeadingPath,
			&r.PageStart, &r.PageEnd, &r.BlockIDs, &r.Text, &r.Score, &r.KeywordRank, &r.VectorRank)
		return r, err
	})
	if err != nil {
		return nil, fmt.Errorf("search the chunks: %w", err)
	}
	return results, nil
}
