package testset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"
)

// A search report measures the search with the questions of the test set.
// The measures are those of trec_eval, the tool of NIST for search
// measures:
//
//   - success at K: a correct page is in the first K results.
//   - recall at K: the part of the correct pages that the first K results
//     have.
//   - MRR (mean reciprocal rank): 1 / the position of the first result on
//     a correct page, as the mean of the questions.
//
// A result is a chunk with a first and a last page. It is on a correct page
// when it is from the document of a source and its pages have a page of
// that source.

// Cutoffs is each K of "success at K" and "recall at K".
var Cutoffs = []int{1, 3, 5, 10}

// Depth is the number of results that a report reads for one question: the
// largest cutoff. A correct page after this position counts as not found.
const Depth = 10

// Found is one result of a search, in the terms of the test set.
type Found struct {
	// Document is the ID of the document in the test set. It is "" for a
	// document that is not in the set.
	Document string
	// PageStart and PageEnd are the first and the last page of the chunk.
	// They are 0 for a document with no pages.
	PageStart, PageEnd int
}

// Report is one measurement of the search. A saved report is the benchmark
// that a later report is compared with.
type Report struct {
	CreatedAt time.Time `json:"created_at"`
	// QuestionsSHA256 is the hash of the question file. Two reports with
	// different hashes do not measure the same questions.
	QuestionsSHA256 string     `json:"questions_sha256"`
	Settings        Settings   `json:"settings"`
	Collection      Collection `json:"collection"`
	// Runs has one run without the reranker, one run with it, or the two.
	Runs []Run `json:"runs"`
	// NotMeasured is the number of questions of the kind no_answer. No page
	// is correct for them, so they are in no measure of the search.
	NotMeasured int `json:"not_measured"`
}

// Settings is the settings of the service that change the results of a
// search.
type Settings struct {
	ChunkMaxTokens   int    `json:"chunk_max_tokens"`
	EmbedProvider    string `json:"embed_provider"`
	EmbedModel       string `json:"embed_model"`
	SearchCandidates int    `json:"search_candidates"`
	RankConstant     int    `json:"rank_constant"`
	RerankProvider   string `json:"rerank_provider"`
	RerankModel      string `json:"rerank_model"`
	RerankCandidates int    `json:"rerank_candidates"`
}

// Collection is the documents of the test set in the service.
type Collection struct {
	Documents int `json:"documents"`
	Chunks    int `json:"chunks"`
}

// Run is the measures of all the questions with one mode of the search.
type Run struct {
	// Reranker is true for the search with the reranker.
	Reranker  bool     `json:"reranker"`
	Questions int      `json:"questions"`
	At        []Cutoff `json:"at"`
	MRR       float64  `json:"mrr"`
	TimeMS    Times    `json:"time_ms"`
	Results   []Result `json:"results"`
}

// Cutoff is the measures at one K, each from 0 to 1.
type Cutoff struct {
	K       int     `json:"k"`
	Success float64 `json:"success"`
	Recall  float64 `json:"recall"`
}

// Times is the time of one search, in milliseconds.
type Times struct {
	Median  int64 `json:"median"`
	Longest int64 `json:"longest"`
}

// Result is the result of one question.
type Result struct {
	ID   string `json:"id"`
	Kind string `json:"type"`
	// FirstRank is the position of the first result on a correct page,
	// from 1. It is 0 when no result of the first Depth is on one.
	FirstRank int `json:"first_rank"`
	// Pages has each correct page with the position of the first result
	// that has it.
	Pages  []PageRank `json:"pages"`
	TimeMS int64      `json:"time_ms"`
}

// PageRank is one correct page of a question.
type PageRank struct {
	Document string `json:"document"`
	Page     int    `json:"page"`
	// Rank is the position of the first result that has the page, from 1.
	// It is 0 when no result of the first Depth has it.
	Rank int `json:"rank"`
}

