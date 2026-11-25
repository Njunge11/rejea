// Package testset reads the question file: the questions that measure the
// search and the answers of the service, each with its correct answer and
// the pages that have it. Each later change of the service is measured
// with the same questions.
//
// The kinds of question are levels 1 to 3 of GraphRAG-Bench (fact
// retrieval, complex reasoning, contextual summary), and the questions
// with no answer are those of SQuAD 2.0: they look like the others, and
// the correct result is to give no answer.
package testset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

// The kinds of question.
const (
	// Fact is a question that one passage answers.
	Fact = "fact"
	// Reasoning is a question that needs two or more passages together.
	Reasoning = "reasoning"
	// Summary is a question that needs passages of two or more documents.
	Summary = "summary"
	// NoAnswer is a question that the documents cannot answer.
	NoAnswer = "no_answer"
)

// Kinds is each kind of question, in the order of a report.
var Kinds = []string{Fact, Reasoning, Summary, NoAnswer}

// Set is the content of a question file.
type Set struct {
	// Documents is the documents that the questions are about.
	Documents []Document `json:"documents"`
	Questions []Question `json:"questions"`
}

// Document is one document of the test set.
type Document struct {
	// ID is the short name that a source of a question uses.
	ID string `json:"id"`
	// Type is the kind of report, as its publisher names it.
	Type  string `json:"type"`
	Title string `json:"title"`
	// Filename and URL tell where to get the file.
	Filename string `json:"filename"`
	URL      string `json:"url"`
	// SHA256 is the hash of the file, in hexadecimal. The service keeps the
	// same hash for an uploaded file, so it finds the document.
	SHA256 string `json:"sha256"`
	// Pages is the number of pages of the file.
	Pages int `json:"pages"`
}

// Question is one question with its correct answer.
type Question struct {
	ID       string `json:"id"`
	Kind     string `json:"type"`
	Question string `json:"question"`
	// Answer is the correct answer. It is empty for the kind no_answer.
	Answer string `json:"answer"`
	// Sources is the pages that have the answer. It is empty for the kind
	// no_answer.
	Sources []Source `json:"sources"`
	// Evidence is the words or the numbers of the pages that give the
	// answer. For the kind no_answer, it tells why the documents have no
	// answer.
	Evidence string `json:"evidence"`
}

// Source is the pages of one document that have a part of an answer.
type Source struct {
	// Document is the ID of a document of the set.
	Document string `json:"document"`
	// Pages is the page numbers. The first page of a document is 1.
	Pages []int `json:"pages"`
}

var (
	idForm   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	hashForm = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Load reads a question file and examines it. The error names each fault
// of the file, with its place and its correction.
func Load(path string) (Set, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Set{}, fmt.Errorf("read the question file: %w: give the path of a question file (the file of this repository is testset/questions.json)", err)
	}
	return Parse(path, raw)
}

// Parse examines the content of a question file. The path is for the text
// of an error.
func Parse(path string, raw []byte) (Set, error) {
	var set Set
	decoder := json.NewDecoder(bytes.NewReader(raw))
	// A field with a wrong name is a fault, not a field with no value.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&set); err != nil {
		return Set{}, fmt.Errorf("the question file %s is not the JSON of a test set%s: %w: correct the file (README.md shows its fields)", path, line(raw, err), err)
	}
	if faults := set.faults(); len(faults) > 0 {
		return Set{}, fmt.Errorf("the question file %s has %s:\n  %s", path, number(len(faults), "fault"), strings.Join(faults, "\n  "))
	}
	return set, nil
}

// line gives the line of the file that has the fault, when the decoder
// tells its position.
func line(raw []byte, err error) string {
	var offset int64
	var syntax *json.SyntaxError
	var value *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		offset = syntax.Offset
	case errors.As(err, &value):
		offset = value.Offset
	default:
		return ""
	}
	offset = min(max(offset, 0), int64(len(raw)))
	return fmt.Sprintf(" (line %d)", 1+bytes.Count(raw[:offset], []byte("\n")))
}

