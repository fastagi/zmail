package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// External mail sync. provider is a connector id from the code-owned registry and outcome one of
// the six sync outcomes, so both label sets stay bounded.
var (
	externalMailSyncsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "zmail_external_mail_syncs_total",
			Help: "Fetches of external mail connections, by provider and outcome",
		},
		[]string{"provider", "outcome"},
	)
	externalMailSyncSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "zmail_external_mail_sync_seconds",
			Help:    "Time one fetch of an external mail connection took, by provider",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
		},
		[]string{"provider"},
	)
	externalMailPushNotificationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "zmail_external_mail_push_notifications_total",
			Help: "Push notification requests of external mail providers, by provider and result",
		},
		[]string{"provider", "result"},
	)
	externalMailPushDelaySeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "zmail_external_mail_push_delay_seconds",
			Help:    "Time from a push notification to the start of the sync it triggered, by provider",
			Buckets: []float64{0.5, 1, 2.5, 5, 10, 20, 30, 60, 120, 300},
		},
		[]string{"provider"},
	)
	externalMailCopiesPurgedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "zmail_external_mail_copies_purged_total",
			Help: "Synced copies of revoked external mail connections deleted",
		},
	)
)

// RecordExternalMailSync counts one fetch of a connection and observes how long it took.
func RecordExternalMailSync(provider, outcome string, duration time.Duration) {
	externalMailSyncsTotal.WithLabelValues(provider, outcome).Inc()
	externalMailSyncSeconds.WithLabelValues(provider).Observe(duration.Seconds())
}

// RecordExternalMailCopiesPurged counts copies deleted after their connection was revoked.
func RecordExternalMailCopiesPurged(count int) {
	if count > 0 {
		externalMailCopiesPurgedTotal.Add(float64(count))
	}
}

// RecordExternalMailPushNotification counts one notification request. result is one of a fixed set
// (accepted, ignored, validation, unauthenticated, malformed, rate_limited).
func RecordExternalMailPushNotification(provider, result string) {
	externalMailPushNotificationsTotal.WithLabelValues(provider, result).Inc()
}

// ObserveExternalMailPushDelay observes how long a notification waited for its sync to start.
func ObserveExternalMailPushDelay(provider string, delay time.Duration) {
	if delay >= 0 {
		externalMailPushDelaySeconds.WithLabelValues(provider).Observe(delay.Seconds())
	}
}
