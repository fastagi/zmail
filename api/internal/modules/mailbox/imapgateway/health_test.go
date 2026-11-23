package imapgateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthHandler(t *testing.T) {
	server := NewServer(newFakeStore(), Config{}, nil)
	handler := server.HealthHandler()
	get := func(path string) (int, string) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		body, _ := io.ReadAll(recorder.Result().Body)
		return recorder.Code, string(body)
	}
	if code, body := get("/health"); code != http.StatusOK || !strings.Contains(body, `"ok"`) {
		t.Fatalf("/health = %d %s", code, body)
	}
	recordAuth("success")
	if code, body := get("/metrics"); code != http.StatusOK || !strings.Contains(body, "zmail_mail_gateway_auth_total") {
		t.Fatalf("/metrics = %d", code)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if code, _ := get("/health"); code != http.StatusServiceUnavailable {
		t.Fatalf("/health while stopping = %d", code)
	}
}
