package notification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/pkg/response"
)

func newWebPushTestRouter(t *testing.T, handler *Handler, userID uint) *gin.Engine {
	t.Helper()
	handler.RegisterErrorMappings(response.DefaultErrorMapper)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	authed := func(next gin.HandlerFunc) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Set("userID", userID)
			next(c)
		}
	}
	router.GET("/web-push-config", authed(handler.GetWebPushConfig))
	router.GET("/web-push-subscriptions", authed(handler.ListWebPushSubscriptions))
	router.PUT("/web-push-subscriptions", authed(handler.PutWebPushSubscription))
	router.DELETE("/web-push-subscriptions/:id", authed(handler.DeleteWebPushSubscription))
	router.GET("/notification-preferences", authed(handler.GetPreference))
	router.PUT("/notification-preferences", authed(handler.ReplacePreference))
	return router
}

func serveJSON(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestWebPushHTTPShapes(t *testing.T) {
	service, db, _, _ := newWebPushTestService(t)
	userID := createNotificationTestUser(t, db, "alice")
	router := newWebPushTestRouter(t, NewHandler(service), userID)

	config := serveJSON(router, http.MethodGet, "/web-push-config", "")
	require.Equal(t, http.StatusOK, config.Code)
	assert.JSONEq(t, `{"enabled":true,"public_key":"test-public-key"}`, string(dataOf(t, config)))

	input := testSubscriptionInput(t, pushEndpoint("http"))
	body := fmt.Sprintf(`{"endpoint":%q,"keys":{"p256dh":%q,"auth":%q},"device_label":"Phone"}`,
		input.Endpoint, input.P256dh, input.Auth)
	created := serveJSON(router, http.MethodPut, "/web-push-subscriptions", body)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	var subscription map[string]any
	require.NoError(t, json.Unmarshal(dataOf(t, created), &subscription))
	assert.ElementsMatch(t, []string{"id", "device_label", "created_at", "last_success_at"}, keysOf(subscription))
	assert.Equal(t, "Phone", subscription["device_label"])
	assert.Nil(t, subscription["last_success_at"])

	list := serveJSON(router, http.MethodGet, "/web-push-subscriptions", "")
	require.Equal(t, http.StatusOK, list.Code)
	var items []map[string]any
	require.NoError(t, json.Unmarshal(dataOf(t, list), &items))
	require.Len(t, items, 1)
	assert.NotContains(t, list.Body.String(), input.Endpoint)
	assert.NotContains(t, list.Body.String(), input.P256dh)

	badEndpoint := serveJSON(router, http.MethodPut, "/web-push-subscriptions",
		fmt.Sprintf(`{"endpoint":"https://evil.example.com/x","keys":{"p256dh":%q,"auth":%q}}`, input.P256dh, input.Auth))
	assert.Equal(t, http.StatusUnprocessableEntity, badEndpoint.Code)
	assert.Equal(t, CodeWebPushEndpointInvalid, errorCodeOf(t, badEndpoint))

	badKeys := serveJSON(router, http.MethodPut, "/web-push-subscriptions",
		fmt.Sprintf(`{"endpoint":%q,"keys":{"p256dh":"AAAA","auth":%q}}`, input.Endpoint, input.Auth))
	assert.Equal(t, http.StatusUnprocessableEntity, badKeys.Code)
	assert.Equal(t, CodeWebPushKeysInvalid, errorCodeOf(t, badKeys))

	preference := serveJSON(router, http.MethodPut, "/notification-preferences",
		`{"in_app_enabled":true,"email_enabled":false}`)
	require.Equal(t, http.StatusOK, preference.Code)
	assert.JSONEq(t, `{"in_app_enabled":true,"email_enabled":false,"web_push_enabled":true}`, string(dataOf(t, preference)))

	id := uint(subscription["id"].(float64))
	missing := serveJSON(router, http.MethodDelete, fmt.Sprintf("/web-push-subscriptions/%d", id+100), "")
	assert.Equal(t, http.StatusNotFound, missing.Code)
	assert.Equal(t, CodeWebPushSubscriptionNotFound, errorCodeOf(t, missing))
	deleted := serveJSON(router, http.MethodDelete, fmt.Sprintf("/web-push-subscriptions/%d", id), "")
	assert.Equal(t, http.StatusNoContent, deleted.Code)

	service.push = nil
	disabledConfig := serveJSON(router, http.MethodGet, "/web-push-config", "")
	assert.JSONEq(t, `{"enabled":false,"public_key":""}`, string(dataOf(t, disabledConfig)))
	disabled := serveJSON(router, http.MethodPut, "/web-push-subscriptions", body)
	assert.Equal(t, http.StatusServiceUnavailable, disabled.Code)
	assert.Equal(t, CodeWebPushDisabled, errorCodeOf(t, disabled))
}

func dataOf(t *testing.T, recorder *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	return envelope.Data
}

func errorCodeOf(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		ErrorCode string `json:"error_code"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	return envelope.ErrorCode
}

func keysOf(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	return keys
}
