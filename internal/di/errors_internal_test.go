package di

import (
	"errors"
	"testing"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errSentinel = errors.New("boom")

func TestServiceErrorWrapsWithDomain(t *testing.T) {
	t.Parallel()

	err := serviceError(errSentinel, "load config")

	require.Error(t, err)

	oopsErr, ok := oops.AsOops(err)

	require.True(t, ok, "error is oops-wrapped")
	assert.Equal(t, "di", oopsErr.Domain())
	assert.Equal(t, "di_error", oopsErr.Code())
	require.ErrorContains(t, err, "load config")
	assert.ErrorIs(t, err, errSentinel)
}

func TestServiceErrorReturnsNilForNil(t *testing.T) {
	t.Parallel()

	assert.NoError(t, serviceError(nil, "noop"))
}