// number gives a count with its noun: "1 page", "3 pages".
func number(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// faults gives each fault of the set.
func (s Set) faults() []string {
	var faults []string
	fault := func(format string, args ...any) { faults = append(faults, fmt.Sprintf(format, args...)) }

	pages := map[string]int{}
	for i, doc := range s.Documents {
		place := fmt.Sprintf("document %d (%q)", i+1, doc.ID)
		switch {
		case !idForm.MatchString(doc.ID):
			fault("%s: the id must have small letters, digits and hyphens only", place)
		case pages[doc.ID] != 0:
			fault("%s: a document before it has the same id", place)
		}
		if strings.TrimSpace(doc.Title) == "" || strings.TrimSpace(doc.Filename) == "" || strings.TrimSpace(doc.Type) == "" {
			fault("%s: give it a type, a title and a filename", place)
		}
		if !hashForm.MatchString(doc.SHA256) {
			fault("%s: sha256 must be the 64 hexadecimal characters of the hash of the file (shasum -a 256 <file>)", place)
		}
		if doc.Pages < 1 {
			fault("%s: pages must be the number of pages of the file, 1 or more", place)
		}
		pages[doc.ID] = max(doc.Pages, 1)
	}
	if len(s.Documents) == 0 {
		fault("the file has no documents: add the list \"documents\"")
	}

	seen := map[string]bool{}
	for i, q := range s.Questions {
		place := fmt.Sprintf("question %d (%q)", i+1, q.ID)
		switch {
		case !idForm.MatchString(q.ID):
			fault("%s: the id must have small letters, digits and hyphens only", place)
		case seen[q.ID]:
			fault("%s: a question before it has the same id", place)
		}
		seen[q.ID] = true
		if strings.TrimSpace(q.Question) == "" {
			fault("%s: it has no question text", place)
		}
		if strings.TrimSpace(q.Evidence) == "" {
			fault("%s: give the evidence: the words of the page that give the answer, or the reason that no page has one", place)
		}
		if !slices.Contains(Kinds, q.Kind) {
			fault("%s: the type is %q, and it must be one of: %s", place, q.Kind, strings.Join(Kinds, ", "))
			continue
		}
		if q.Kind == NoAnswer {
			if q.Answer != "" || len(q.Sources) > 0 {
				fault("%s: a question of the type no_answer has no answer and no sources", place)
			}
			continue
		}
		if strings.TrimSpace(q.Answer) == "" {
			fault("%s: it has no answer", place)
		}
		if len(q.Sources) == 0 {
			fault("%s: it has no sources: give the document and the pages that have the answer", place)
		}
		documents := map[string]bool{}
		for _, source := range q.Sources {
			count, known := pages[source.Document]
			if !known {
				fault("%s: the source document %q is not in the list of documents", place, source.Document)
				continue
			}
			if documents[source.Document] {
				fault("%s: the document %q is a source two times: put its pages in one source", place, source.Document)
			}
			documents[source.Document] = true
			if len(source.Pages) == 0 {
				fault("%s: the source %q has no pages", place, source.Document)
			}
			for _, page := range source.Pages {
				if page < 1 || page > count {
					fault("%s: page %d is not a page of %q, which has %s", place, page, source.Document, number(count, "page"))
				}
			}
		}
		if q.Kind == Summary && len(documents) < 2 {
			fault("%s: a question of the type summary needs sources in 2 or more documents", place)
		}
	}
	if len(s.Questions) == 0 {
		fault("the file has no questions: add the list \"questions\"")
	}
	return faults
}

// Count gives the number of questions of each kind.
func (s Set) Count() map[string]int {
	count := map[string]int{}
	for _, q := range s.Questions {
		count[q.Kind]++
	}
	return count
}

// ErrSize tells that the set does not have the size that the plan gives.
var ErrSize = errors.New("the test set does not have the size of the plan")

// Size examines the size that the plan gives for the set: 30 to 50
// questions, each kind in it, and 3 or more questions with no answer.
func (s Set) Size() error {
	count := s.Count()
	var faults []string
	if total := len(s.Questions); total < 30 || total > 50 {
		faults = append(faults, fmt.Sprintf("it has %s, and the plan gives 30 to 50", number(total, "question")))
	}
	for _, kind := range Kinds {
		least := 1
		if kind == NoAnswer {
			least = 3
		}
		if count[kind] < least {
			faults = append(faults, fmt.Sprintf("it has %s of the type %s, and it needs %d or more", number(count[kind], "question"), kind, least))
		}
	}
	if len(faults) > 0 {
		return fmt.Errorf("%w: %s: add or remove questions in the file", ErrSize, strings.Join(faults, "; "))
	}
	return nil
}
