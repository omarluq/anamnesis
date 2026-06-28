package tui

import "strings"

const (
	borderHorizontal  = "─"
	borderVertical    = "│"
	borderTopLeft     = "╭"
	borderTopRight    = "╮"
	borderBottomLeft  = "╰"
	borderBottomRight = "╯"
	borderMiddleLeft  = "├"
	borderMiddleRight = "┤"
)

// Border contains the runes used to draw a box.
type Border struct {
	Horizontal  string
	Vertical    string
	TopLeft     string
	TopRight    string
	BottomLeft  string
	BottomRight string
	MiddleLeft  string
	MiddleRight string
}

// RoundedBorder returns the default rounded border style.
func RoundedBorder() Border {
	return Border{
		Horizontal:  borderHorizontal,
		Vertical:    borderVertical,
		TopLeft:     borderTopLeft,
		TopRight:    borderTopRight,
		BottomLeft:  borderBottomLeft,
		BottomRight: borderBottomRight,
		MiddleLeft:  borderMiddleLeft,
		MiddleRight: borderMiddleRight,
	}
}

// TopBorder returns a rounded top border with an optional right-aligned label.
func TopBorder(width int, title string) string {
	border := RoundedBorder()

	return borderLine(width, title, border.TopLeft, border.TopRight, border.Horizontal)
}

// BottomBorder returns a rounded bottom border.
func BottomBorder(width int) string {
	border := RoundedBorder()

	return borderLine(width, "", border.BottomLeft, border.BottomRight, border.Horizontal)
}

func borderLine(width int, title, left, right, horizontal string) string {
	if width <= 0 {
		return ""
	}

	leftWidth := Width(left)
	rightWidth := Width(right)

	if width <= leftWidth {
		return Truncate(left, width)
	}

	if width <= leftWidth+rightWidth {
		return Truncate(left+right, width)
	}

	innerWidth := max(0, width-leftWidth-rightWidth)

	if title == "" {
		return left + strings.Repeat(horizontal, innerWidth) + right
	}

	label := borderTitleLabel(title, innerWidth)
	fillWidth := max(0, innerWidth-Width(label))

	return left + strings.Repeat(horizontal, fillWidth) + label + right
}

func borderTitleLabel(title string, width int) string {
	title = strings.TrimSpace(strings.ReplaceAll(title, "\n", " "))
	if title == "" {
		return ""
	}

	return Truncate(title+"──", width)
}
