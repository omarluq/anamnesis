package openai

import (
	"context"
	"net/http"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keyLookup returns an environment lookup that resolves OPENAI_API_KEY to key
// and reports every other variable as absent.
func keyLookup(key string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if name == EnvOpenAIKey {
			return key, true
		}

		return "", false
	}
}

// absentLookup is an environment lookup that reports every variable as unset.
func absentLookup(string) (string, bool) {
	return "", false
}

// withLookupEnv overrides the environment lookup NewClient resolves the API key
// through, letting a test drive both the present-key and unset-key paths without
// mutating process-wide state (which t.Parallel would make unsafe).
func withLookupEnv(lookup func(string) (string, bool)) Option {
	return func(set *settings) {
		set.lookupEnv = lookup
	}
}

func TestNewClientErrorsWhenAPIKeyUnset(t *testing.T) {
	t.Parallel()

	client, err := NewClient(withLookupEnv(absentLookup))

	require.Error(t, err)
	assert.Nil(t, client)

	oopsErr, ok := oops.AsOops(err)
	require.True(t, ok, "error is oops-wrapped")
	assert.Equal(t, "openai", oopsErr.Domain())
	assert.Equal(t, "missing_api_key", oopsErr.Code())
	assert.ErrorContains(t, err, EnvOpenAIKey)
}

func TestNewClientErrorsWhenAPIKeyEmpty(t *testing.T) {
	t.Parallel()

	client, err := NewClient(withLookupEnv(keyLookup("")))

	require.Error(t, err)
	assert.Nil(t, client)

	oopsErr, ok := oops.AsOops(err)
	require.True(t, ok, "error is oops-wrapped")
	assert.Equal(t, "missing_api_key", oopsErr.Code())
}

func TestNewClientSucceedsWithAPIKey(t *testing.T) {
	t.Parallel()

	client, err := NewClient(withLookupEnv(keyLookup("sk-present")))

	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, client.api, "the constructor wires the internal API client")
}

func TestParseEffortMapsKnownTiers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  responses.ReasoningEffort
	}{
		{name: "tier_none", input: "none", want: responses.ReasoningEffortNone},
		{name: "tier_minimal", input: "minimal", want: responses.ReasoningEffortMinimal},
		{name: "tier_low", input: "low", want: responses.ReasoningEffortLow},
		{name: "tier_medium", input: "medium", want: responses.ReasoningEffortMedium},
		{name: "tier_high", input: "high", want: responses.ReasoningEffortHigh},
		{name: "tier_xhigh", input: "xhigh", want: responses.ReasoningEffortXhigh},
		{name: "surrounding_whitespace_trimmed", input: "  Medium  ", want: responses.ReasoningEffortMedium},
		{name: "uppercase_normalized", input: "XHIGH", want: responses.ReasoningEffortXhigh},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseEffort(testCase.input)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got, "the tier name maps to its SDK enum, case-insensitively")
		})
	}
}

func TestParseEffortRejectsUnknownTier(t *testing.T) {
	t.Parallel()

	effort, err := ParseEffort("extreme")

	require.Error(t, err)
	assert.Empty(t, string(effort), "an unknown tier yields the zero effort")

	oopsErr, ok := oops.AsOops(err)
	require.True(t, ok, "error is oops-wrapped")
	assert.Equal(t, "config", oopsErr.Domain())
	assert.Equal(t, "invalid_reasoning_effort", oopsErr.Code())
	assert.ErrorContains(t, err, "extreme", "the error names the offending value")
}

func TestNewClientDefaultsReasoningEfforts(t *testing.T) {
	t.Parallel()

	client, err := NewClient(withLookupEnv(keyLookup("sk-present")))
	require.NoError(t, err)

	assert.Equal(t, responses.ReasoningEffortMedium, client.controllerEffort,
		"the controller defaults to medium effort")
	assert.Equal(t, responses.ReasoningEffortLow, client.subEffort,
		"the high-volume sub-calls default to low effort")
}

func TestEffortOptionsOverrideDefaults(t *testing.T) {
	t.Parallel()

	client, err := NewClient(
		withLookupEnv(keyLookup("sk-present")),
		WithControllerEffort(responses.ReasoningEffortXhigh),
		WithSubEffort(responses.ReasoningEffortHigh),
	)
	require.NoError(t, err)

	assert.Equal(t, responses.ReasoningEffortXhigh, client.controllerEffort,
		"WithControllerEffort overrides the controller default")
	assert.Equal(t, responses.ReasoningEffortHigh, client.subEffort,
		"WithSubEffort overrides the sub default")
}

func TestNewClientRequestCarriesBearerKeyAndBaseURL(t *testing.T) {
	t.Parallel()

	const (
		apiKey  = "sk-test-secret-123"
		baseURL = "https://mock.anamnesis.test/v1/"
	)

	transport := newRecordingTransport(http.StatusOK, "{}")

	client, err := NewClient(
		withLookupEnv(keyLookup(apiKey)),
		WithBaseURL(baseURL),
		WithTransport(transport),
	)
	require.NoError(t, err)

	var out map[string]any

	err = client.api.Get(context.Background(), "models", nil, &out)
	require.NoError(t, err)

	req := transport.last()
	require.NotNil(t, req, "the recording transport saw a request")

	assert.Equal(t, "Bearer "+apiKey, req.Header.Get("Authorization"),
		"the outbound request carries the bearer API key")
	assert.Equal(t, "https", req.URL.Scheme, "the base-URL override sets the scheme")
	assert.Equal(t, "mock.anamnesis.test", req.URL.Host, "the base-URL override sets the host")
	assert.Contains(t, req.URL.Path, "models", "the request targets the requested path")
}

func TestNewClientBaseURLOverrideBeatsAmbientDefault(t *testing.T) {
	t.Parallel()

	const baseURL = "https://override.anamnesis.test/v1/"

	transport := newRecordingTransport(http.StatusOK, "{}")

	client, err := NewClient(
		withLookupEnv(keyLookup("sk-key")),
		WithBaseURL(baseURL),
		WithTransport(transport),
	)
	require.NoError(t, err)

	var out map[string]any

	err = client.api.Get(context.Background(), "models", nil, &out)
	require.NoError(t, err)

	req := transport.last()
	require.NotNil(t, req)
	assert.Equal(t, "override.anamnesis.test", req.URL.Host,
		"the explicit base URL wins regardless of any ambient OPENAI_BASE_URL")
}
