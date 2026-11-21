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
