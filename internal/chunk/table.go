package chunk

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// splitTable cuts a table into tables of the given number of tokens or
// less. The method is that of the table chunker of Unstructured:
//
//   - Each part starts with the header rows of the table.
//   - Rows that a cell of more than one row holds together stay in one
//     part.
//   - When such rows are too large for one part, they go one by one, and
//     each gets a copy of the cell that covers it.
//   - A row that is too large for a part with the header rows is a part
//     with no header rows. A row that is too large for a part is split as
//     text.
func (c *Chunker) splitTable(rows []tableRow, tokens int) ([]string, error) {
	groups := rowGroups(rows)
	header := ""
	if len(groups) > 1 {
		first := rowsHTML(groups[0])
		n, err := c.counter.Count(table(first))
		if err != nil {
			return nil, err
		}
		// Header rows that leave no room for a row are rows like the
		// others.
		if n < tokens {
			header, groups = first, groups[1:]
		}
	}

	var (
		parts   []string
		current string // the rows of the part in the making, with no header
		shown   bool   // a part has the header rows
	)
	fits := func(rows string) (bool, error) {
		n, err := c.counter.Count(table(rows))
		return n <= tokens, err
	}
	flush := func() {
		if current != "" {
			parts = append(parts, table(header+current))
			current, shown = "", true
		}
	}
	// add puts the rows in the part in the making, or in a new part. It
	// gives false when the rows are too large for a part with the header.
	add := func(rows string) (bool, error) {
		ok, err := fits(header + current + rows)
		if err != nil {
			return false, err
		}
		if ok {
			current += rows
			return true, nil
		}
		flush()
		if ok, err = fits(header + rows); err != nil {
			return false, err
		}
		if ok {
			current = rows
		}
		return ok, nil
	}
	// alone adds parts that have no header rows. The header rows are a
	// part of their own before them, if no part has them: thus the words
	// of the header rows are in a part.
	alone := func(without ...string) {
		if header != "" && !shown {
			parts = append(parts, table(header))
			shown = true
		}
		parts = append(parts, without...)
	}

	for _, group := range groups {
		ok, err := add(rowsHTML(group))
		if err != nil {
			return nil, err
		}
		if ok {
			continue
		}
		// The group is too large for one part: its rows go one by one.
		for _, row := range withoutSpans(group) {
			if ok, err = add(row.html); err != nil {
				return nil, err
			}
			if ok {
				continue
			}
			// The row is too large for a part with the header.
			if ok, err = fits(row.html); err != nil {
				return nil, err
			}
			if ok {
				alone(table(row.html))
				continue
			}
			pieces, err := c.splitText(row.text(), tokens)
			if err != nil {
				return nil, err
			}
			alone(pieces...)
		}
	}
	flush()
	return parts, nil
}

func table(rows string) string {
	return "<table>" + rows + "</table>"
}

// tableRow is one row of a table.
type tableRow struct {
	// html is the row: from <tr> to </tr>.
	html  string
	cells []tableCell
	// header is true when the row is in the head of the table, or each of
	// its cells is a header cell.
	header bool
}

// span gives the largest number of rows that a cell of the row covers.
func (r tableRow) span() int {
	span := 1
	for _, cell := range r.cells {
		span = max(span, cell.rows)
	}
	return span
}

// text gives the text of the row with no HTML: " | " between two cells.
func (r tableRow) text() string {
	texts := make([]string, len(r.cells))
	for i, cell := range r.cells {
		texts[i] = cell.text
	}
	return strings.Join(texts, " | ")
}

// tableCell is one cell of a row.
type tableCell struct {
	// start is the start tag as it is in the table. alone is the start tag
	// of a cell that covers one row: with no rowspan.
	start, alone string
	// rest is the content and the end tag.
	rest string
	// rows and cols are the rows and the columns that the cell covers.
	rows, cols int
	// text is the content with no HTML.
	text string
}

