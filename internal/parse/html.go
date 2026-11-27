package parse

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// htmlBlocks gives the blocks of an HTML document in their reading order.
// A block has no box.
//
//   - A heading gives a heading with its level: 1 for h1.
//   - A list gives one block, with one line for each item.
//   - A table gives its HTML with table, tr, th and td elements only.
//   - Text that is in no such element gives a paragraph.
func htmlBlocks(root *html.Node) []Block {
	var reader htmlReader
	reader.walk(root)
	reader.flush()
	return reader.blocks
}

// htmlReader collects the blocks of an HTML document.
type htmlReader struct {
	blocks []Block
	// line is the text that is in no block yet: the text between two
	// block elements.
	line strings.Builder
}

// add adds a block. A block with no text is left out: with no box, it has
// nothing to show.
func (r *htmlReader) add(blockType Type, text string) {
	if text = strings.TrimSpace(text); text != "" {
		r.blocks = append(r.blocks, Block{Type: blockType, Text: text})
	}
}

// flush makes a paragraph of the text that is in no block yet.
func (r *htmlReader) flush() {
	r.add(Paragraph, tidy(r.line.String()))
	r.line.Reset()
}

// The classes that goldmark gives to the parts of a footnote.
const (
	footnotesClass    = "footnotes"
	footnoteLinkClass = "footnote-ref"
	footnoteBackClass = "footnote-backref"
)

// walk reads the elements in a node.
func (r *htmlReader) walk(parent *html.Node) {
	for node := parent.FirstChild; node != nil; node = node.NextSibling {
		if node.Type != html.ElementNode || !blockElements[node.DataAtom] {
			// Text, or an element in a line of text.
			writeText(&r.line, node)
			continue
		}
		r.flush()
		switch node.DataAtom {
		case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
			if text := oneLine(textOf(node)); text != "" {
				// The names of the six elements are next to each other
				// in their order: h1 to h6.
				r.blocks = append(r.blocks, Block{Type: Heading, Text: text, Level: int(node.Data[1] - '0')})
			}
		case atom.P:
			if pictures, alone := picturesOnly(node); alone {
				r.add(Figure, pictures)
			} else {
				r.add(Paragraph, textOf(node))
			}
		case atom.Ul, atom.Ol:
			r.add(List, strings.Join(listLines(nil, node, 0), "\n"))
		case atom.Table:
			r.table(node)
		case atom.Pre:
			r.add(Paragraph, strings.Trim(rawText(node), "\r\n"))
		case atom.Figcaption, atom.Caption:
			r.add(Caption, textOf(node))
		case atom.Hr:
			// A rule across the page has no text.
		case atom.Div:
			if hasClass(node, footnotesClass) {
				r.footnotes(node)
			} else {
				r.walk(node)
			}
		default:
			// An element that holds blocks, such as a quote or a section.
			r.walk(node)
		}
		r.flush()
	}
}

// table adds the block of a table, and the block of its caption before it.
func (r *htmlReader) table(table *html.Node) {
	var (
		out   strings.Builder
		words bool
	)
	out.WriteString("<table>")
	var rows func(parent *html.Node, head bool)
	rows = func(parent *html.Node, head bool) {
		for node := parent.FirstChild; node != nil; node = node.NextSibling {
			switch node.DataAtom {
			case atom.Caption:
				r.add(Caption, textOf(node))
			case atom.Thead:
				rows(node, true)
			case atom.Tbody, atom.Tfoot:
				rows(node, false)
			case atom.Tr:
				out.WriteString("<tr>")
				for cell := node.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.DataAtom != atom.Td && cell.DataAtom != atom.Th {
						continue
					}
					name := "td"
					if head || cell.DataAtom == atom.Th {
						name = "th"
					}
					out.WriteString("<" + name)
					for _, span := range []string{"colspan", "rowspan"} {
						if n, err := strconv.Atoi(strings.TrimSpace(attribute(cell, span))); err == nil && n > 1 {
							out.WriteString(" " + span + `="` + strconv.Itoa(n) + `"`)
						}
					}
					text := textOf(cell)
					words = words || text != ""
					out.WriteString(">" + strings.ReplaceAll(html.EscapeString(text), "\n", "<br>") + "</" + name + ">")
				}
				out.WriteString("</tr>")
			}
		}
	}
	rows(table, false)
	out.WriteString("</table>")
	if words {
		r.blocks = append(r.blocks, Block{Type: Table, Text: out.String()})
	}
}

