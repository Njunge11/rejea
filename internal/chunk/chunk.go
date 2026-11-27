// Package chunk cuts the blocks of a document into chunks: the pieces of
// text that the search finds and that the answering model reads.
//
// The method is that of the HybridChunker of Docling, with its option to
// keep each heading:
//
//  1. Each block gets the path of the headings above it. A title or a
//     heading starts a new section.
//  2. A block that is larger than the limit is split: a table at its rows,
//     with its header rows at the top of each part, and other text at its
//     paragraphs, lines and words.
//  3. Blocks that are next to each other in one section are put together,
//     as many as the limit permits.
//
// The limit is in tokens of the embedding model, and it is for the full
// text of a chunk: the heading path and the body.
package chunk

import (
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/Njunge11/rejea/internal/parse"
)

// Block is one block of a document, as the parser, the Word reader or the
// Markdown reader gave it. The blocks of a document are in the order in which a person reads
// them.
type Block struct {
	ID uuid.UUID
	// Page is the number of the page of the block. It is 0 for a Word file
	// and for a Markdown file, which have no pages.
	Page int
	Type parse.Type
	// Level is the level of a heading: 1 is the top. With 0, the heading
	// gets the level 1.
	Level int
	Text  string
}

// Chunk is one piece of a document.
type Chunk struct {
	// Headings is the path of the headings above the body: the title of
	// the document first, the nearest heading last.
	Headings []string
	// Body is the text of the blocks, with an empty line between two
	// blocks. It is empty for a heading that has no text below it.
	Body string
	// Blocks is the blocks of the body, in their order. For a heading with
	// no text below it, it is the block of that heading.
	Blocks []uuid.UUID
	// PageStart and PageEnd are the first and the last page of the blocks.
	// They are 0 for a Word file and for a Markdown file.
	PageStart, PageEnd int
}

// Text gives the full text of the chunk: the heading path on the first
// line, with " > " between two headings, then an empty line, then the body.
// It is the text that the limit is for.
func (c Chunk) Text() string {
	return text(c.Headings, c.Body)
}

func text(headings []string, body string) string {
	path := strings.Join(headings, " > ")
	if path == "" || body == "" {
		return path + body
	}
	return path + between + body
}

// between is the text between two blocks of a chunk, and between the
// heading path and the body: an empty line.
const between = "\n\n"

// Counter counts the tokens of a text, as the embedding model counts them.
type Counter interface {
	Count(text string) (int, error)
}

// Config is the settings of a Chunker.
type Config struct {
	// MaxTokens is the largest number of tokens of the text of one chunk.
	MaxTokens int
}

// Chunker cuts the blocks of a document into chunks.
type Chunker struct {
	counter Counter
	limit   int
}

// New makes a Chunker.
func New(counter Counter, cfg Config) (*Chunker, error) {
	if cfg.MaxTokens < 1 {
		return nil, errors.New("the largest number of tokens of a chunk must be 1 or more")
	}
	return &Chunker{counter: counter, limit: cfg.MaxTokens}, nil
}

// Split gives the chunks of a document, in the order of its blocks. The
// text at the top and at the bottom of each page (header, footer and page
// number) is in no chunk. Each other block with text is in a chunk.
func (c *Chunker) Split(blocks []Block) ([]Chunk, error) {
	var pieces []piece
	// The heading paths that are too large to be at the top of a chunk.
	// Each is text of its own one time: thus its words are in a chunk.
	tooLarge := map[string]bool{}
	for _, section := range sections(blocks) {
		parts, err := c.fit(section)
		if err != nil {
			return nil, err
		}
		path := text(section.headings, "")
		if path != "" && len(parts) > 0 && len(parts[0].headings) == 0 && !tooLarge[path] {
			tooLarge[path] = true
			if section.body != "" {
				alone, err := c.fit(piece{body: path, blocks: section.blocks})
				if err != nil {
					return nil, err
				}
				pieces = append(pieces, alone...)
			}
		}
		pieces = append(pieces, parts...)
	}
	pieces, err := c.join(pieces)
	if err != nil {
		return nil, err
	}

	chunks := make([]Chunk, 0, len(pieces))
	for _, piece := range pieces {
		chunk := Chunk{Headings: piece.headings, Body: piece.body}
		for _, block := range piece.blocks {
			if n := len(chunk.Blocks); n > 0 && chunk.Blocks[n-1] == block.ID {
				continue // two parts of one block
			}
			chunk.Blocks = append(chunk.Blocks, block.ID)
			if chunk.PageStart == 0 || block.Page < chunk.PageStart {
				chunk.PageStart = block.Page
			}
			chunk.PageEnd = max(chunk.PageEnd, block.Page)
		}
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

// piece is a chunk in the making: one block or a part of it, and later the
// blocks that are put together.
type piece struct {
	headings []string
	body     string
	blocks   []Block
}

// heading is one heading of the path of the current section.
type heading struct {
	level int
	text  string
	block Block
	// used is true when a piece has this heading as the last of its path.
	used bool
}

// sections gives one piece for each block with body text, with the path of
// the headings above it. A heading with no text below it, and with no
// heading of a lower level below it, gets a piece of its own: thus the
// words of each heading are in a chunk.
func sections(blocks []Block) []piece {
	var (
		pieces []piece
		path   []heading // from the title down, each level one time
	)
	headings := func() []string {
		texts := make([]string, len(path))
		for i, h := range path {
			texts[i] = h.text
		}
		return texts
	}
	// alone adds the piece of the last heading of the path, if no piece
	// has it.
	alone := func() {
		if n := len(path); n > 0 && !path[n-1].used {
			pieces = append(pieces, piece{headings: headings(), blocks: []Block{path[n-1].block}})
			path[n-1].used = true
		}
	}

	for _, block := range blocks {
		switch block.Type {
		case parse.Header, parse.Footer, parse.PageNumber:
			continue
		case parse.Title, parse.Heading:
			// A heading is one line in the path.
			words := strings.Join(strings.Fields(block.Text), " ")
			if words == "" {
				continue
			}
			level := 0 // a title
			if block.Type == parse.Heading {
				level = max(block.Level, 1)
			}
			// The new heading ends each section of its level and of the
			// levels below.
			keep := 0
			for keep < len(path) && path[keep].level < level {
				keep++
			}
			if keep < len(path) {
				alone()
			}
			path = append(path[:keep], heading{level: level, text: words, block: block})
		default:
			body := strings.TrimSpace(block.Text)
			if body == "" {
				continue
			}
			pieces = append(pieces, piece{headings: headings(), body: body, blocks: []Block{block}})
			if n := len(path); n > 0 {
				path[n-1].used = true
			}
		}
	}
	alone()
	return pieces
}

// join puts the pieces that are next to each other and have the same
// heading path together, as many as the limit permits.
func (c *Chunker) join(pieces []piece) ([]piece, error) {
	var joined []piece
	for start := 0; start < len(pieces); {
		current := pieces[start]
		end := start + 1
		for ; end < len(pieces) && slices.Equal(pieces[end].headings, current.headings); end++ {
			candidate := piece{
				headings: current.headings,
				body:     current.body,
				blocks:   append(slices.Clone(current.blocks), pieces[end].blocks...),
			}
			switch {
			case candidate.body == "":
				candidate.body = pieces[end].body
			case pieces[end].body != "":
				candidate.body += between + pieces[end].body
			}
			tokens, err := c.counter.Count(text(candidate.headings, candidate.body))
			if err != nil {
				return nil, err
			}
			if tokens > c.limit {
				break
			}
			current = candidate
		}
		joined = append(joined, current)
		start = end
	}
	return joined, nil
}
