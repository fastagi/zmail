package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Mail gateway. protocol, result, reason, and command come from code-owned sets, so every label
// set stays bounded; no label ever carries a user, address, or mailbox.
var (
	mailGatewayConnections = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "zmail_mail_gateway_connections",
			Help: "Live mail app connections of this gateway process, by protocol",
		},
		[]string{"protocol"},
	)
	mailGatewayConnectionsRefused = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "zmail_mail_gateway_connections_refused_total",
			Help: "Mail app connections refused by a connection cap, by protocol",
		},
		[]string{"protocol"},
	)
	mailGatewayAuth = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "zmail_mail_gateway_auth_total",
			Help: "Mail app sign-ins, by protocol and result",
		},
		[]string{"protocol", "result"},
	)
	mailGatewaySweepClosed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "zmail_mail_gateway_sweep_closed_total",
			Help: "Mail app sessions the revocation sweep closed, by reason",
		},
		[]string{"reason"},
	)
	mailGatewayCommandSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "zmail_mail_gateway_command_seconds",
			Help:    "Time one mail app command took, by command",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"command"},
	)
	mailGatewaySubmissions = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "zmail_mail_gateway_smtp_submissions_total",
			Help: "Messages mail apps submitted over SMTP, by outcome",
		},
		[]string{"outcome"},
	)
)

// RecordMailGatewaySubmission counts a submitted message; outcome is sent, queued, error, or the
// kind of a refusal (domain.MailClientSubmitErrorKind).
func RecordMailGatewaySubmission(outcome string) {
	mailGatewaySubmissions.WithLabelValues(outcome).Inc()
}

// SetMailGatewayConnections sets the live connections of a protocol.
func SetMailGatewayConnections(protocol string, count int) {
	mailGatewayConnections.WithLabelValues(protocol).Set(float64(count))
}

// RecordMailGatewayConnectionRefused counts a connection a cap refused.
func RecordMailGatewayConnectionRefused(protocol string) {
	mailGatewayConnectionsRefused.WithLabelValues(protocol).Inc()
}

// RecordMailGatewayAuth counts a sign-in; result is success, failure, locked, limited, or error.
func RecordMailGatewayAuth(protocol, result string) {
	mailGatewayAuth.WithLabelValues(protocol, result).Inc()
}

// RecordMailGatewaySweepClosed counts sessions the sweep closed; reason is revoked, mailbox, or
// fail_closed.
func RecordMailGatewaySweepClosed(reason string, count int) {
	if count > 0 {
		mailGatewaySweepClosed.WithLabelValues(reason).Add(float64(count))
	}
}

// ObserveMailGatewayCommand observes how long a command took; command is an IMAP command name.
func ObserveMailGatewayCommand(command string, duration time.Duration) {
	mailGatewayCommandSeconds.WithLabelValues(command).Observe(duration.Seconds())
}
