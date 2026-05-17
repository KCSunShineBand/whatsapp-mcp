package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestWebhookPayload_JSONShape locks the wire format so downstream consumers
// (cloudflare-worker/personal-router) keep parsing it correctly.
func TestWebhookPayload_JSONShape(t *testing.T) {
	p := WebhookPayload{
		Sender:    "6598626278",
		Content:   "hello",
		ChatJID:   "6598626278@s.whatsapp.net",
		IsFromMe:  false,
		MessageId: "3EB0ABCDEF1234567890",
		MediaType: "image",
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		`"sender":"6598626278"`,
		`"content":"hello"`,
		`"chatJID":"6598626278@s.whatsapp.net"`,
		`"isFromMe":false`,
		`"messageId":"3EB0ABCDEF1234567890"`,
		`"mediaType":"image"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("payload JSON missing %q\nfull: %s", want, got)
		}
	}
}

// TestWebhookPayload_MediaTypeOmittedWhenEmpty keeps the wire compact for
// the text-only majority case.
func TestWebhookPayload_MediaTypeOmittedWhenEmpty(t *testing.T) {
	p := WebhookPayload{
		Sender:    "6598626278",
		Content:   "hello",
		ChatJID:   "6598626278@s.whatsapp.net",
		MessageId: "3EB0ABCDEF1234567890",
		// MediaType: "",
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), `"mediaType"`) {
		t.Errorf("empty mediaType should be omitted, got %s", string(b))
	}
}

// TestIsDNSLookupFailure_TrueForRawDNSError covers the simplest case.
func TestIsDNSLookupFailure_TrueForRawDNSError(t *testing.T) {
	err := &net.DNSError{Err: "no such host", Name: "router.example.com", IsNotFound: true}
	if !isDNSLookupFailure(err) {
		t.Errorf("expected DNS error to be classified as DNS failure")
	}
}

// TestIsDNSLookupFailure_TrueForWrappedURLDNSError covers the realistic shape
// that http.Client surfaces (a *url.Error wrapping a *net.OpError wrapping a
// *net.DNSError).
func TestIsDNSLookupFailure_TrueForWrappedURLDNSError(t *testing.T) {
	dnsErr := &net.DNSError{Err: "no such host", Name: "router.example.com", IsNotFound: true}
	urlErr := &url.Error{Op: "Post", URL: "http://router.example.com", Err: dnsErr}
	if !isDNSLookupFailure(urlErr) {
		t.Errorf("expected wrapped DNS error to be classified as DNS failure")
	}
}

// TestIsDNSLookupFailure_FalseForOtherErrors guards against over-eager retry
// on connection refused / timeout / 5xx response shapes.
func TestIsDNSLookupFailure_FalseForOtherErrors(t *testing.T) {
	for _, err := range []error{
		errors.New("connection refused"),
		&url.Error{Op: "Post", URL: "http://x", Err: errors.New("connection reset")},
		io.EOF,
	} {
		if isDNSLookupFailure(err) {
			t.Errorf("non-DNS error %v incorrectly classified as DNS failure", err)
		}
	}
}

// TestSendWebhook_PostsExpectedFields validates the happy path including
// new fields and the HMAC header.
func TestSendWebhook_PostsExpectedFields(t *testing.T) {
	t.Setenv("WEBHOOK_SECRET", "test-secret")

	var receivedBody atomic.Value // string
	var receivedSig atomic.Value  // string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody.Store(string(body))
		receivedSig.Store(r.Header.Get("X-Signature"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	os.Setenv("WEBHOOK_URL", srv.URL)
	defer os.Unsetenv("WEBHOOK_URL")

	SendWebhook(
		"6598626278", "hello", "6598626278@s.whatsapp.net", false,
		"image", "3EB0FEEDBEEFCAFE",
		"", "", "",
	)

	body, _ := receivedBody.Load().(string)
	sig, _ := receivedSig.Load().(string)
	for _, want := range []string{
		`"messageId":"3EB0FEEDBEEFCAFE"`,
		`"mediaType":"image"`,
		`"sender":"6598626278"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nfull: %s", want, body)
		}
	}
	if !strings.HasPrefix(sig, "sha256=") {
		t.Errorf("expected X-Signature header with sha256= prefix, got %q", sig)
	}
}

// TestSendWebhook_RetriesOnDNSFailureUntilSuccess simulates the LL-0030
// scenario: CF Workers Custom Domain DNS lags bridge startup, the first
// attempts return no-such-host, then propagation completes and the request
// succeeds. The webhookClient is swapped to a transport that fakes DNS
// failures for the first N calls then forwards to a real httptest server.
func TestSendWebhook_RetriesOnDNSFailureUntilSuccess(t *testing.T) {
	// Speed up the retry backoff for the test.
	origBackoff := webhookRetryBackoff
	webhookRetryBackoff = 20 * time.Millisecond
	defer func() { webhookRetryBackoff = origBackoff }()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	os.Setenv("WEBHOOK_URL", srv.URL)
	defer os.Unsetenv("WEBHOOK_URL")

	// Fake transport: first 2 attempts return a DNS error, then delegate
	// to the real default transport which reaches httptest.
	var attempt atomic.Int32
	origClient := webhookClient
	webhookClient = &http.Client{
		Timeout: 5 * time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			n := attempt.Add(1)
			if n <= 2 {
				return nil, &url.Error{
					Op:  r.Method,
					URL: r.URL.String(),
					Err: &net.DNSError{Err: "no such host", Name: r.URL.Hostname(), IsNotFound: true},
				}
			}
			return http.DefaultTransport.RoundTrip(r)
		}),
	}
	defer func() { webhookClient = origClient }()

	SendWebhook(
		"6598626278", "hello", "6598626278@s.whatsapp.net", false,
		"", "3EB0FEEDBEEFCAFE",
		"", "", "",
	)

	if got, want := attempt.Load(), int32(3); got != want {
		t.Errorf("expected %d transport attempts (2 DNS fail + 1 success), got %d", want, got)
	}
	if got, want := hits.Load(), int32(1); got != want {
		t.Errorf("expected exactly %d server hit, got %d", want, got)
	}
}

// TestSendWebhook_GivesUpAfterMaxRetries asserts the cap is enforced and the
// server is never reached when DNS keeps failing.
func TestSendWebhook_GivesUpAfterMaxRetries(t *testing.T) {
	origBackoff := webhookRetryBackoff
	webhookRetryBackoff = 5 * time.Millisecond
	defer func() { webhookRetryBackoff = origBackoff }()

	var serverHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serverHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	os.Setenv("WEBHOOK_URL", srv.URL)
	defer os.Unsetenv("WEBHOOK_URL")

	var transportAttempts atomic.Int32
	origClient := webhookClient
	webhookClient = &http.Client{
		Timeout: 5 * time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			transportAttempts.Add(1)
			return nil, &url.Error{
				Op:  r.Method,
				URL: r.URL.String(),
				Err: &net.DNSError{Err: "no such host", Name: r.URL.Hostname(), IsNotFound: true},
			}
		}),
	}
	defer func() { webhookClient = origClient }()

	SendWebhook(
		"6598626278", "hello", "6598626278@s.whatsapp.net", false,
		"", "3EB0FEEDBEEFCAFE",
		"", "", "",
	)

	if got, want := transportAttempts.Load(), int32(webhookMaxRetries+1); got != want {
		t.Errorf("expected %d transport attempts, got %d", want, got)
	}
	if got := serverHits.Load(); got != 0 {
		t.Errorf("server should not have been reached, got %d hits", got)
	}
}

// roundTripperFunc adapts a function to http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
