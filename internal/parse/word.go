package parse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// WordConfig is the settings of the Word reader.
type WordConfig struct {
	// Path is the program office-oxide: the command line program of the
	// library office_oxide. scripts/install-word-reader installs it.
	Path string
	// Timeout is the longest time that the program gets for one file.
	Timeout time.Duration
	// TempDir is the folder for the copy of a file that the program
	// reads. "" is the folder of the operating system for such files.
	TempDir string
}

// Word reads Word files, .docx and .doc, with the program office-oxide. It
// is safe for use by many goroutines.
//
// The program runs as its own process for each file. The library has no
// time limit, and its guide says to read a file from an unknown source in
// a process of its own: the reader stops a process that takes too long,
// and a fault in the program does not stop the service.
type Word struct {
	cfg WordConfig
}

// NewWord makes a Word. It does not run the program.
func NewWord(cfg WordConfig) (*Word, error) {
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, errors.New("the Word reader has no program")
	}
	if cfg.Timeout <= 0 {
		return nil, fmt.Errorf("the Word reader has a time limit of %s: it must be more than zero", cfg.Timeout)
	}
	return &Word{cfg: cfg}, nil
}

// ContentError is a fault in the content of a file: a new attempt reads
// the same file and finds the same fault.
type ContentError struct {
	Reason string
}

func (e *ContentError) Error() string { return e.Reason }

// IsContentError tells if an error is a fault in the content of a file.
func IsContentError(err error) bool {
	_, ok := errors.AsType[*ContentError](err)
	return ok
}

// Name gives the name and the version of the program, such as
// "office-oxide 0.1.17".
func (w *Word) Name(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, w.cfg.Path, "--version").Output() //nolint:gosec // The path is a setting of the service.
	if err != nil {
		return "", fmt.Errorf("run the Word reader %s: %w", w.cfg.Path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Read gives the blocks of a Word file in their reading order. A block of
// a Word file has no box. The ending is that of the file name: .docx or
// .doc.
//
// The program reads a file from the disk only, so Read makes a copy of the
// content and removes it when the program ends. The program has the strict
// limits of the library for the size of a file and of its parts, and a
// file that it cannot read completely is a fault.
//
// The error is a ContentError when the fault is in the file: it is not a
// Word file, it has a password, it is damaged, it is larger than a limit,
// or the program does not complete it in the time limit.
func (w *Word) Read(ctx context.Context, content io.Reader, ending string) ([]Block, error) {
	path, err := w.copyToDisk(content, ending)
	if err != nil {
		return nil, err
	}
	defer os.Remove(path) //nolint:errcheck // The file is in the folder for files of a short life.

	runCtx, cancel := context.WithTimeout(ctx, w.cfg.Timeout)
	defer cancel()
	program := exec.CommandContext(runCtx, w.cfg.Path, "--strict", "ir", path) //nolint:gosec // The path is a setting of the service.
	var faults tail
	program.Stderr = &faults
	answer, err := program.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("run the Word reader: %w", err)
	}
	if err := program.Start(); err != nil {
		return nil, fmt.Errorf("run the Word reader %s: %w", w.cfg.Path, err)
	}
	blocks, readErr := readWordAnswer(answer)
	if readErr != nil {
		// The program can wait for a reader of its answer. Stop it.
		cancel()
	}
	waitErr := program.Wait()

	exit, exited := errors.AsType[*exec.ExitError](waitErr)
	switch {
	case ctx.Err() != nil:
		// The job stops, or the service does.
		return nil, fmt.Errorf("read the Word file: %w", context.Cause(ctx))
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return nil, &ContentError{Reason: fmt.Sprintf("the Word reader did not read the file in %s", w.cfg.Timeout)}
	case exited && exit.ExitCode() == 1:
		// The program found a fault in the file and gave the reason.
		return nil, &ContentError{Reason: "the Word reader cannot read the file: " + faults.reason()}
	case waitErr != nil:
		return nil, fmt.Errorf("the Word reader stopped: %w: %s", waitErr, faults.reason())
	case readErr != nil:
		return nil, fmt.Errorf("read the answer of the Word reader: %w", readErr)
	}
	return blocks, nil
}

// copyToDisk writes the content to a new file and gives its path.
func (w *Word) copyToDisk(content io.Reader, ending string) (string, error) {
	file, err := os.CreateTemp(w.cfg.TempDir, "rejea-word-*"+ending)
	if err != nil {
		return "", fmt.Errorf("make the copy of the Word file: %w", err)
	}
	_, err = io.Copy(file, content)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(file.Name()) //nolint:errcheck,gosec // The first error is the cause.
		return "", fmt.Errorf("make the copy of the Word file: %w", err)
	}
	return file.Name(), nil
}

