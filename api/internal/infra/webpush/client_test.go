package webpush

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendClassifiesPushServiceResponses(t *testing.T) {
	cases := map[int]Result{201: Delivered, 404: Gone, 410: Gone, 429: Retry, 503: Retry, 400: Rejected, 413: Rejected}
	for status, want := range cases {
		var gotHeaders http.Header
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotHeaders = r.Header.Clone()
			w.WriteHeader(status)
		}))
		keys, _ := GenerateVAPIDKeys()
		vapid, err := NewVAPID(keys.PublicKey, keys.PrivateKey, "mailto:ops@zmail.im")
		if err != nil {
			t.Fatal(err)
		}
		client := NewClient(vapid, server.Client())
		got, _ := client.Send(context.Background(), Message{
			Endpoint: server.URL + "/push/abc",
			Keys:     Keys{P256dh: rfcUAPublic, Auth: rfcAuth},
			Payload:  []byte(`{"title":"New mail"}`),
			Topic:    "mail-42",
			Urgency:  "high",
		})
		server.Close()
		if got != want {
			t.Fatalf("status %d: result %v, want %v", status, got, want)
		}
		if gotHeaders.Get("Content-Encoding") != "aes128gcm" || gotHeaders.Get("TTL") != "86400" ||
			gotHeaders.Get("Topic") != "mail-42" || gotHeaders.Get("Urgency") != "high" ||
			!strings.HasPrefix(gotHeaders.Get("Authorization"), "vapid t=") {
			t.Fatalf("status %d: headers %v", status, gotHeaders)
		}
	}
}

func TestSendRefusesNonHTTPSEndpoints(t *testing.T) {
	keys, _ := GenerateVAPIDKeys()
	vapid, _ := NewVAPID(keys.PublicKey, keys.PrivateKey, "mailto:ops@zmail.im")
	got, err := NewClient(vapid, nil).Send(context.Background(), Message{
		Endpoint: "http://127.0.0.1/push", Keys: Keys{P256dh: rfcUAPublic, Auth: rfcAuth}, Payload: []byte("x"),
	})
	if got != Rejected || err == nil {
		t.Fatalf("result %v err %v", got, err)
	}
}

func TestAllowedEndpointOnlyAcceptsBrowserPushServices(t *testing.T) {
	allowed := []string{
		"https://fcm.googleapis.com/fcm/send/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/abc",
		"https://web.push.apple.com/QGx",
		"https://wns2-par02p.notify.windows.com/w/?token=abc",
	}
	for _, endpoint := range allowed {
		if !AllowedEndpoint(endpoint) {
			t.Fatalf("expected %q to be allowed", endpoint)
		}
	}
	refused := []string{
		"http://fcm.googleapis.com/fcm/send/abc",
		"https://evilfcm.googleapis.com.attacker.test/x",
		"https://fcm.googleapis.com.attacker.test/x",
		"https://127.0.0.1/push",
		"https://user@fcm.googleapis.com/x",
		"https://fcm.googleapis.com:8443/x",
		"https://attackerpush.apple.com/x",
		"",
	}
	for _, endpoint := range refused {
		if AllowedEndpoint(endpoint) {
			t.Fatalf("expected %q to be refused", endpoint)
		}
	}
}
