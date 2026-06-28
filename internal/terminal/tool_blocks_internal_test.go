package terminal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParseQueryContentKeepsMarkerLikeOutputLines proves a sub-answer whose own
// lines start with reserved section markers ("output:", "error:", "tool:") stays
// verbatim output content once the output section opens, rather than re-opening a
// section and corrupting the rendered query block (a real risk on journald data).
func TestParseQueryContentKeepsMarkerLikeOutputLines(t *testing.T) {
	t.Parallel()

	content := "tool: agent.Query\narguments:\n{\"prompt\":\"why\"}\noutput:\n" +
		"the unit logged:\noutput: disk full\nerror: i/o timeout\ntool: restart attempted"

	parsed := parseQueryContent(content)

	assert.Equal(t, "agent.Query", parsed.Name, "the tool header still names the query")
	assert.JSONEq(t, `{"prompt":"why"}`, parsed.Args, "the arguments section is parsed before the output opens")
	assert.Equal(t,
		"the unit logged:\noutput: disk full\nerror: i/o timeout\ntool: restart attempted",
		parsed.Output,
		"marker-like lines inside the output stay verbatim output content")
	assert.Empty(t, parsed.Error,
		"a marker-like 'error:' line inside the output does not populate the error section")
}