// footnotes adds one block for each footnote of the list that goldmark
// puts at the end of a document.
func (r *htmlReader) footnotes(section *html.Node) {
	for list := section.FirstChild; list != nil; list = list.NextSibling {
		if list.DataAtom != atom.Ol {
			continue
		}
		for item := list.FirstChild; item != nil; item = item.NextSibling {
			if item.DataAtom == atom.Li {
				r.add(Footnote, textOf(item))
			}
		}
	}
}

// blockElements is the elements that start a new line in a browser: the
// "flow content" of the HTML standard that is not in a line of text. The
// text before and after such an element is not in the same paragraph.
var blockElements = map[atom.Atom]bool{
	atom.Address: true, atom.Article: true, atom.Aside: true, atom.Blockquote: true,
	atom.Body: true, atom.Caption: true, atom.Center: true, atom.Dd: true,
	atom.Details: true, atom.Dialog: true, atom.Div: true, atom.Dl: true,
	atom.Dt: true, atom.Fieldset: true, atom.Figcaption: true, atom.Figure: true,
	atom.Footer: true, atom.Form: true, atom.H1: true, atom.H2: true,
	atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
	atom.Header: true, atom.Hgroup: true, atom.Hr: true, atom.Html: true,
	atom.Li: true, atom.Main: true, atom.Menu: true,
	atom.Nav: true, atom.Ol: true, atom.P: true, atom.Pre: true,
	atom.Search: true, atom.Section: true, atom.Summary: true, atom.Table: true,
	atom.Tbody: true, atom.Td: true, atom.Tfoot: true, atom.Th: true,
	atom.Thead: true, atom.Tr: true, atom.Ul: true,
}

// noTextElements is the elements whose content is not text of the
// document.
var noTextElements = map[atom.Atom]bool{
	atom.Head: true, atom.Script: true, atom.Style: true, atom.Template: true,
	atom.Noscript: true, atom.Svg: true, atom.Math: true, atom.Iframe: true,
	atom.Object: true, atom.Select: true, atom.Textarea: true,
}

// writeText adds the text of a node and of each node in it to a line of
// text. A new line in the source is a space, as in a browser. A br
// element is a new line. A picture gives the text that describes it.
func writeText(line *strings.Builder, node *html.Node) {
	switch node.Type {
	case html.TextNode:
		line.WriteString(strings.NewReplacer("\n", " ", "\r", " ").Replace(node.Data))
		return
	case html.ElementNode, html.DocumentNode:
	default:
		return // a comment, or the type of the document
	}
	switch {
	case noTextElements[node.DataAtom]:
		return
	case node.DataAtom == atom.Br:
		line.WriteString("\n")
		return
	case node.DataAtom == atom.Img:
		line.WriteString(attribute(node, "alt"))
		return
	case node.DataAtom == atom.Input:
		// goldmark gives the box of a task in a list as a checkbox.
		if attribute(node, "type") == "checkbox" {
			if hasAttribute(node, "checked") {
				line.WriteString("[x] ")
			} else {
				line.WriteString("[ ] ")
			}
		}
		return
	case node.DataAtom == atom.A && hasClass(node, footnoteBackClass):
		// The link from a footnote back to its place in the text.
		return
	case node.DataAtom == atom.A && hasClass(node, footnoteLinkClass):
		// The number of a footnote in the text. With no brackets, it
		// reads as a part of the word or of the number before it.
		line.WriteString("[" + oneLine(textOfChildren(node)) + "]")
		return
	}
	block := blockElements[node.DataAtom]
	if block {
		line.WriteString("\n")
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		writeText(line, child)
	}
	if block {
		line.WriteString("\n")
	}
}

