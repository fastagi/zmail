package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

func TestMiddlewareBoundsUnmatchedRouteLabels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware())

	paths := []string{
		"/missing/metrics-cardinality-first",
		"/missing/metrics-cardinality-second",
	}
	for _, path := range paths {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want %d", path, response.Code, http.StatusNotFound)
		}
	}

	labels := metricPathLabels(t, "http_requests_total")
	for _, path := range paths {
		if labels[path] {
			t.Fatalf("unmatched raw path %q was recorded as a metric label", path)
		}
	}
	if !labels["unmatched"] {
		t.Fatalf("metric path labels = %v, want bounded unmatched label", labels)
	}
}

func TestHTTPStatusLabel(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		want   string
	}{
		{name: "common", status: http.StatusNoContent, want: "204"},
		{name: "error", status: http.StatusUnprocessableEntity, want: "422"},
		{name: "custom", status: 700, want: "700"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := httpStatusLabel(test.status); got != test.want {
				t.Fatalf("httpStatusLabel(%d) = %q, want %q", test.status, got, test.want)
			}
		})
	}
}

func metricPathLabels(t *testing.T, metricName string) map[string]bool {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	labels := make(map[string]bool)
	for _, family := range families {
		if family.GetName() != metricName {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "path" {
					labels[label.GetValue()] = true
				}
			}
		}
	}
	return labels
}

// TestTransactionalMailMetricsAreRegistered checks the transactional mail metrics of spec §15
// exist under their names with their bounded labels.
func TestTransactionalMailMetricsAreRegistered(t *testing.T) {
	RecordTransactionalMessage("live", "sent")
	ObserveTransactionalDispatch(1500 * time.Millisecond)
	SetTransactionalQueueOldest(42)
	RecordSendLaneAttempt("transactional")
	RecordTransactionalRateLimited("mail.transactional.recipient")

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	want := map[string][]string{
		"zmail_transactional_messages_total":       {"mode", "outcome"},
		"zmail_transactional_dispatch_seconds":     nil,
		"zmail_transactional_queue_oldest_seconds": nil,
		"zmail_send_lane_attempts_total":           {"lane"},
		"zmail_transactional_rate_limited_total":   {"rule"},
	}
	for _, family := range families {
		labels, ok := want[family.GetName()]
		if !ok {
			continue
		}
		delete(want, family.GetName())
		got := map[string]bool{}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				got[label.GetName()] = true
			}
		}
		if len(got) != len(labels) {
			t.Fatalf("%s labels = %v; want %v", family.GetName(), got, labels)
		}
		for _, label := range labels {
			if !got[label] {
				t.Fatalf("%s labels = %v; want %v", family.GetName(), got, labels)
			}
		}
	}
	if len(want) > 0 {
		t.Fatalf("metrics not registered: %v", want)
	}
}
