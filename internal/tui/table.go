package tui

import (
	"strings"

	"github.com/gdamore/tcell/v3"
)

const (
	tableCellPaddingWidth = 2
	tableCenterDivisor    = 2
	tableSpansPerCell     = 2
)

// Alignment controls table cell alignment.
type Alignment int

const (
	// AlignLeft pads table cell content on the right.
	AlignLeft Alignment = iota
	// AlignCenter pads table cell content evenly on both sides.
	AlignCenter
	// AlignRight pads table cell content on the left.
	AlignRight
)

// TableCell is one table cell.
type TableCell struct {
	Style tcell.Style
	Text  string
}

// Table is a simple bordered table component.
type Table struct {
	Style       tcell.Style
	HeaderStyle tcell.Style
	BorderStyle tcell.Style
	Headers     []TableCell
	Rows        [][]TableCell
	Alignments  []Alignment
	// Stretch widens the last column so the table fills the available width;
	// when false the table renders at its content width.
	Stretch bool
}

// Render returns table lines clipped to width and height. The top border and
// header are always rendered; only the data rows are clipped to the height that
// remains after reserving a row for the bottom border, and every line is
// truncated to width.
func (table *Table) Render(width, height int) []Line {
	if table == nil || width <= 0 || height <= 0 {
		return []Line{}
	}

	rows := table.allRows()
	if len(rows) == 0 {
		return []Line{}
	}

	colCount := table.columnCount(rows)
	colWidths := table.columnWidths(rows, colCount, width)

	header := []Line{NewLine(table.BorderStyle, table.tableBorder("╭", "┬", "╮", colWidths))}
	if len(table.Headers) > 0 {
		header = append(
			header,
			table.renderRow(table.Headers, colWidths, table.HeaderStyle),
			NewLine(table.BorderStyle, table.tableBorder("├", "┼", "┤", colWidths)),
		)
	}

	footer := NewLine(table.BorderStyle, table.tableBorder("╰", "┴", "╯", colWidths))

	// Reserve one row for the bottom border and clip the data rows to the rest.
	dataRows := table.Rows
	if budget := max(0, height-len(header)-1); len(dataRows) > budget {
		dataRows = dataRows[:budget]
	}

	lines := make([]Line, 0, len(header)+len(dataRows)+1)

	lines = append(lines, header...)
	for _, row := range dataRows {
		lines = append(lines, table.renderRow(row, colWidths, table.Style))
	}

	lines = append(lines, footer)

	for index := range lines {
		lines[index] = lines[index].Truncate(width)
	}

	// Head-clip so the top border and header survive viewports too short for the
	// full chrome (Tail would drop them).
	return lines[:min(len(lines), height)]
}

func (table *Table) allRows() [][]TableCell {
	rows := [][]TableCell{}
	if len(table.Headers) > 0 {
		rows = append(rows, table.Headers)
	}

	rows = append(rows, table.Rows...)

	return rows
}

func (table *Table) columnCount(rows [][]TableCell) int {
	count := 0
	for _, row := range rows {
		count = max(count, len(row))
	}

	return count
}

func (table *Table) columnWidths(rows [][]TableCell, colCount, maxWidth int) []int {
	if colCount == 0 {
		return []int{}
	}

	widths := make([]int, colCount)

	for _, row := range rows {
		for col, cell := range row {
			widths[col] = max(widths[col], Width(cell.Text))
		}
	}

	available := max(1, maxWidth-colCount-1-(colCount*tableCellPaddingWidth))
	for sumInts(widths) > available {
		largest := 0
		for index := range widths {
			if widths[index] > widths[largest] {
				largest = index
			}
		}

		if widths[largest] <= 1 {
			break
		}

		widths[largest]--
	}

	if table.Stretch {
		if slack := available - sumInts(widths); slack > 0 {
			widths[colCount-1] += slack
		}
	}

	return widths
}

func (table *Table) renderRow(row []TableCell, widths []int, fallback tcell.Style) Line {
	spans := make([]Span, 0, 1+len(widths)*tableSpansPerCell)
	spans = append(spans, Span{Text: "│", Style: table.BorderStyle})

	var builder strings.Builder
	builder.WriteString("│")

	for col, width := range widths {
		cell := TableCell{Style: tcell.Style{}, Text: ""}
		if col < len(row) {
			cell = row[col]
		}

		style := cell.Style
		if style == (tcell.Style{}) {
			style = fallback
		}

		value := table.align(cell.Text, width, col)
		segment := " " + value + " "
		spans = append(spans, Span{Text: segment, Style: style}, Span{Text: "│", Style: table.BorderStyle})
		builder.WriteString(segment)
		builder.WriteString("│")
	}

	return Line{Text: builder.String(), Style: fallback, Spans: spans}
}

func (table *Table) align(text string, width, column int) string {
	text = Truncate(text, width)
	padding := width - Width(text)

	alignment := AlignLeft
	if column < len(table.Alignments) {
		alignment = table.Alignments[column]
	}

	switch alignment {
	case AlignLeft:
		return text + strings.Repeat(" ", padding)
	case AlignRight:
		return strings.Repeat(" ", padding) + text
	case AlignCenter:
		left := padding / tableCenterDivisor

		return strings.Repeat(" ", left) + text + strings.Repeat(" ", padding-left)
	default:
		return text + strings.Repeat(" ", padding)
	}
}

func (table *Table) tableBorder(left, middle, right string, widths []int) string {
	parts := make([]string, 0, len(widths))
	for _, width := range widths {
		parts = append(parts, strings.Repeat("─", width+tableCellPaddingWidth))
	}

	return left + strings.Join(parts, middle) + right
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}

	return total
}
