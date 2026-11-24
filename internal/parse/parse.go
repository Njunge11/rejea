// Package parse turns the content of a document into blocks: the pieces of
// the document, each with its type and its text.
//
// A PDF is read from the images of its pages, and each block has its place
// on the page. Parser is the port for that: the service knows a parser
// only through it. An adapter makes one parser fit the port. PaddleOCR is
// the adapter for the parser service of this repository (make parser).
//
// A Word file is read from the file itself, by Word. Its blocks have no
// place on a page.
package parse

import "context"

// Parser reads page images.
type Parser interface {
	// Parse reads the image of one page, a PNG file, and gives the blocks
	// of the page in the order in which a person reads them.
	Parse(ctx context.Context, image []byte) ([]Block, error)
	// Name gives the name and the version of the parser. The service
	// records it on each document that the parser reads, so a person can
	// find the documents that an old parser read.
	Name() string
}

// Block is one piece of a page, or of a Word file.
type Block struct {
	Type Type
	// Level is the level of a heading: 1 is the top. It is 0 when the
	// block is not a heading, and when the parser gives no level.
	Level int
	// Text is the content. A table is HTML: table, tr and td elements, and
	// br for a new line in a cell. A chart is the values that the parser
	// read from it, one line for each row, with " | " between the columns.
	// A picture is the text that the parser read in it.
	Text string
	// Box is the place of the block on the page. A block of a Word file
	// has no box.
	Box Box
}

// Box is a rectangle on a page. Each value is a fraction of the page, from
// 0 to 1: Left and Right of its width, Top and Bottom of its height. The
// start is the top left corner of the page.
type Box struct {
	Left, Top, Right, Bottom float64
}

// Type is the type of a block. Each parser has its own names for the
// pieces of a page, and its adapter turns them into these types.
type Type string

// The types of a block.
const (
	// Title is the title of the document.
	Title Type = "title"
	// Heading is the title of a section.
	Heading Type = "heading"
	// Paragraph is body text.
	Paragraph Type = "paragraph"
	// Table is a table.
	Table Type = "table"
	// List is a list: one line for each item.
	List Type = "list"
	// Figure is a picture, a chart or a seal.
	Figure Type = "figure"
	// Caption is the title of a figure or of a table.
	Caption Type = "caption"
	// Formula is a mathematical formula.
	Formula Type = "formula"
	// Footnote is a note at the foot of a page, or below a figure or a
	// table.
	Footnote Type = "footnote"
	// Header and Footer are the text and the pictures that a document has
	// at the top and at the bottom of each page.
	Header Type = "header"
	Footer Type = "footer"
	// PageNumber is the number of the page, as the page shows it.
	PageNumber Type = "page_number"
	// Aside is text in the margin of a page.
	Aside Type = "aside"
)
