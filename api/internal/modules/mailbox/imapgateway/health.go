package imapgateway

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HealthHandler serves /health, which answers 503 once the gateway is shutting down, and the
// process's Prometheus /metrics. Bind it to a private address.
func (s *Server) HealthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		s.mu.Lock()
		closing := s.closing
		s.mu.Unlock()
		status, body := http.StatusOK, `{"status":"ok"}`
		if closing {
			status, body = http.StatusServiceUnavailable, `{"status":"stopping"}`
		}
		w.WriteHeader(status)
		if _, err := w.Write([]byte(body)); err != nil {
			s.logger.Debug("imap_gateway.health_write_failed", "error", err)
		}
	})
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}
