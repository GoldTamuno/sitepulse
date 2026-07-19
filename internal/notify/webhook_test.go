package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
)

func testNotifyLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestWebhookNotifier_SendsCorrectPayload(t *testing.T) {
	var mu sync.Mutex
	var received webhookPayload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", r.Header.Get("Content-Type"))
		}
		mu.Lock()
		defer mu.Unlock()
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("failed to decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(srv.URL, testNotifyLogger())
	n.Notify(context.Background(), Event{
		MonitorID:   1,
		MonitorName: "Test Site",
		MonitorURL:  "https://example.com",
		Kind:        EventIncidentOpened,
		Cause:       "connection refused",
	})

	mu.Lock()
	defer mu.Unlock()
	if received.MonitorName != "Test Site" {
		t.Errorf("expected monitor_name %q, got %q", "Test Site", received.MonitorName)
	}
	if received.Kind != string(EventIncidentOpened) {
		t.Errorf("expected kind %q, got %q", EventIncidentOpened, received.Kind)
	}
	if received.Cause != "connection refused" {
		t.Errorf("expected cause %q, got %q", "connection refused", received.Cause)
	}
}

func TestWebhookNotifier_EmptyURLIsANoOp(t *testing.T) {
	// Must not panic or attempt any network call when unconfigured.
	n := NewWebhookNotifier("", testNotifyLogger())
	n.Notify(context.Background(), Event{MonitorID: 1, Kind: EventIncidentOpened})
}

func TestWebhookNotifier_ServerErrorDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// The important assertion here is implicit: this must return normally,
	// not panic, when the receiving endpoint fails — matching Notify's "no
	// error return" contract.
	n := NewWebhookNotifier(srv.URL, testNotifyLogger())
	n.Notify(context.Background(), Event{MonitorID: 1, Kind: EventIncidentOpened})
}

func TestWebhookNotifier_UnreachableURLDoesNotPanic(t *testing.T) {
	n := NewWebhookNotifier("http://127.0.0.1:1", testNotifyLogger())
	n.Notify(context.Background(), Event{MonitorID: 1, Kind: EventIncidentOpened})
}
