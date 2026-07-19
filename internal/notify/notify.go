// Package notify sends alerts about incidents through one or more
// channels (email, webhook, ...). Following the exact same pattern as
// checker.ResultRecorder: a small interface, several implementations, and
// a composite that fans out to all of them without any one implementation
// knowing the others exist.
package notify

import (
	"context"
	"log/slog"

	"github.com/yourname/sitepulse/internal/domain"
)

// Event is what a Notifier is told about — deliberately a flat struct of
// already-resolved, human-readable fields (not a raw *domain.Incident and
// *domain.Monitor pair) so that every Notifier implementation (email,
// webhook, future providers) formats a message from the same simple
// source, without each one re-deriving "how long was the outage" or
// "what's this monitor's name" from raw domain types.
type Event struct {
	MonitorID   int64
	MonitorName string
	MonitorURL  string
	Kind        EventKind
	Cause       string        // populated for EventIncidentOpened
	Duration    string        // populated for EventIncidentResolved, human-readable (e.g. "4m32s")
}

type EventKind string

const (
	EventIncidentOpened   EventKind = "incident_opened"
	EventIncidentResolved EventKind = "incident_resolved"
	EventCertExpiringSoon EventKind = "cert_expiring_soon"
)

// Notifier is the interface every notification channel implements. Like
// checker.ResultRecorder, Notify has no error return — a failed webhook
// delivery or a bounced email must never crash or block the caller (the
// incident-detection code path). Implementations are responsible for
// logging their own failures.
type Notifier interface {
	Notify(ctx context.Context, event Event)
}

// LoggingNotifier is the safe, always-available default — genuinely useful
// on its own during development (see every alert in the log without
// needing real SMTP credentials or a webhook endpoint configured), and a
// reasonable fallback in production if email/webhook aren't configured.
type LoggingNotifier struct {
	log *slog.Logger
}

func NewLoggingNotifier(log *slog.Logger) *LoggingNotifier {
	return &LoggingNotifier{log: log}
}

func (n *LoggingNotifier) Notify(ctx context.Context, event Event) {
	n.log.Warn("notification",
		"kind", event.Kind,
		"monitor_id", event.MonitorID,
		"monitor_name", event.MonitorName,
		"cause", event.Cause,
		"duration", event.Duration,
	)
}

// MultiNotifier fans one event out to several notifiers, same pattern as
// checker.MultiRecorder — one channel failing (e.g. SMTP is down) never
// blocks or skips the others.
type MultiNotifier struct {
	notifiers []Notifier
}

func NewMultiNotifier(notifiers ...Notifier) *MultiNotifier {
	return &MultiNotifier{notifiers: notifiers}
}

func (n *MultiNotifier) Notify(ctx context.Context, event Event) {
	for _, notifier := range n.notifiers {
		notifier.Notify(ctx, event)
	}
}

// NewIncidentOpenedEvent and NewIncidentResolvedEvent build Events from
// domain types — centralizing this mapping here (rather than in each
// Notifier implementation) so "how do we describe an incident" is defined
// once, not reimplemented slightly differently by email vs. webhook.
func NewIncidentOpenedEvent(m *domain.Monitor, i *domain.Incident) Event {
	return Event{
		MonitorID:   m.ID,
		MonitorName: m.Name,
		MonitorURL:  m.URL,
		Kind:        EventIncidentOpened,
		Cause:       i.Cause,
	}
}

func NewIncidentResolvedEvent(m *domain.Monitor, i *domain.Incident, duration string) Event {
	return Event{
		MonitorID:   m.ID,
		MonitorName: m.Name,
		MonitorURL:  m.URL,
		Kind:        EventIncidentResolved,
		Duration:    duration,
	}
}

func NewCertExpiringSoonEvent(m *domain.Monitor, remaining string) Event {
	return Event{
		MonitorID:   m.ID,
		MonitorName: m.Name,
		MonitorURL:  m.URL,
		Kind:        EventCertExpiringSoon,
		Duration:    remaining,
	}
}
