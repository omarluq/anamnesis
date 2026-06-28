package transcript_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/omarluq/anamnesis/internal/transcript"
)

func TestCanMergeStreamingRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role transcript.Role
		want bool
	}{
		{name: "assistant", role: transcript.RoleAssistant, want: true},
		{name: "thinking", role: transcript.RoleThinking, want: true},
		{name: "user", role: transcript.RoleUser, want: false},
		{name: "tool", role: transcript.RoleToolResult, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.want, transcript.CanMergeStreamingRole(test.role))
		})
	}
}
