// Package tui provides small reusable terminal UI primitives and components.
//
// It is intentionally lighter than tview: components render into tcell/v3
// screens, but most state is plain Go data that can also be tested by
// inspecting rendered lines.
package tui

import "github.com/gdamore/tcell/v3"

// ContentSetter is the subset of tcell.Screen required by draw helpers.
// It only requires SetContent so tests and buffers can provide lightweight sinks.
type ContentSetter interface {
	SetContent(column, row int, mainc rune, combc []rune, style tcell.Style)
}

// Rect describes a terminal rectangle.
type Rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// Empty reports whether rect has no drawable area.
func (rect Rect) Empty() bool {
	return rect.Width <= 0 || rect.Height <= 0
}

// Span is one styled segment inside a line.
type Span struct {
	Style tcell.Style
	Text  string
}

// Line is one terminal display line with optional per-span styles.
type Line struct {
	Text  string
	Style tcell.Style
	Spans []Span
}

// NewLine returns a line with one default style and no per-span overrides.
func NewLine(style tcell.Style, text string) Line {
	return Line{Text: text, Style: style, Spans: nil}
}
