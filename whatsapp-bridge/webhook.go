package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// webhookClient is a dedicated HTTP client with a timeout for outbound webhook
// calls, replacing http.DefaultClient which has no timeout by default.
var webhookClient = &http.Client{Timeout: 10 * time.Second}

// WebhookPayload represents the data sent to the webhook.
type WebhookPayload struct {
	Sender          string `json:"sender"`
	Content         string `json:"content"`
	ChatJID         string `json:"chatJID"`
	IsFromMe        bool   `json:"isFromMe"`
	QuotedMessageId string `json:"quotedMessageId,omitempty"`
	QuotedSender    string `json:"quotedSender,omitempty"`
	QuotedContent   string `json:"quotedContent,omitempty"`
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
func SendWebhook(sender, content, chatJID string, isFromMe bool, quotedMessageId, quotedSender, quotedContent string) {
	webhookURL := os.Getenv("WEBHOOK_URL")
	if webhookURL == "" {
		webhookURL = "http://localhost:8769/whatsapp/webhook"
	}

	payload := WebhookPayload{
		Sender:          sender,
		Content:         content,
		ChatJID:         chatJID,
		IsFromMe:        isFromMe,
		QuotedMessageId: quotedMessageId,
		QuotedSender:    quotedSender,
		QuotedContent:   quotedContent,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("Error marshaling webhook payload: %v\n", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		fmt.Printf("Error building webhook request: %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// Sign the body when a shared secret is configured. The downstream
	// handler compares this against its own HMAC using timingSafeEqual.
	if secret := os.Getenv("WEBHOOK_SECRET"); secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(jsonData)
		sig := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Signature", "sha256="+sig)
	}

	resp, err := webhookClient.Do(req)
	if err != nil {
		fmt.Printf("Error sending webhook: %v\n", err)
		return
	}
	defer resp.Body.Close()
	fmt.Printf("✓ Webhook sent for message from %s\n", sender)
}

// In main.go, handleMessage forwards webhooks for messages with text content.
// It will forward self-sent messages when the env var FORWARD_SELF=true.