// tail keeps the start of what the program writes about its faults.
type tail struct {
	text []byte
}

// tailBytes is the number of bytes of the fault text that tail keeps.
const tailBytes = 1024

func (t *tail) Write(p []byte) (int, error) {
	if room := tailBytes - len(t.text); room > 0 {
		t.text = append(t.text, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// reason gives the first line of the fault text, with no "error: " at its
// start.
func (t *tail) reason() string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(t.text)), "\n")
	line = strings.TrimPrefix(strings.ToValidUTF8(line, ""), "error: ")
	if line == "" {
		return "the program gave no reason"
	}
	return line
}

// wordElement is one element of the answer of the program: the document
// structure of office_oxide as JSON. An element is a piece of the
// document, such as a paragraph, or a piece of a line, such as a run of
// text. The struct has only the fields that the reader uses.
type wordElement struct {
	Type string `json:"type"`
	// Level is the level of a heading: 1 is the top.
	Level int `json:"level"`
	// Content is a list of elements, or the text of a code block.
	Content json.RawMessage `json:"content"`
	// Text is the text of a run.
	Text string `json:"text"`
	// Style is the name of the style of a paragraph.
	Style string `json:"style"`
	// Rows and Caption are those of a table.
	Rows []struct {
		Cells []struct {
			Content []wordElement `json:"content"`
			ColSpan int           `json:"col_span"`
			RowSpan int           `json:"row_span"`
		} `json:"cells"`
	} `json:"rows"`
	Caption string `json:"caption"`
	// Ordered, StartNumber and Items are those of a list.
	Ordered     bool `json:"ordered"`
	StartNumber *int `json:"start_number"`
	Items       []struct {
		Content []wordElement `json:"content"`
		Nested  *wordElement  `json:"nested"`
	} `json:"items"`
	// AltText and Decorative are those of a picture.
	AltText    string `json:"alt_text"`
	Decorative bool   `json:"decorative"`
	// Marker is "comment" for the note of a person who reviewed the
	// document.
	Marker string `json:"marker"`
}

// readWordAnswer reads the answer of the program: one JSON object with the
// sections of the document, each with its elements. It reads one element
// at a time, so the memory that it uses does not grow with the answer: the
// answer has each picture of the document in it.
func readWordAnswer(answer io.Reader) ([]Block, error) {
	decoder := json.NewDecoder(answer)
	var blocks []Block
	err := eachMember(decoder, func(key string) error {
		if key != "sections" {
			return skipValue(decoder)
		}
		return eachItem(decoder, func() error {
			// A section also has the text at the top and at the bottom of
			// each page. The reader leaves it out: it is not the content.
			return eachMember(decoder, func(key string) error {
				if key != "elements" {
					return skipValue(decoder)
				}
				return eachItem(decoder, func() error {
					var element wordElement
					if err := decoder.Decode(&element); err != nil {
						return err
					}
					blocks = appendWordBlocks(blocks, element)
					return nil
				})
			})
		})
	})
	if err != nil {
		return nil, err
	}
	return blocks, nil
}

// eachMember reads a JSON object. It calls value for each member, after
// the key of the member. value reads the value.
func eachMember(decoder *json.Decoder, value func(key string) error) error {
	if err := expectDelim(decoder, '{'); err != nil {
		return err
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("the answer has %v in the place of a key", token)
		}
		if err := value(key); err != nil {
			return err
		}
	}
	return expectDelim(decoder, '}')
}

// eachItem reads a JSON array. It calls item for each item, and item reads
// the item.
func eachItem(decoder *json.Decoder, item func() error) error {
	if err := expectDelim(decoder, '['); err != nil {
		return err
	}
	for decoder.More() {
		if err := item(); err != nil {
			return err
		}
	}
	return expectDelim(decoder, ']')
}

// expectDelim reads one token, which must be the given delimiter.
func expectDelim(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != want {
		return fmt.Errorf("the answer has %v in the place of %v", token, want)
	}
	return nil
}

// skipValue reads one JSON value and keeps nothing of it.
func skipValue(decoder *json.Decoder) error {
	depth := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
		if depth == 0 {
			return nil
		}
	}
}

