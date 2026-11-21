package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// TestExternalMailMetricsAreRegistered checks the external mail sync metrics exist under their
// names with their bounded labels.
func TestExternalMailMetricsAreRegistered(t *testing.T) {
	RecordExternalMailSync("gmail", "ok", 300*time.Millisecond)
	RecordExternalMailCopiesPurged(3)

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	want := map[string]int{
		"zmail_external_mail_syncs_total":         2,
		"zmail_external_mail_sync_seconds":        1,
		"zmail_external_mail_copies_purged_total": 0,
	}
	for _, family := range families {
		labels, ok := want[family.GetName()]
		if !ok {
			continue
		}
		delete(want, family.GetName())
		if got := len(family.GetMetric()[0].GetLabel()); got != labels {
			t.Fatalf("%s has %d labels, want %d", family.GetName(), got, labels)
		}
	}
	if len(want) > 0 {
		t.Fatalf("metrics not registered: %v", want)
	}
}