// textOf gives the text of an element: each line with one space between
// two words, and no empty line.
func textOf(node *html.Node) string {
	return tidy(textOfChildren(node))
}

func textOfChildren(node *html.Node) string {
	var line strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		writeText(&line, child)
	}
	return line.String()
}

// tidy gives a text with one space between two words of a line, and with
// no empty line.
func tidy(text string) string {
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		if words := strings.Join(strings.Fields(line), " "); words != "" {
			lines = append(lines, words)
		}
	}
	return strings.Join(lines, "\n")
}

// oneLine gives a text as one line.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// rawText gives the text of an element as it is in the source: the text of
// a pre element, where each space and each new line counts.
func rawText(node *html.Node) string {
	var text strings.Builder
	var read func(*html.Node)
	read = func(n *html.Node) {
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			switch {
			case child.Type == html.TextNode:
				text.WriteString(child.Data)
			case child.DataAtom == atom.Br:
				text.WriteString("\n")
			case child.Type == html.ElementNode && !noTextElements[child.DataAtom]:
				read(child)
			}
		}
	}
	read(node)
	return text.String()
}

// picturesOnly tells if an element has pictures and no text of its own. It
// gives the text that describes the pictures.
func picturesOnly(node *html.Node) (string, bool) {
	var (
		described []string
		pictures  int
		text      bool
	)
	var read func(*html.Node)
	read = func(n *html.Node) {
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			switch {
			case child.Type == html.TextNode:
				text = text || strings.TrimSpace(child.Data) != ""
			case child.DataAtom == atom.Img:
				pictures++
				if alt := oneLine(attribute(child, "alt")); alt != "" {
					described = append(described, alt)
				}
			case child.Type == html.ElementNode:
				read(child)
			}
		}
	}
	read(node)
	return strings.Join(described, "\n"), pictures > 0 && !text
}

// listLines adds one line for each item of a list: its number or a dash,
// then its text. The items of a list in a list are two spaces more to the
// right. It is the form that the Word reader gives.
func listLines(lines []string, list *html.Node, depth int) []string {
	number := 1
	if start, err := strconv.Atoi(strings.TrimSpace(attribute(list, "start"))); err == nil {
		number = start
	}
	for item := list.FirstChild; item != nil; item = item.NextSibling {
		if item.DataAtom != atom.Li {
			continue
		}
		marker := "- "
		if list.DataAtom == atom.Ol {
			marker = strconv.Itoa(number) + ". "
			number++
		}
		// The text of the item, with no list that is in the item.
		var text strings.Builder
		var inner []*html.Node
		for child := item.FirstChild; child != nil; child = child.NextSibling {
			if child.DataAtom == atom.Ul || child.DataAtom == atom.Ol {
				inner = append(inner, child)
				continue
			}
			writeText(&text, child)
		}
		lines = append(lines, strings.Repeat("  ", depth)+marker+oneLine(text.String()))
		for _, list := range inner {
			lines = listLines(lines, list, depth+1)
		}
	}
	return lines
}

// attribute gives the value of an attribute of an element, or "".
func attribute(node *html.Node, name string) string {
	for _, a := range node.Attr {
		if a.Namespace == "" && a.Key == name {
			return a.Val
		}
	}
	return ""
}

func hasAttribute(node *html.Node, name string) bool {
	for _, a := range node.Attr {
		if a.Namespace == "" && a.Key == name {
			return true
		}
	}
	return false
}

// hasClass tells if an element has a class.
func hasClass(node *html.Node, class string) bool {
	for name := range strings.FieldsSeq(attribute(node, "class")) {
		if name == class {
			return true
		}
	}
	return false
}
