package terminal_test

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"

	"github.com/omarluq/anamnesis/internal/terminal"
)

func TestDefaultThemeColorsAreNonZero(t *testing.T) {
	t.Parallel()

	theme := terminal.DefaultTheme()
	colors := []tcell.Color{
		theme.Text, theme.Accent, theme.Success, theme.Warning, theme.Dim,
		theme.Muted, theme.Border, theme.DiffAdd, theme.DiffDel,
		theme.UserMessageBg, theme.ToolPendingBg, theme.ToolSuccessBg,
		theme.ToolErrorBg, theme.ToolReviseBg, theme.ThinkingText,
	}

	for index, color := range colors {
		assert.NotEqualf(t, tcell.ColorDefault, color, "palette color %d must be set, not the terminal default", index)
	}
}

func TestThemeCodeThemeIsFullyPopulated(t *testing.T) {
	t.Parallel()

	code := terminal.DefaultTheme().CodeTheme()
	colors := []tcell.Color{
		code.Text, code.Accent, code.Success, code.Warning,
		code.Dim, code.Muted, code.DiffAdd, code.DiffDel,
	}

	for index, color := range colors {
		assert.NotEqualf(t, tcell.ColorDefault, color, "code color %d must be set", index)
	}
}

func TestThemeMarkdownStylesAreNonZero(t *testing.T) {
	t.Parallel()

	styles := terminal.DefaultTheme().MarkdownStyles()

	assert.NotEqual(t, tcell.StyleDefault, styles.Text)
	assert.NotEqual(t, tcell.StyleDefault, styles.Accent)
	assert.NotEqual(t, tcell.StyleDefault, styles.Muted)
	assert.NotEqual(t, tcell.StyleDefault, styles.Code)
	assert.NotEqual(t, tcell.ColorDefault, styles.CodeTheme.Text)
}

func TestThemeTextAreaStylesAreNonZero(t *testing.T) {
	t.Parallel()

	styles := terminal.DefaultTheme().TextAreaStyles()

	assert.NotEqual(t, tcell.StyleDefault, styles.Border)
	assert.NotEqual(t, tcell.StyleDefault, styles.Body)
}

func TestThemeStyleSetsAreDistinct(t *testing.T) {
	t.Parallel()

	styles := terminal.DefaultTheme().MarkdownStyles()

	// Distinct foreground colors must not collapse onto the same style.
	assert.NotEqual(t, styles.Text, styles.Accent)
	// A background-painted style differs from a foreground style.
	assert.NotEqual(t, styles.Text, styles.Code)
}

func TestThemeBuildsUsableTuiStyleSets(t *testing.T) {
	t.Parallel()

	theme := terminal.DefaultTheme()

	assert.NotEqual(t, tcell.ColorDefault, theme.CodeTheme().Accent)
	assert.NotEqual(t, tcell.StyleDefault, theme.MarkdownStyles().Text)
	assert.NotEqual(t, tcell.StyleDefault, theme.TextAreaStyles().Body)
}