// appendWordBlocks adds the blocks of one element of a document. A block
// with no text is left out: with no box, it has nothing to show.
func appendWordBlocks(blocks []Block, element wordElement) []Block {
	add := func(blockType Type, text string) {
		if text = strings.TrimSpace(text); text != "" {
			blocks = append(blocks, Block{Type: blockType, Text: text})
		}
	}
	switch element.Type {
	case "heading":
		if text := strings.TrimSpace(wordText(element)); text != "" {
			blocks = append(blocks, Block{Type: Heading, Text: text, Level: max(element.Level, 1)})
		}
	case "paragraph":
		switch strings.ToLower(element.Style) {
		case "title":
			add(Title, wordText(element))
		case "caption":
			add(Caption, wordText(element))
		default:
			add(Paragraph, wordText(element))
		}
	case "code_block":
		add(Paragraph, wordText(element))
	case "table":
		add(Caption, element.Caption)
		if table := wordTable(element); table != "" {
			blocks = append(blocks, Block{Type: Table, Text: table})
		}
	case "list":
		add(List, strings.Join(wordListLines(nil, element, 0), "\n"))
	case "image":
		if !element.Decorative {
			add(Figure, element.AltText)
		}
	case "text_box":
		for _, inner := range wordChildren(element) {
			blocks = appendWordBlocks(blocks, inner)
		}
	case "footnote", "endnote":
		// The note of a reviewer is not part of the document.
		if element.Marker != "comment" {
			add(Footnote, wordText(element))
		}
	}
	return blocks
}

// wordChildren gives the elements in the content of an element. The
// content of a code block is text, so it has no elements.
func wordChildren(element wordElement) []wordElement {
	var children []wordElement
	if json.Unmarshal(element.Content, &children) != nil {
		return nil
	}
	return children
}

// wordText gives the text of an element and of each element in it, with a
// new line between two pieces of the document.
func wordText(element wordElement) string {
	switch element.Type {
	case "text":
		return element.Text
	case "line_break":
		return "\n"
	case "code_block":
		var text string
		if json.Unmarshal(element.Content, &text) != nil {
			return ""
		}
		return text
	case "heading", "paragraph":
		// The content is the runs of the line.
		var text strings.Builder
		for _, run := range wordChildren(element) {
			text.WriteString(wordText(run))
		}
		return text.String()
	case "table":
		var rows []string
		for _, row := range element.Rows {
			var cells []string
			for _, cell := range row.Cells {
				cells = append(cells, wordTextOf(cell.Content))
			}
			rows = append(rows, strings.Join(cells, " "))
		}
		return strings.Join(rows, "\n")
	case "list":
		return strings.Join(wordListLines(nil, element, 0), "\n")
	case "image":
		return element.AltText
	}
	return wordTextOf(wordChildren(element))
}

// wordTextOf gives the text of a list of elements, one on each line.
func wordTextOf(elements []wordElement) string {
	var parts []string
	for _, element := range elements {
		if text := strings.TrimSpace(wordText(element)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// wordListLines adds one line for each item of a list: its number or a
// dash, then its text. The items of a list in a list are two spaces more
// to the right.
func wordListLines(lines []string, list wordElement, depth int) []string {
	number := 1
	if list.StartNumber != nil {
		number = *list.StartNumber
	}
	for _, item := range list.Items {
		marker := "- "
		if list.Ordered {
			marker = strconv.Itoa(number) + ". "
			number++
		}
		text := strings.ReplaceAll(wordTextOf(item.Content), "\n", " ")
		lines = append(lines, strings.Repeat("  ", depth)+marker+text)
		if item.Nested != nil {
			lines = wordListLines(lines, *item.Nested, depth+1)
		}
	}
	return lines
}

// wordTable gives a table as HTML, in the form that the PaddleOCR adapter
// gives: table, tr and td elements, and br for a new line in a cell.
func wordTable(table wordElement) string {
	if len(table.Rows) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("<table>")
	for _, row := range table.Rows {
		out.WriteString("<tr>")
		for _, cell := range row.Cells {
			out.WriteString("<td")
			if cell.ColSpan > 1 {
				out.WriteString(` colspan="` + strconv.Itoa(cell.ColSpan) + `"`)
			}
			if cell.RowSpan > 1 {
				out.WriteString(` rowspan="` + strconv.Itoa(cell.RowSpan) + `"`)
			}
			out.WriteString(">")
			out.WriteString(strings.ReplaceAll(html.EscapeString(wordTextOf(cell.Content)), "\n", "<br>"))
			out.WriteString("</td>")
		}
		out.WriteString("</tr>")
	}
	out.WriteString("</table>")
	return out.String()
}