// Judge compares the results of a search with the correct pages of the
// question. The results are in the order of the search, the best first.
func (q Question) Judge(found []Found, elapsed time.Duration) Result {
	result := Result{ID: q.ID, Kind: q.Kind, TimeMS: elapsed.Milliseconds()}
	for _, source := range q.Sources {
		for _, page := range source.Pages {
			rank := 0
			for position, f := range found[:min(len(found), Depth)] {
				if f.Document == source.Document && f.PageStart <= page && page <= f.PageEnd {
					rank = position + 1
					break
				}
			}
			result.Pages = append(result.Pages, PageRank{Document: source.Document, Page: page, Rank: rank})
			if rank > 0 && (result.FirstRank == 0 || rank < result.FirstRank) {
				result.FirstRank = rank
			}
		}
	}
	return result
}

// Measure gives the measures of the results of one mode of the search.
func Measure(reranker bool, results []Result) Run {
	run := Run{Reranker: reranker, Questions: len(results), Results: results}
	if len(results) == 0 {
		return run
	}
	questions := float64(len(results))
	for _, k := range Cutoffs {
		cutoff := Cutoff{K: k}
		for _, r := range results {
			if r.FirstRank > 0 && r.FirstRank <= k {
				cutoff.Success++
			}
			found := 0
			for _, page := range r.Pages {
				if page.Rank > 0 && page.Rank <= k {
					found++
				}
			}
			cutoff.Recall += float64(found) / float64(len(r.Pages))
		}
		cutoff.Success /= questions
		cutoff.Recall /= questions
		run.At = append(run.At, cutoff)
	}
	times := make([]int64, len(results))
	for i, r := range results {
		if r.FirstRank > 0 {
			run.MRR += 1 / float64(r.FirstRank)
		}
		times[i] = r.TimeMS
	}
	run.MRR /= questions
	slices.Sort(times)
	run.TimeMS = Times{Median: times[len(times)/2], Longest: times[len(times)-1]}
	return run
}

// ReadReport reads a saved report.
func ReadReport(path string) (Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Report{}, fmt.Errorf("read the report: %w: give the path of a report file (make search-report makes testset/reports/last.json)", err)
	}
	var report Report
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return Report{}, fmt.Errorf("the file %s is not a search report: %w: make it again with make search-report", path, err)
	}
	if len(report.Runs) == 0 {
		return Report{}, fmt.Errorf("the file %s is not a search report: it has no run: make it again with make search-report", path)
	}
	return report, nil
}

// mode gives the name of a run for a person.
func mode(reranker bool) string {
	if reranker {
		return "with the reranker"
	}
	return "without the reranker"
}

// count gives a part of the questions as a number of questions.
func count(part float64, questions int) int {
	return int(math.Round(part * float64(questions)))
}

// Text gives the report for a person: the settings and one table.
func (r Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Search report of %s\n", r.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "  Collection: %d documents of the test set, %d chunks of %d tokens at most\n",
		r.Collection.Documents, r.Collection.Chunks, r.Settings.ChunkMaxTokens)
	fmt.Fprintf(&b, "  Embedding model: %s %s\n", r.Settings.EmbedProvider, r.Settings.EmbedModel)
	if slices.ContainsFunc(r.Runs, func(run Run) bool { return run.Reranker }) {
		fmt.Fprintf(&b, "  Reranker model: %s %s (it reads %d chunks)\n", r.Settings.RerankProvider, r.Settings.RerankModel, r.Settings.RerankCandidates)
	}
	fmt.Fprintf(&b, "  Questions: %d measured, %d of the kind no_answer not measured\n\n", r.Runs[0].Questions, r.NotMeasured)

	fmt.Fprintf(&b, "  %-22s", "A correct page is")
	for _, k := range Cutoffs {
		fmt.Fprintf(&b, "  %-10s", fmt.Sprintf("in first %d", k))
	}
	fmt.Fprintf(&b, "  %-12s  %-6s  %s\n", "recall at 10", "MRR", "median time")
	for _, run := range r.Runs {
		fmt.Fprintf(&b, "  %-22s", mode(run.Reranker))
		recall := 0.0
		for _, at := range run.At {
			fmt.Fprintf(&b, "  %-10s", fmt.Sprintf("%d of %d", count(at.Success, run.Questions), run.Questions))
			recall = at.Recall
		}
		fmt.Fprintf(&b, "  %-12.3f  %-6.3f  %d ms\n", recall, run.MRR, run.TimeMS.Median)
	}
	return b.String()
}

