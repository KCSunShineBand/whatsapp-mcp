package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

// webhookClient is a dedicated HTTP client with a timeout for outbound webhook
// calls, replacing http.DefaultClient which has no timeout by default.
var webhookClient = &http.Client{Timeout: 10 * time.Second}

// webhookRetryBackoff is the delay between webhook send retries when the
// failure is a DNS lookup failure (LL-0030: CF Workers custom-domain DNS can
// lag bridge startup by up to ~20 min during initial deploy). Overridable in
// tests via the unexported var below.
var webhookRetryBackoff = 30 * time.Second

// webhookMaxRetries is the maximum number of additional attempts after the
// first try, when the failure is a DNS lookup failure. Total attempts =
// 1 + webhookMaxRetries.
const webhookMaxRetries = 3

// WebhookPayload represents the data sent to the webhook.
//
// MessageId is the upstream WA message ID (msg.Info.ID). Downstream consumers
// SHOULD use it as the deduplication key. Without it, the Worker would
// generate a fresh UUID per inbound and the messages.external_id unique
// constraint could not catch duplicate webhook deliveries.
//
// MediaType is the upstream media type ("image", "video", "audio", "document",
// "sticker") or empty for plain text. Required so that media-only messages
// (no caption) can be routed correctly downstream and so the Worker can
// populate messages.media_type without re-reading from bridge SQLite.
type WebhookPayload struct {
	Sender          string `json:"sender"`
	Content         string `json:"content"`
	ChatJID         string `json:"chatJID"`
	IsFromMe        bool   `json:"isFromMe"`
	MessageId       string `json:"messageId"`
	MediaType       string `json:"mediaType,omitempty"`
	QuotedMessageId string `json:"quotedMessageId,omitempty"`
	QuotedSender    string `json:"quotedSender,omitempty"`
	QuotedContent   string `json:"quotedContent,omitempty"`
}

// isDNSLookupFailure returns true when the supplied error is (or wraps) a
// DNS lookup failure -- the kind of failure that warrants a retry on the
// assumption that propagation is in progress. Returns false for connection
// refused, timeouts, TLS failures, 5xx responses, etc., which retry would
// not help.
func isDNSLookupFailure(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return errors.As(urlErr.Err, &dnsErr)
	}
	return false
}

// SendWebhook sends a message to the webhook endpoint.
//
// When the WEBHOOK_SECRET env var is set, the outgoing request is signed
// with an HMAC-SHA256 over the raw JSON body and the hex digest is sent in
// the `X-Signature: sha256=<hex>` header. Downstream consumers (e.g., the
// LGPL Ticket Tracker webhook at /api/webhooks/whatsapp) verify this header
// using a constant-time compare before trusting any payload content.
//
// When WEBHOOK_SECRET is empty the signature header is omitted for
// backwards compatibility with local dev stacks; production deployments
// MUST set the secret.
//
// On DNS lookup failure, retries up to webhookMaxRetries times with
// webhookRetryBackoff between attempts. Non-DNS errors fail fast.
func SendWebhook(sender, content, chatJID string, isFromMe bool, mediaType, messageId, quotedMessageId, quotedSender, quotedContent string) {
	webhookURL := os.Getenv("WEBHOOK_URL")
	if webhookURL == "" {
		webhookURL = "http://localhost:8769/whatsapp/webhook"
	}

	payload := WebhookPayload{
		Sender:          sender,
		Content:         content,
		ChatJID:         chatJID,
		IsFromMe:        isFromMe,
		MessageId:       messageId,
		MediaType:       mediaType,
		QuotedMessageId: quotedMessageId,
		QuotedSender:    quotedSender,
		QuotedContent:   quotedContent,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("Error marshaling webhook payload: %v\n", err)
		return
	}

	secret := os.Getenv("WEBHOOK_SECRET")
	var sigHeader string
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(jsonData)
		sigHeader = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	var resp *http.Response
	var lastErr error
	for attempt := 0; attempt <= webhookMaxRetries; attempt++ {
		// Build a fresh request per attempt -- the body buffer is consumed
		// on each Do() call so it cannot be safely reused.
		req, reqErr := http.NewRequest(http.MethodPost, webhookURL, bytes.NewBuffer(jsonData))
		if reqErr != nil {
			fmt.Printf("Error building webhook request: %v\n", reqErr)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if sigHeader != "" {
			req.Header.Set("X-Signature", sigHeader)
		}

		resp, lastErr = webhookClient.Do(req)
		if lastErr == nil {
			break
		}
		if !isDNSLookupFailure(lastErr) || attempt == webhookMaxRetries {
			fmt.Printf("Error sending webhook (attempt %d/%d): %v\n",
				attempt+1, webhookMaxRetries+1, lastErr)
			return
		}
		fmt.Printf("Webhook DNS lookup failed (attempt %d/%d): %v, retrying in %v\n",
			attempt+1, webhookMaxRetries+1, lastErr, webhookRetryBackoff)
		time.Sleep(webhookRetryBackoff)
	}
	defer resp.Body.Close()
	fmt.Printf("✓ Webhook sent for message from %s\n", sender)
}

// In main.go, handleMessage forwards webhooks for messages with text content.
// It will forward self-sent messages when the env var FORWARD_SELF=true.
