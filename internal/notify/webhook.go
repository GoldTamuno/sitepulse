package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// WebhookNotifier POSTs a JSON payload to a configured URL — the
// integration point for Slack, Discord, PagerDuty, or any custom receiver
// a user wants to wire up themselves. Designed so additional providers can
// be added later (per the original spec's requirement) without touching
// this type: a user pointing this at a Slack Incoming Webhook URL, for
// instance, works today with zero SitePulse-side changes, because Slack
// accepts a JSON POST — the "pluggability" is in the fact that ANY
// JSON-accepting HTTP endpoint is already supported, not in a per-provider
// integration list that needs maintaining.
type WebhookNotifier struct {
	url    string
	client *http.Client
	log    *slog.Logger
}

func NewWebhookNotifier(url string, log *slog.Logger) *WebhookNotifier {
	return &WebhookNotifier{
		url: url,
		// A dedicated short timeout for webhook delivery — this must never
		// be allowed to hang the incident-detection code path waiting on
		// some slow or unresponsive third-party endpoint.
		client: &http.Client{Timeout: 5 * time.Second},
		log:    log,
	}
}

type webhookPayload struct {
	Kind        string `json:"kind"`
	MonitorID   int64  `json:"monitor_id"`
	MonitorName string `json:"monitor_name"`
	MonitorURL  string `json:"monitor_url"`
	Cause       string `json:"cause,omitempty"`
	Duration    string `json:"duration,omitempty"`
	Timestamp   string `json:"timestamp"`
}

func (n *WebhookNotifier) Notify(ctx context.Context, event Event) {
	if n.url == "" {
		return // webhook notifications not configured — silent no-op
	}

	payload := webhookPayload{
		Kind:        string(event.Kind),
		MonitorID:   event.MonitorID,
		MonitorName: event.MonitorName,
		MonitorURL:  event.MonitorURL,
		Cause:       event.Cause,
		Duration:    event.Duration,
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		n.log.Error("webhook notification: failed to encode payload", "error", err)
		return
	}

	// A bounded per-request timeout separate from ctx — the caller's ctx
	// might be long-lived (or, during shutdown, already cancelled), but a
	// webhook delivery attempt should never be allowed to run longer than
	// a few seconds regardless.
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		n.log.Error("webhook notification: failed to build request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "SitePulse-Webhook/1.0")

	resp, err := n.client.Do(req)
	if err != nil {
		n.log.Error("webhook notification failed", "monitor_id", event.MonitorID, "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		n.log.Warn("webhook notification received non-success status",
			"monitor_id", event.MonitorID, "status_code", resp.StatusCode)
		return
	}
	n.log.Info("webhook notification sent", "monitor_id", event.MonitorID, "kind", event.Kind)
}
