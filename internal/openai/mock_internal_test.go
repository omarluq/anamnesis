package openai

import (
	"bytes"
	"io"
	"net/http"
	"sync"
)

// recordingTransport is a clean http.RoundTripper stub: it captures every request
// it sees and answers each with one canned response, so a test can assert on the
// outbound request (auth header, target URL) with no network access. It is
// concurrency safe so the -race detector stays quiet if a client retries.
//
// This stays a hand-written stub rather than a testify/mock by deliberate
// judgment: http.RoundTripper is a one-method seam whose only job here is to
// record a request and replay a fixed response. A testify/mock would add
// .On/.Return plumbing that reads no clearer than the stub, so the package keeps
// a clean RoundTripper stub for its transport seam and reserves testify/mock for
// the richer behavioral interfaces elsewhere in the codebase.
type recordingTransport struct {
	body     string
	requests []*http.Request
	mu       sync.Mutex
	status   int
}

// newRecordingTransport returns a recordingTransport that answers every request
// with the given status code and JSON body.
func newRecordingTransport(status int, body string) *recordingTransport {
	return &recordingTransport{
		body:     body,
		requests: nil,
		mu:       sync.Mutex{},
		status:   status,
	}
}

// RoundTrip records a clone of req and returns the canned response.
func (transport *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	transport.mu.Lock()
	transport.requests = append(transport.requests, req.Clone(req.Context()))
	transport.mu.Unlock()

	header := make(http.Header)
	header.Set("Content-Type", "application/json")

	return &http.Response{
		Status:        http.StatusText(transport.status),
		StatusCode:    transport.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(bytes.NewBufferString(transport.body)),
		ContentLength: int64(len(transport.body)),
		Request:       req,
	}, nil
}

// last returns the most recently recorded request, or nil if none were seen.
func (transport *recordingTransport) last() *http.Request {
	transport.mu.Lock()
	defer transport.mu.Unlock()

	if len(transport.requests) == 0 {
		return nil
	}

	return transport.requests[len(transport.requests)-1]
}
