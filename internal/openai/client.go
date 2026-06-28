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
	"github.com/openai/openai-go/v3/responses"
	"github.com/samber/mo"
	"github.com/samber/oops"
)

// EnvOpenAIKey is the environment variable NewClient reads the OpenAI API key from.
const EnvOpenAIKey = "OPENAI_API_KEY"

// Default reasoning effort per model role, applied when NewClient is called without
// the matching WithXEffort option. They mirror the config-layer defaults: the
// controller and judge reason at medium, the high-volume sub-calls at low, trading
// the former maximum-effort setting's minutes-long turns for responsiveness. The DI
// layer overrides all three from config, so these defaults bound only direct,
// option-less construction (chiefly tests).
const (
	defaultControllerEffort = responses.ReasoningEffortMedium
	defaultSubEffort        = responses.ReasoningEffortLow
	defaultJudgeEffort      = responses.ReasoningEffortMedium
)

// Client wraps the openai-go SDK client behind the anamnesis package boundary so
// the controller, sub-LLM, and judge layers all depend on one constructed handle
// rather than scattering SDK construction across the codebase. The per-role
// reasoning efforts are resolved once at construction and read by Controller, Sub,
// and Judge respectively, so each role's effort is tuned in one place.
type Client struct {
	controllerEffort responses.ReasoningEffort
	subEffort        responses.ReasoningEffort
	judgeEffort      responses.ReasoningEffort
	api              openaisdk.Client
}

// Option customizes how NewClient builds a Client.
type Option func(*settings)

// settings holds the resolved knobs NewClient applies while building a Client.
type settings struct {
	transport        http.RoundTripper
	lookupEnv        func(string) (string, bool)
	baseURL          string
	controllerEffort responses.ReasoningEffort
	subEffort        responses.ReasoningEffort
	judgeEffort      responses.ReasoningEffort
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

// WithControllerEffort sets the reasoning effort the controller turn runs at,
// overriding defaultControllerEffort. The DI layer resolves it from
// config.Reasoning.Controller via ParseEffort.
func WithControllerEffort(effort responses.ReasoningEffort) Option {
	return func(set *settings) {
		set.controllerEffort = effort
	}
}

// WithSubEffort sets the reasoning effort the bounded sub-LLM calls run at,
// overriding defaultSubEffort. The DI layer resolves it from config.Reasoning.Sub
// via ParseEffort.
func WithSubEffort(effort responses.ReasoningEffort) Option {
	return func(set *settings) {
		set.subEffort = effort
	}
}

// WithJudgeEffort sets the reasoning effort the post-FINAL audit judge runs at,
// overriding defaultJudgeEffort. The DI layer resolves it from
// config.Reasoning.Judge via ParseEffort.
func WithJudgeEffort(effort responses.ReasoningEffort) Option {
	return func(set *settings) {
		set.judgeEffort = effort
	}
}

// NewClient reads OPENAI_API_KEY from the environment and returns a Client bound
// to it. It returns an oops error when the key is unset or empty. Options may
// override the base URL and HTTP transport for testing.
func NewClient(opts ...Option) (*Client, error) {
	set := settings{
		transport:        nil,
		lookupEnv:        os.LookupEnv,
		baseURL:          "",
		controllerEffort: defaultControllerEffort,
		subEffort:        defaultSubEffort,
		judgeEffort:      defaultJudgeEffort,
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

	return &Client{
		api:              openaisdk.NewClient(requestOpts...),
		controllerEffort: set.controllerEffort,
		subEffort:        set.subEffort,
		judgeEffort:      set.judgeEffort,
	}, nil
}
