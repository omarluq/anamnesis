package vinfo_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/vinfo"
)

// versionContract matches the public String result "<version> (commit=<c>, built=<d>)".
var versionContract = regexp.MustCompile(`^.+ \(commit=.+, built=.+\)$`)

func TestStringReportsBuildContract(t *testing.T) {
	t.Parallel()

	rendered := vinfo.String()

	require.NotEmpty(t, rendered)
	assert.Regexp(t, versionContract, rendered)
}