// Compare gives the differences between a saved report and a later one:
// the settings that changed, each measure, and each question whose first
// correct result moved.
func Compare(saved, latest Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Compared with the saved report of %s\n", saved.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"))
	if saved.QuestionsSHA256 != latest.QuestionsSHA256 {
		b.WriteString("  The question file changed after the saved report: the two reports do not measure the same questions.\n")
	}
	changed := false
	setting := func(name string, before, after any) {
		if before != after {
			fmt.Fprintf(&b, "  Changed: %s was %v, and is %v\n", name, before, after)
			changed = true
		}
	}
	setting("chunk_max_tokens", saved.Settings.ChunkMaxTokens, latest.Settings.ChunkMaxTokens)
	setting("chunks", saved.Collection.Chunks, latest.Collection.Chunks)
	setting("documents", saved.Collection.Documents, latest.Collection.Documents)
	setting("embed_model", saved.Settings.EmbedProvider+" "+saved.Settings.EmbedModel, latest.Settings.EmbedProvider+" "+latest.Settings.EmbedModel)
	setting("search_candidates", saved.Settings.SearchCandidates, latest.Settings.SearchCandidates)
	setting("rank_constant", saved.Settings.RankConstant, latest.Settings.RankConstant)
	setting("rerank_model", saved.Settings.RerankProvider+" "+saved.Settings.RerankModel, latest.Settings.RerankProvider+" "+latest.Settings.RerankModel)
	setting("rerank_candidates", saved.Settings.RerankCandidates, latest.Settings.RerankCandidates)
	if !changed {
		b.WriteString("  The settings and the collection are the same.\n")
	}

	for _, now := range latest.Runs {
		index := slices.IndexFunc(saved.Runs, func(run Run) bool { return run.Reranker == now.Reranker })
		if index < 0 {
			fmt.Fprintf(&b, "\n  %s: the saved report has no such run.\n", mode(now.Reranker))
			continue
		}
		before := saved.Runs[index]
		fmt.Fprintf(&b, "\n  %s  %-10s  %-10s  %s\n", fmt.Sprintf("%-28s", mode(now.Reranker)), "saved", "now", "change")
		line := func(name, was, is string, delta float64, unit string) {
			change := "same"
			if delta != 0 {
				change = fmt.Sprintf("%+.3g%s", delta, unit)
			}
			fmt.Fprintf(&b, "    %-26s  %-10s  %-10s  %s\n", name, was, is, change)
		}
		for i, at := range now.At {
			if i >= len(before.At) || before.At[i].K != at.K {
				continue
			}
			was, is := count(before.At[i].Success, before.Questions), count(at.Success, now.Questions)
			line(fmt.Sprintf("correct page in first %d", at.K), fmt.Sprintf("%d of %d", was, before.Questions), fmt.Sprintf("%d of %d", is, now.Questions), float64(is-was), "")
		}
		if len(now.At) > 0 && len(before.At) == len(now.At) {
			was, is := before.At[len(before.At)-1].Recall, now.At[len(now.At)-1].Recall
			line(fmt.Sprintf("recall at %d", Depth), fmt.Sprintf("%.3f", was), fmt.Sprintf("%.3f", is), round(is-was), "")
		}
		line("MRR", fmt.Sprintf("%.3f", before.MRR), fmt.Sprintf("%.3f", now.MRR), round(now.MRR-before.MRR), "")
		line("median time", fmt.Sprintf("%d ms", before.TimeMS.Median), fmt.Sprintf("%d ms", now.TimeMS.Median), float64(now.TimeMS.Median-before.TimeMS.Median), " ms")

		moved := 0
		for _, result := range now.Results {
			i := slices.IndexFunc(before.Results, func(r Result) bool { return r.ID == result.ID })
			if i < 0 || before.Results[i].FirstRank == result.FirstRank {
				continue
			}
			if moved == 0 {
				b.WriteString("    Questions whose first correct result moved (position saved -> now; 0 is not in the first 10):\n")
			}
			moved++
			fmt.Fprintf(&b, "      %-40s %d -> %d\n", result.ID, before.Results[i].FirstRank, result.FirstRank)
		}
		if moved == 0 {
			b.WriteString("    No question has its first correct result at a different position.\n")
		}
	}
	return b.String()
}

// round keeps 3 decimal places, so that the difference of two numbers
// that are the same in print is 0.
func round(x float64) float64 {
	return math.Round(x*1000) / 1000
}
