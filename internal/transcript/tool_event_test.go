package transcript_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/omarluq/anamnesis/internal/transcript"
)

func TestFormatToolEventDisplayOmitsStructuredErrorMarker(t *testing.T) {
	t.Parallel()

	event := transcript.ToolEvent{
		Name:          "read",
		ArgumentsJSON: "",
		DetailsJSON:   "",
		Result:        "",
		Error:         "read failed",
	}

	assert.Equal(t, stringsJoinLines(
		"tool: read",
		"error:",
		"read failed",
	), transcript.FormatToolEventDisplay(&event))
}

func TestFormatToolEventSkipsBlankOptionalSections(t *testing.T) {
	t.Parallel()

	event := transcript.ToolEvent{
		Name:          "write",
		ArgumentsJSON: " \n\t ",
		DetailsJSON:   "",
		Result:        "\n",
		Error:         "",
	}

	assert.Equal(t, "tool: write", transcript.FormatToolEventDisplay(&event))
}

func stringsJoinLines(lines ...string) string {
	return strings.Join(lines, "\n")
}
