package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net/smtp"
)

// EmailConfig holds SMTP connection details. An empty Host means email
// notifications are disabled — the notifier becomes a safe no-op rather
// than the app requiring SMTP credentials just to start up, which matters
// for local development and for anyone trying this project without
// setting up a mail account first.
type EmailConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
	To       string
}

type EmailNotifier struct {
	cfg EmailConfig
	log *slog.Logger
}

func NewEmailNotifier(cfg EmailConfig, log *slog.Logger) *EmailNotifier {
	return &EmailNotifier{cfg: cfg, log: log}
}

// Notify sends a plain-text email. Two honest limitations worth stating
// rather than hiding:
//
//  1. net/smtp.SendMail predates context.Context in the standard library
//     and has no way to accept one — there's no way to cancel or bound
//     this call via ctx the way every other I/O in this project does. A
//     production system sending high volumes of email would reach for a
//     dedicated library (or an HTTP-based provider API, which DOES accept
//     context) specifically to fix this gap.
//  2. There's no retry here. A transient SMTP failure means that one
//     notification is simply lost. Given Notify already can't return an
//     error to its caller (by design — see the Notifier interface doc),
//     a dropped notification fails silently except for the log line
//     below. Acceptable for a portfolio project's alerting; a production
//     system would want at least a bounded retry, ideally a durable queue
//     so "notify the owner" survives an SMTP hiccup.
func (n *EmailNotifier) Notify(ctx context.Context, event Event) {
	if n.cfg.Host == "" {
		return // email notifications not configured — silent no-op, not an error
	}

	subject, body := formatEmail(event)
	msg := buildEmailMessage(n.cfg.From, n.cfg.To, subject, body)

	auth := smtp.PlainAuth("", n.cfg.Username, n.cfg.Password, n.cfg.Host)
	addr := n.cfg.Host + ":" + n.cfg.Port

	if err := smtp.SendMail(addr, auth, n.cfg.From, []string{n.cfg.To}, []byte(msg)); err != nil {
		n.log.Error("email notification failed", "monitor_id", event.MonitorID, "error", err)
		return
	}
	n.log.Info("email notification sent", "monitor_id", event.MonitorID, "kind", event.Kind)
}

func formatEmail(event Event) (subject, body string) {
	switch event.Kind {
	case EventIncidentOpened:
		subject = fmt.Sprintf("[SitePulse] %s is DOWN", event.MonitorName)
		body = fmt.Sprintf("%s (%s) is currently unreachable.\n\nReason: %s\n", event.MonitorName, event.MonitorURL, event.Cause)
	case EventIncidentResolved:
		subject = fmt.Sprintf("[SitePulse] %s is back UP", event.MonitorName)
		body = fmt.Sprintf("%s (%s) has recovered. Downtime: %s\n", event.MonitorName, event.MonitorURL, event.Duration)
	case EventCertExpiringSoon:
		subject = fmt.Sprintf("[SitePulse] TLS certificate for %s expires soon", event.MonitorName)
		body = fmt.Sprintf("The TLS certificate for %s (%s) expires in %s. Renew it before it lapses.\n", event.MonitorName, event.MonitorURL, event.Duration)
	default:
		subject = "[SitePulse] Notification"
		body = fmt.Sprintf("%+v", event)
	}
	return subject, body
}

// buildEmailMessage constructs a minimal RFC 5322 message by hand rather
// than reaching for a MIME library — the message here is always
// plain-text, single-part, no attachments, so the handful of required
// headers (From/To/Subject/blank-line-then-body) are simple enough to not
// warrant a dependency.
func buildEmailMessage(from, to, subject, body string) string {
	return fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s", from, to, subject, body)
}
