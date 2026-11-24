package chunk

import (
	"github.com/tmc/langchaingo/textsplitter"

	"github.com/Njunge11/rejea/internal/parse"
)

// fit gives the piece as it is when its text is in the limit. If not, it
// splits the body into parts, and each part is a piece with the same
// heading path and the same block. When the heading path leaves no room for
// the body, the parts have no heading path.
func (c *Chunker) fit(p piece) ([]piece, error) {
	total, err := c.counter.Count(text(p.headings, p.body))
	if err != nil {
		return nil, err
	}
	if total <= c.limit {
		return []piece{p}, nil
	}
	if p.body == "" {
		// A heading path that is larger than the limit is split as text.
		p = piece{body: text(p.headings, ""), blocks: p.blocks}
	}

	bodyTokens, err := c.counter.Count(p.body)
	if err != nil {
		return nil, err
	}
	// The tokens that a part can have: the limit less the heading path.
	// The count of a text is not the sum of the counts of its parts, so a
	// part can be some tokens over the limit with its heading path. Then
	// the parts are made again, smaller.
	var bodies []string
	for available := c.limit - (total - bodyTokens); available > 0; {
		if bodies, err = c.split(p, available); err != nil {
			return nil, err
		}
		over := 0
		for _, body := range bodies {
			tokens, err := c.counter.Count(text(p.headings, body))
			if err != nil {
				return nil, err
			}
			over = max(over, tokens-c.limit)
		}
		if over == 0 {
			break
		}
		available -= over
		bodies = nil
	}
	if bodies == nil && len(p.headings) > 0 {
		// The heading path leaves no room for the body: the parts get no
		// path.
		return c.fit(piece{body: p.body, blocks: p.blocks})
	}
	if bodies == nil {
		// One character is larger than the limit. No smaller part is
		// possible.
		if bodies, err = c.split(p, 1); err != nil {
			return nil, err
		}
	}
	parts := make([]piece, len(bodies))
	for i, body := range bodies {
		parts[i] = piece{headings: p.headings, body: body, blocks: p.blocks}
	}
	return parts, nil
}

// split cuts the body of a piece into parts of the given number of tokens
// or less.
func (c *Chunker) split(p piece, tokens int) ([]string, error) {
	if len(p.blocks) == 1 && p.blocks[0].Type == parse.Table {
		if rows := tableRows(p.body); len(rows) > 0 {
			return c.splitTable(rows, tokens)
		}
	}
	return c.splitText(p.body, tokens)
}

// splitText cuts a text at its empty lines. A part that is too large is
// cut at its lines, then at its spaces, then between its characters. It is
// the recursive character splitter of LangChain.
func (c *Chunker) splitText(body string, tokens int) ([]string, error) {
	var failed error
	splitter := textsplitter.NewRecursiveCharacter(
		textsplitter.WithChunkSize(tokens),
		textsplitter.WithChunkOverlap(0),
		textsplitter.WithLenFunc(func(part string) int {
			n, err := c.counter.Count(part)
			if err != nil && failed == nil {
				failed = err
			}
			return n
		}),
	)
	parts, err := splitter.SplitText(body)
	if err != nil {
		return nil, err
	}
	return parts, failed
}
