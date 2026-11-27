package parse

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"runtime/debug"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"go.yaml.in/yaml/v4"
	"golang.org/x/net/html"
)

// MarkdownConfig is the settings of the Markdown reader.
type MarkdownConfig struct {
	// MaxBytes is the largest Markdown file that the reader reads. The
	// file is in memory as a whole.
	MaxBytes int64
}

// Markdown reads Markdown files with the library goldmark. It is safe for
// use by many goroutines.
//
// The reader uses the method of Unstructured, which Docling also uses for
// a file with HTML in it: the library turns the Markdown into HTML, and
// one HTML reader gives the blocks. Thus a table, a list or a heading that
// a file has as HTML gives the same block as one in Markdown.
//
// The Markdown is CommonMark with the additions of GitHub (tables, struck
// text, task lists, links with no brackets) and footnotes.
type Markdown struct {
	cfg    MarkdownConfig
	engine goldmark.Markdown
	name   string
}

// goldmarkModule is the module of the library in the build of the service.
const goldmarkModule = "github.com/yuin/goldmark"

// NewMarkdown makes a Markdown.
func NewMarkdown(cfg MarkdownConfig) (*Markdown, error) {
	if cfg.MaxBytes < 1 {
		return nil, fmt.Errorf("the Markdown reader has a size limit of %d bytes: it must be 1 or more", cfg.MaxBytes)
	}
	name := "goldmark"
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, module := range build.Deps {
			if module.Path == goldmarkModule {
				name += " " + module.Version
			}
		}
	}
	engine := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.Footnote),
		// The HTML of the file stays in the HTML that the library gives.
		// No browser gets this HTML: the reader takes the text out of it.
		goldmark.WithRendererOptions(goldmarkhtml.WithUnsafe()),
	)
	return &Markdown{cfg: cfg, engine: engine, name: name}, nil
}

// Name gives the name and the version of the library, such as
// "goldmark v1.8.7".
func (m *Markdown) Name() string { return m.name }

// Read gives the blocks of a Markdown file in their reading order. A block
// of a Markdown file has no box. The metadata block at the start of a file
// (YAML between two lines of three dashes) is not content: the reader
// leaves it out.
//
// The error is a ContentError when the fault is in the file: it is larger
// than the limit, or it is not text in UTF-8.
func (m *Markdown) Read(content io.Reader) ([]Block, error) {
	source, err := io.ReadAll(io.LimitReader(content, m.cfg.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read the Markdown file: %w", err)
	}
	if int64(len(source)) > m.cfg.MaxBytes {
		return nil, &ContentError{Reason: fmt.Sprintf(
			"the Markdown file has more than %d bytes, the limit for a Markdown file: divide it into smaller files", m.cfg.MaxBytes)}
	}
	source, err = markdownText(source)
	if err != nil {
		return nil, err
	}
	var page bytes.Buffer
	if err := m.engine.Convert(withoutFrontMatter(source), &page); err != nil {
		return nil, fmt.Errorf("read the Markdown file: %w", err)
	}
	root, err := html.Parse(&page)
	if err != nil {
		return nil, fmt.Errorf("read the HTML of the Markdown file: %w", err)
	}
	return htmlBlocks(root), nil
}

// The marks that a text file can have at its start to tell its encoding.
var (
	markUTF8    = []byte{0xEF, 0xBB, 0xBF}
	markUTF16LE = []byte{0xFF, 0xFE}
	markUTF16BE = []byte{0xFE, 0xFF}
)

// markdownText gives the text of a Markdown file with no encoding mark. It
// refuses a file that is not text in UTF-8: the reader does not guess the
// encoding of a file, because a wrong guess gives wrong words.
func markdownText(source []byte) ([]byte, error) {
	switch {
	case bytes.HasPrefix(source, markUTF16LE), bytes.HasPrefix(source, markUTF16BE):
		return nil, &ContentError{Reason: "the Markdown file is text in UTF-16: save it as UTF-8"}
	case bytes.IndexByte(source, 0) >= 0:
		return nil, &ContentError{Reason: "the Markdown file has a NUL character: it is not a text file"}
	case !utf8.Valid(source):
		return nil, &ContentError{Reason: "the Markdown file is not text in UTF-8: save it as UTF-8"}
	}
	return bytes.TrimPrefix(source, markUTF8), nil
}

// frontMatter finds the metadata block at the start of a Markdown file: a
// line of three dashes, the metadata, and a line of three dashes or three
// dots. It is the pattern of Docling.
var frontMatter = regexp.MustCompile(`(?s)\A---[ \t]*\r?\n(.*?)(?m:^)(?:---|\.\.\.)[ \t]*(?:\r?\n|\z)`)

// blankLine finds an empty line at the start of a text.
var blankLine = regexp.MustCompile(`\A[ \t]*\r?\n`)

// withoutFrontMatter gives the text of a Markdown file with no metadata
// block. The block is metadata only when it is a YAML mapping: a file can
// also start with a line of three dashes that is a rule across the page.
func withoutFrontMatter(source []byte) []byte {
	found := frontMatter.FindSubmatchIndex(source)
	if found == nil {
		return source
	}
	metadata := source[found[2]:found[3]]
	if blankLine.Match(metadata) {
		return source
	}
	var value any
	if yaml.Unmarshal(metadata, &value) != nil || reflect.ValueOf(value).Kind() != reflect.Map {
		return source
	}
	return source[found[1]:]
}
