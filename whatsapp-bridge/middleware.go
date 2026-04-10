package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// bearerAuthMiddleware enforces Authorization: Bearer <token> on all routes
// except /api/health (which stays public for uptime probes).
//
// The token is read from BRIDGE_AUTH_TOKEN. If unset, the bridge refuses
// to start — we will not run an unauthenticated WhatsApp send endpoint
// on the public internet.
func bearerAuthMiddleware(next http.Handler) http.Handler {
	token := os.Getenv("BRIDGE_AUTH_TOKEN")
	if token == "" {
		panic("BRIDGE_AUTH_TOKEN must be set — refusing to start bridge without auth")
	}
	expected := []byte("Bearer " + token)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health stays public so uptime checks can probe without the token.
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		got := []byte(r.Header.Get("Authorization"))
		if len(got) != len(expected) || subtle.ConstantTimeCompare(got, expected) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// signPayload returns "sha256=<hex>" HMAC of body using WEBHOOK_SIGNING_SECRET.
// Returns "" if the secret is unset (webhook signing disabled).
func signPayload(body []byte) string {
	secret := os.Getenv("WEBHOOK_SIGNING_SECRET")
	if secret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// postWebhookWithRetry POSTs body to url with exponential-backoff retry
// (3 attempts, 500ms/1s/2s). It signs the payload with HMAC-SHA256 if
// WEBHOOK_SIGNING_SECRET is set and sets X-Signature + X-Delivery-Attempt
// headers. 4xx responses are treated as permanent and not retried.
func postWebhookWithRetry(url string, body []byte) error {
	if url == "" {
		return fmt.Errorf("webhook url empty")
	}
	const maxAttempts = 3
	var lastErr error
	backoff := 500 * time.Millisecond

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "whatsapp-bridge/leonglobal")
		if sig := signPayload(body); sig != "" {
			req.Header.Set("X-Signature", sig)
		}
		req.Header.Set("X-Delivery-Attempt", fmt.Sprintf("%d", attempt))

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("webhook returned %d", resp.StatusCode)
			// 4xx = permanent client error, don't retry.
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return lastErr
			}
		}

		if attempt < maxAttempts {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return fmt.Errorf("webhook delivery failed after %d attempts: %w", maxAttempts, lastErr)
}
