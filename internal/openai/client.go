// Package openai constructs the OpenAI API client anamnesis issues its
// controller, sub-LLM, and judge calls through. It reads the API key from the
// environment, fails closed with an oops error when the key is absent, and
// exposes seams that let tests inject an HTTP transport and a base-URL override
// so the whole layer is exercisable with no live network.
package openai

import (
	"net/http"
	"os"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/samber/mo"
	"github.com/samber/oops"
)

// EnvOpenAIKey is the environment variable NewClient reads the OpenAI API key from.
const EnvOpenAIKey = "OPENAI_API_KEY"

// Client wraps the openai-go SDK client behind the anamnesis package boundary so
// the controller, sub-LLM, and judge layers all depend on one constructed handle
// rather than scattering SDK construction across the codebase.
type Client struct {
	api openaisdk.Client
}

// Option customizes how NewClient builds a Client.
type Option func(*settings)

// settings holds the resolved knobs NewClient applies while building a Client.
type settings struct {
	transport http.RoundTripper
	lookupEnv func(string) (string, bool)
	baseURL   string
}

// WithBaseURL overrides the OpenAI API base URL, chiefly to point tests at a
// local stub instead of the live endpoint.
func WithBaseURL(baseURL string) Option {
	return func(set *settings) {
		set.baseURL = baseURL
	}
}

// WithTransport injects the HTTP round tripper the underlying client uses, so a
// test can capture or stub requests without touching the network.
func WithTransport(transport http.RoundTripper) Option {
	return func(set *settings) {
		set.transport = transport
	}
}

// NewClient reads OPENAI_API_KEY from the environment and returns a Client bound
// to it. It returns an oops error when the key is unset or empty. Options may
// override the base URL and HTTP transport for testing.
func NewClient(opts ...Option) (*Client, error) {
	set := settings{
		transport: nil,
		lookupEnv: os.LookupEnv,
		baseURL:   "",
	}
	for _, opt := range opts {
		opt(&set)
	}

	apiKey := mo.TupleToOption(set.lookupEnv(EnvOpenAIKey)).OrEmpty()
	if apiKey == "" {
		return nil, oops.
			In("openai").
			Code("missing_api_key").
			Errorf("%s is not set", EnvOpenAIKey)
	}

	requestOpts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if set.baseURL != "" {
		requestOpts = append(requestOpts, option.WithBaseURL(set.baseURL))
	}

	if set.transport != nil {
		requestOpts = append(requestOpts, option.WithHTTPClient(&http.Client{
			Transport: set.transport,
		}))
	}

	return &Client{api: openaisdk.NewClient(requestOpts...)}, nil
}

// API exposes the underlying openai-go client for the Responses calls the
// controller, sub-LLM, and judge layers issue.
func (client *Client) API() *openaisdk.Client {
	return &client.api
}
