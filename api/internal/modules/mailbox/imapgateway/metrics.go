package imapgateway

import (
	"time"

	"github.com/fastagi/zmail/api/internal/infra/metrics"
)

const protocolIMAP = "imap"

func recordConnections(count int) { metrics.SetMailGatewayConnections(protocolIMAP, count) }
func recordConnectionRefused()    { metrics.RecordMailGatewayConnectionRefused(protocolIMAP) }
func recordAuth(result string)    { metrics.RecordMailGatewayAuth(protocolIMAP, result) }

// observeCommand times a command from start.
func observeCommand(command string, start time.Time) {
	metrics.ObserveMailGatewayCommand(command, time.Since(start))
}

func recordSweepClosed(reason string, n int) { metrics.RecordMailGatewaySweepClosed(reason, n) }
