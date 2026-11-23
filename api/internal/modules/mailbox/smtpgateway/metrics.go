package smtpgateway

import (
	"time"

	"github.com/fastagi/zmail/api/internal/infra/metrics"
)

const protocolSMTP = "smtp"

func recordConnections(count int) { metrics.SetMailGatewayConnections(protocolSMTP, count) }
func recordConnectionRefused()    { metrics.RecordMailGatewayConnectionRefused(protocolSMTP) }
func recordAuth(result string)    { metrics.RecordMailGatewayAuth(protocolSMTP, result) }
func recordSubmission(outcome string) {
	metrics.RecordMailGatewaySubmission(outcome)
}

// observeSubmission times the store's send of one message.
func observeSubmission(start time.Time) {
	metrics.ObserveMailGatewayCommand("SMTP DATA", time.Since(start))
}