// tableRows gives the rows of a table. A table in a cell is a part of that
// cell. With no row, the text is not a table that this package can split.
func tableRows(source string) []tableRow {
	var (
		rows    []tableRow
		row     *tableRow
		cell    *tableCell
		name    string // td or th: the tag of the open cell
		content strings.Builder
		words   strings.Builder
		depth   int  // the tables that are open
		head    bool // in the head of the table
	)
	closeCell := func() {
		if cell == nil {
			return
		}
		cell.rest = content.String() + "</" + name + ">"
		cell.text = strings.TrimSpace(words.String())
		row.cells = append(row.cells, *cell)
		cell = nil
		content.Reset()
		words.Reset()
	}
	closeRow := func() {
		if row == nil {
			return
		}
		closeCell()
		var all strings.Builder
		all.WriteString("<tr>")
		headers := 0
		for _, done := range row.cells {
			all.WriteString(done.start + done.rest)
			if strings.HasPrefix(done.alone, "<th") {
				headers++
			}
		}
		all.WriteString("</tr>")
		row.html = all.String()
		row.header = head || (headers > 0 && headers == len(row.cells))
		rows = append(rows, *row)
		row = nil
	}
	// inside adds a token to the content of the open cell.
	inside := func(raw string, kind html.TokenType, tag atom.Atom, tokenizer *html.Tokenizer) {
		if cell == nil {
			return
		}
		content.WriteString(raw)
		switch {
		case kind == html.TextToken:
			words.Write(tokenizer.Text())
		case tag == atom.Br:
			words.WriteString("\n")
		case tag == atom.Td || tag == atom.Th || tag == atom.Tr:
			words.WriteString(" ") // a table in the cell
		}
	}

	tokenizer := html.NewTokenizer(strings.NewReader(source))
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			break // the end of the text
		}
		// The tokenizer changes its buffer when it gives a tag name, an
		// attribute or a text: the copy comes first.
		raw := string(tokenizer.Raw())
		var (
			tag     atom.Atom // 0 for a token that is not a tag
			hasAttr bool
		)
		start := kind == html.StartTagToken
		end := kind == html.EndTagToken
		if start || end || kind == html.SelfClosingTagToken {
			var tagName []byte
			tagName, hasAttr = tokenizer.TagName()
			tag = atom.Lookup(tagName)
		}

		if tag == atom.Table && (start || end) {
			switch {
			case start && depth > 0, end && depth > 1:
				inside(raw, kind, tag, tokenizer) // a table in a cell
			case end && depth == 1:
				closeRow()
			}
			if start {
				depth++
			} else if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 1 {
			if depth > 1 {
				inside(raw, kind, tag, tokenizer)
			}
			continue
		}

		switch {
		case tag == atom.Thead:
			closeRow()
			head = start
		case tag == atom.Tbody || tag == atom.Tfoot:
			closeRow()
		case tag == atom.Tr:
			closeRow()
			if start {
				row = &tableRow{}
			}
		case start && (tag == atom.Td || tag == atom.Th):
			if row == nil {
				row = &tableRow{}
			}
			closeCell()
			name = tag.String()
			cell = &tableCell{start: raw, alone: "<" + name, rows: 1, cols: 1}
			for hasAttr {
				var key, value []byte
				key, value, hasAttr = tokenizer.TagAttr()
				n, err := strconv.Atoi(strings.TrimSpace(string(value)))
				switch {
				case string(key) == "rowspan" && err == nil && n > 1:
					cell.rows = n
					continue
				case string(key) == "rowspan":
					continue
				case string(key) == "colspan" && err == nil && n > 1:
					cell.cols = n
				}
				cell.alone += " " + string(key) + `="` + html.EscapeString(string(value)) + `"`
			}
			cell.alone += ">"
		case end && (tag == atom.Td || tag == atom.Th):
			closeCell()
		default:
			inside(raw, kind, tag, tokenizer)
		}
	}
	closeRow()
	return rows
}

// rowGroups gives the rows in groups that stay together: the rows that a
// cell of more than one row covers. The first group is the header rows of
// the table: the rows in its head and its rows of header cells, or, with
// none of them, its first row.
func rowGroups(rows []tableRow) [][]tableRow {
	var groups [][]tableRow
	for i := 0; i < len(rows); {
		first := i
		last := i + rows[i].span() - 1
		header := len(groups) == 0 && rows[i].header
		for ; i < len(rows) && (i <= last || (header && rows[i].header)); i++ {
			last = max(last, i+rows[i].span()-1)
		}
		groups = append(groups, rows[first:i])
	}
	return groups
}

func rowsHTML(rows []tableRow) string {
	var all strings.Builder
	for _, row := range rows {
		all.WriteString(row.html)
	}
	return all.String()
}

// withoutSpans gives the rows of a group again, with no cell that covers
// more than one row: each row gets a copy of each cell that covers it.
// Thus each row is complete by itself.
func withoutSpans(group []tableRow) []tableRow {
	type covering struct {
		cell tableCell
		left int // the rows below that the cell covers
	}
	covers := map[int]*covering{} // by the column of the cell
	rows := make([]tableRow, len(group))
	for i, row := range group {
		var cells []tableCell
		column, next := 0, 0
		for {
			if cover, ok := covers[column]; ok {
				cells = append(cells, cover.cell)
				if cover.left--; cover.left == 0 {
					delete(covers, column)
				}
				column += cover.cell.cols
				continue
			}
			if next < len(row.cells) {
				cell := row.cells[next]
				next++
				below := cell.rows - 1
				cell.start, cell.rows = cell.alone, 1
				if below > 0 {
					covers[column] = &covering{cell: cell, left: below}
				}
				cells = append(cells, cell)
				column += cell.cols
				continue
			}
			// The row has no cell left. A cell of a row above can cover a
			// column to the right.
			right := -1
			for at := range covers {
				if at > column && (right < 0 || at < right) {
					right = at
				}
			}
			if right < 0 {
				break
			}
			column = right
		}
		var all strings.Builder
		all.WriteString("<tr>")
		for _, cell := range cells {
			all.WriteString(cell.start + cell.rest)
		}
		all.WriteString("</tr>")
		rows[i] = tableRow{html: all.String(), cells: cells, header: row.header}
	}
	return rows
}
