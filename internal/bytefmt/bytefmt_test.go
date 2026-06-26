package bytefmt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/omarluq/anamnesis/internal/bytefmt"
)

func TestFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		want  string
		input int64
	}{
		{name: "zero", input: 0, want: "0B"},
		{name: "negative", input: -512, want: "0B"},
		{name: "bytes", input: 512, want: "512B"},
		{name: "kibibytes", input: 4096, want: "4.0KiB"},
		{name: "mebibytes", input: 5 * 1024 * 1024, want: "5.0MiB"},
		{name: "gibibytes", input: 3 * 1024 * 1024 * 1024, want: "3.0GiB"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, bytefmt.Format(tc.input))
		})
	}
}
