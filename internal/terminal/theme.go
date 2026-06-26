package terminal

import (
	"github.com/gdamore/tcell/v3"

	"github.com/omarluq/anamnesis/internal/tui"
)

// Dark palette (Tokyo Night) hex values. The tui toolkit ships no default
// styles, so the shell must supply a complete palette or everything renders
// with a zero style.
const (
	hexText       = 0xc0caf5
	hexAccent     = 0x7aa2f7
	hexSuccess    = 0x9ece6a
	hexWarning    = 0xe0af68
	hexDim        = 0x565f89
	hexMuted      = 0x787c99
	hexBorder     = 0x3b4261
	hexDiffAdd    = 0x9ece6a
	hexDiffDel    = 0xf7768e
)

// Theme is the resolved color palette used to build every tui style set.
type Theme struct {
	Text       tcell.Color
	Accent     tcell.Color
	Success    tcell.Color
	Warning    tcell.Color
	Dim        tcell.Color
	Muted      tcell.Color
	Border     tcell.Color
	DiffAdd    tcell.Color
	DiffDel    tcell.Color
}

// DefaultTheme returns the built-in dark palette.
func DefaultTheme() Theme {
	return Theme{
		Text:       tcell.NewHexColor(hexText),
		Accent:     tcell.NewHexColor(hexAccent),
		Success:    tcell.NewHexColor(hexSuccess),
		Warning:    tcell.NewHexColor(hexWarning),
		Dim:        tcell.NewHexColor(hexDim),
		Muted:      tcell.NewHexColor(hexMuted),
		Border:     tcell.NewHexColor(hexBorder),
		DiffAdd:    tcell.NewHexColor(hexDiffAdd),
		DiffDel:    tcell.NewHexColor(hexDiffDel),
	}
}

// CodeTheme maps the palette onto the tui syntax-highlighting theme.
func (theme Theme) CodeTheme() tui.CodeTheme {
	return tui.CodeTheme{
		Text:    theme.Text,
		Accent:  theme.Accent,
		Success: theme.Success,
		Warning: theme.Warning,
		Dim:     theme.Dim,
		Muted:   theme.Muted,
		DiffAdd: theme.DiffAdd,
		DiffDel: theme.DiffDel,
	}
}

// MarkdownStyles builds the style set used to render markdown answers.
func (theme Theme) MarkdownStyles() tui.MarkdownStyles {
	return tui.MarkdownStyles{
		Text:      theme.fg(theme.Text),
		Accent:    theme.fg(theme.Accent),
		Muted:     theme.fg(theme.Muted),
		Code:      theme.bg(theme.Dim),
		CodeTheme: theme.CodeTheme(),
	}
}

// TextAreaStyles builds the style set used to render the composer.
func (theme Theme) TextAreaStyles() tui.TextAreaStyles {
	return tui.TextAreaStyles{
		Border: theme.fg(theme.Border),
		Body:   theme.fg(theme.Text),
	}
}

// ListStyles builds the style set used to render selectable lists.
func (theme Theme) ListStyles() tui.ListStyles {
	return tui.ListStyles{
		Border:   theme.fg(theme.Border),
		Accent:   theme.fg(theme.Accent),
		Muted:    theme.fg(theme.Muted),
		Text:     theme.fg(theme.Text),
		Selected: theme.bg(theme.Accent),
		Dim:      theme.fg(theme.Dim),
	}
}

// fg returns a transparent-background style painting color as the foreground.
func (theme Theme) fg(color tcell.Color) tcell.Style {
	return tcell.StyleDefault.Foreground(color)
}

// bg returns a style painting color as the cell background over the theme text.
func (theme Theme) bg(color tcell.Color) tcell.Style {
	return tcell.StyleDefault.Foreground(theme.Text).Background(color)
}
