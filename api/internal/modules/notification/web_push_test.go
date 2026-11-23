package notification

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/webpush"
)

type fakeWebPushSender struct {
	mu       sync.Mutex
	results  map[string][]webpush.Result
	messages []webpush.Message
}

func (f *fakeWebPushSender) PublicKey() string { return "test-public-key" }

func (f *fakeWebPushSender) Send(_ context.Context, message webpush.Message) (webpush.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, message)
	queue := f.results[message.Endpoint]
	if len(queue) == 0 {
		return webpush.Delivered, nil
	}
	result := queue[0]
	f.results[message.Endpoint] = queue[1:]
	if result == webpush.Delivered || result == webpush.Gone {
		return result, nil
	}
	return result, errors.New("push service refused")
}

func (f *fakeWebPushSender) sent() []webpush.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]webpush.Message(nil), f.messages...)
}

func newWebPushTestService(t *testing.T) (*service, *gorm.DB, *fakeWebPushSender, time.Time) {
	t.Helper()
	service, db, _, now := newNotificationTestService(t)
	push := &fakeWebPushSender{results: map[string][]webpush.Result{}}
	service.push = push
	return service, db, push, now
}

func testSubscriptionInput(t *testing.T, endpoint string) WebPushSubscriptionInput {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	return WebPushSubscriptionInput{
		Endpoint:    endpoint,
		P256dh:      base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		Auth:        base64.RawURLEncoding.EncodeToString(auth),
		DeviceLabel: "Chrome on Mac",
	}
}

func pushEndpoint(name string) string {
	return "https://fcm.googleapis.com/fcm/send/" + name
}

func pushPublication(userID uint, key string) domain.NotificationPublication {
	return domain.NotificationPublication{
		UserID:         userID,
		IdempotencyKey: key,
		Kind:           "mail.received",
		Title:          "Ada Lovelace",
		Body:           "Quarterly numbers",
		ActionURL:      "/console/mailboxes?org=1&mailbox=2&open=3",
		Channels: []domain.NotificationChannel{
			domain.NotificationChannelInApp,
			domain.NotificationChannelWebPush,
		},
	}
}

func deliveryChannels(t *testing.T, db *gorm.DB, notificationID uint) []string {
	t.Helper()
	var channels []string
	require.NoError(t, db.Model(&NotificationDeliveryPO{}).
		Where("notification_id = ?", notificationID).
		Order("channel ASC").
		Pluck("channel", &channels).Error)
	return channels
}

func TestWebPushIsSelectedOnlyWithASubscriptionAndThePreference(t *testing.T) {
	service, db, _, _ := newWebPushTestService(t)
	ctx := context.Background()
	userID := createNotificationTestUser(t, db, "alice")

	without, err := service.Publish(ctx, pushPublication(userID, "no-subscription"))
	require.NoError(t, err)
	assert.Equal(t, []string{"in_app"}, deliveryChannels(t, db, without.ID))

	required := pushPublication(userID, "required-no-subscription")
	required.RequiredChannels = []domain.NotificationChannel{domain.NotificationChannelWebPush}
	requiredWithout, err := service.Publish(ctx, required)
	require.NoError(t, err)
	assert.Equal(t, []string{"in_app"}, deliveryChannels(t, db, requiredWithout.ID))

	_, err = service.RegisterWebPushSubscription(ctx, userID, testSubscriptionInput(t, pushEndpoint("alice")))
	require.NoError(t, err)
	with, err := service.Publish(ctx, pushPublication(userID, "with-subscription"))
	require.NoError(t, err)
	assert.Equal(t, []string{"in_app", "web_push"}, deliveryChannels(t, db, with.ID))
	var pushDelivery NotificationDeliveryPO
	require.NoError(t, db.Where("notification_id = ? AND channel = ?", with.ID, "web_push").First(&pushDelivery).Error)
	assert.Equal(t, string(deliveryStatusPending), pushDelivery.Status)

	off := false
	_, err = service.ReplacePreference(ctx, userID, true, true, &off)
	require.NoError(t, err)
	disabled, err := service.Publish(ctx, pushPublication(userID, "push-disabled"))
	require.NoError(t, err)
	assert.Equal(t, []string{"in_app"}, deliveryChannels(t, db, disabled.ID))

	requiredDisabled := pushPublication(userID, "push-required")
	requiredDisabled.RequiredChannels = []domain.NotificationChannel{domain.NotificationChannelWebPush}
	forced, err := service.Publish(ctx, requiredDisabled)
	require.NoError(t, err)
	assert.Equal(t, []string{"in_app", "web_push"}, deliveryChannels(t, db, forced.ID))
}

func TestWebPushPreferenceDefaultsOnAndPartialReplaceKeepsIt(t *testing.T) {
	service, db, _, _ := newWebPushTestService(t)
	ctx := context.Background()
	userID := createNotificationTestUser(t, db, "alice")

	preference, err := service.Preference(ctx, userID)
	require.NoError(t, err)
	assert.True(t, preference.WebPushEnabled)

	off := false
	preference, err = service.ReplacePreference(ctx, userID, true, false, &off)
	require.NoError(t, err)
	assert.False(t, preference.WebPushEnabled)

	preference, err = service.ReplacePreference(ctx, userID, false, true, nil)
	require.NoError(t, err)
	assert.False(t, preference.WebPushEnabled, "an omitted web_push_enabled keeps the stored value")
	assert.False(t, preference.InAppEnabled)
	stored, err := service.Preference(ctx, userID)
	require.NoError(t, err)
	assert.False(t, stored.WebPushEnabled)
	assert.True(t, stored.EmailEnabled)
}

func TestWebPushConfigReflectsTheSender(t *testing.T) {
	service, _, _, _ := newWebPushTestService(t)
	assert.Equal(t, WebPushConfig{Enabled: true, PublicKey: "test-public-key"}, service.WebPushConfig())
	service.push = nil
	assert.Equal(t, WebPushConfig{}, service.WebPushConfig())
}

func TestRegisterWebPushSubscriptionValidatesAndUpserts(t *testing.T) {
	service, db, _, now := newWebPushTestService(t)
	ctx := context.Background()
	aliceID := createNotificationTestUser(t, db, "alice")
	bobID := createNotificationTestUser(t, db, "bob")

	input := testSubscriptionInput(t, pushEndpoint("shared-browser"))
	first, err := service.RegisterWebPushSubscription(ctx, aliceID, input)
	require.NoError(t, err)
	assert.Equal(t, "Chrome on Mac", first.DeviceLabel)
	assert.WithinDuration(t, now, first.CreatedAt, time.Millisecond)
	assert.Nil(t, first.LastSuccessAt)

	input.DeviceLabel = "  Work laptop  "
	again, err := service.RegisterWebPushSubscription(ctx, aliceID, input)
	require.NoError(t, err)
	assert.Equal(t, first.ID, again.ID)
	assert.Equal(t, "Work laptop", again.DeviceLabel)

	moved, err := service.RegisterWebPushSubscription(ctx, bobID, input)
	require.NoError(t, err)
	assert.Equal(t, first.ID, moved.ID)
	aliceList, err := service.ListWebPushSubscriptions(ctx, aliceID)
	require.NoError(t, err)
	assert.Empty(t, aliceList)
	bobList, err := service.ListWebPushSubscriptions(ctx, bobID)
	require.NoError(t, err)
	require.Len(t, bobList, 1)

	var stored NotificationWebPushSubscriptionPO
	require.NoError(t, db.First(&stored, first.ID).Error)
	assert.Equal(t, hashEndpoint(input.Endpoint), stored.EndpointHash)
	assert.Equal(t, bobID, stored.UserID)

	invalid := []struct {
		name   string
		mutate func(*WebPushSubscriptionInput)
		target error
	}{
		{"unknown push service", func(v *WebPushSubscriptionInput) { v.Endpoint = "https://evil.example.com/push" }, ErrWebPushEndpointInvalid},
		{"plain http", func(v *WebPushSubscriptionInput) { v.Endpoint = "http://fcm.googleapis.com/fcm/send/x" }, ErrWebPushEndpointInvalid},
		{"short p256dh", func(v *WebPushSubscriptionInput) {
			v.P256dh = base64.RawURLEncoding.EncodeToString(make([]byte, 33))
		}, ErrWebPushKeysInvalid},
		{"p256dh not on the curve", func(v *WebPushSubscriptionInput) {
			point := make([]byte, 65)
			point[0] = 4
			v.P256dh = base64.RawURLEncoding.EncodeToString(point)
		}, ErrWebPushKeysInvalid},
		{"auth of 12 bytes", func(v *WebPushSubscriptionInput) {
			v.Auth = base64.RawURLEncoding.EncodeToString(make([]byte, 12))
		}, ErrWebPushKeysInvalid},
		{"auth not base64", func(v *WebPushSubscriptionInput) { v.Auth = "!!!" }, ErrWebPushKeysInvalid},
		{"label too long", func(v *WebPushSubscriptionInput) { v.DeviceLabel = strings.Repeat("x", 121) }, domain.ErrInvalidInput},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			value := testSubscriptionInput(t, pushEndpoint("invalid"))
			test.mutate(&value)
			_, err := service.RegisterWebPushSubscription(ctx, aliceID, value)
			assert.ErrorIs(t, err, test.target)
		})
	}

	service.push = nil
	_, err = service.RegisterWebPushSubscription(ctx, aliceID, testSubscriptionInput(t, pushEndpoint("off")))
	assert.ErrorIs(t, err, ErrWebPushDisabled)
}

func TestRegisterWebPushSubscriptionReplacesTheOldestBeyondTheCap(t *testing.T) {
	service, db, _, now := newWebPushTestService(t)
	ctx := context.Background()
	userID := createNotificationTestUser(t, db, "alice")

	var firstID uint
	for index := range maxWebPushSubscriptionsPerUser + 1 {
		current := now.Add(time.Duration(index) * time.Second)
		service.now = func() time.Time { return current }
		subscription, err := service.RegisterWebPushSubscription(ctx, userID,
			testSubscriptionInput(t, pushEndpoint(fmt.Sprintf("device-%d", index))))
		require.NoError(t, err)
		if index == 0 {
			firstID = subscription.ID
		}
	}
	list, err := service.ListWebPushSubscriptions(ctx, userID)
	require.NoError(t, err)
	assert.Len(t, list, maxWebPushSubscriptionsPerUser)
	for _, item := range list {
		assert.NotEqual(t, firstID, item.ID)
	}
}

func TestDeleteWebPushSubscriptionOnlyRemovesOwnRows(t *testing.T) {
	service, db, _, _ := newWebPushTestService(t)
	ctx := context.Background()
	aliceID := createNotificationTestUser(t, db, "alice")
	bobID := createNotificationTestUser(t, db, "bob")
	subscription, err := service.RegisterWebPushSubscription(ctx, aliceID, testSubscriptionInput(t, pushEndpoint("alice")))
	require.NoError(t, err)

	assert.ErrorIs(t, service.DeleteWebPushSubscription(ctx, bobID, subscription.ID), ErrWebPushSubscriptionNotFound)
	require.NoError(t, service.DeleteWebPushSubscription(ctx, aliceID, subscription.ID))
	assert.ErrorIs(t, service.DeleteWebPushSubscription(ctx, aliceID, subscription.ID), ErrWebPushSubscriptionNotFound)
}

func publishWithPush(t *testing.T, service *service, userID uint, key string) *domain.Notification {
	t.Helper()
	notification, err := service.Publish(context.Background(), pushPublication(userID, key))
	require.NoError(t, err)
	return notification
}

func webPushDelivery(t *testing.T, db *gorm.DB, notificationID uint) NotificationDeliveryPO {
	t.Helper()
	var delivery NotificationDeliveryPO
	require.NoError(t, db.Where("notification_id = ? AND channel = ?", notificationID, "web_push").First(&delivery).Error)
	return delivery
}

func TestDispatchWebPushSendsToEverySubscriptionAndCleansUp(t *testing.T) {
	service, db, push, now := newWebPushTestService(t)
	ctx := context.Background()
	userID := createNotificationTestUser(t, db, "alice")
	kept, err := service.RegisterWebPushSubscription(ctx, userID, testSubscriptionInput(t, pushEndpoint("kept")))
	require.NoError(t, err)
	gone, err := service.RegisterWebPushSubscription(ctx, userID, testSubscriptionInput(t, pushEndpoint("gone")))
	require.NoError(t, err)
	rejected, err := service.RegisterWebPushSubscription(ctx, userID, testSubscriptionInput(t, pushEndpoint("rejected")))
	require.NoError(t, err)
	push.results[pushEndpoint("gone")] = []webpush.Result{webpush.Gone}
	push.results[pushEndpoint("rejected")] = []webpush.Result{webpush.Rejected}
	notification := publishWithPush(t, service, userID, "mail.received:1")

	processed, err := service.DispatchDue(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	delivery := webPushDelivery(t, db, notification.ID)
	assert.Equal(t, string(deliveryStatusDelivered), delivery.Status)
	assert.Equal(t, uint8(1), delivery.Attempts)

	messages := push.sent()
	require.Len(t, messages, 3)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(messages[0].Payload, &payload))
	assert.Equal(t, map[string]any{
		"title": "Ada Lovelace",
		"body":  "Quarterly numbers",
		"url":   "/console/mailboxes?org=1&mailbox=2&open=3",
		"tag":   fmt.Sprintf("n-%d", notification.ID),
	}, payload)
	assert.Equal(t, fmt.Sprintf("n-%d", notification.ID), messages[0].Topic)

	var rows []NotificationWebPushSubscriptionPO
	require.NoError(t, db.Order("id ASC").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, kept.ID, rows[0].ID)
	require.NotNil(t, rows[0].LastSuccessAt)
	assert.WithinDuration(t, now, *rows[0].LastSuccessAt, time.Millisecond)
	assert.Equal(t, rejected.ID, rows[1].ID)
	assert.Equal(t, int16(1), rows[1].FailureCount)
	assert.NotEqual(t, gone.ID, rows[1].ID)
}

func TestDispatchWebPushDropsASubscriptionRejectedThreeTimes(t *testing.T) {
	service, db, push, _ := newWebPushTestService(t)
	ctx := context.Background()
	userID := createNotificationTestUser(t, db, "alice")
	_, err := service.RegisterWebPushSubscription(ctx, userID, testSubscriptionInput(t, pushEndpoint("bad")))
	require.NoError(t, err)
	push.results[pushEndpoint("bad")] = []webpush.Result{webpush.Rejected, webpush.Rejected, webpush.Rejected}

	for round := range 3 {
		notification := publishWithPush(t, service, userID, fmt.Sprintf("round-%d", round))
		_, err := service.DispatchDue(ctx, 10)
		require.NoError(t, err)
		delivery := webPushDelivery(t, db, notification.ID)
		assert.Equal(t, string(deliveryStatusFailed), delivery.Status)
		assert.Equal(t, failureWebPushRejected, delivery.LastFailureCode)
	}
	var count int64
	require.NoError(t, db.Model(&NotificationWebPushSubscriptionPO{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestDispatchWebPushRetriesTransientFailuresThenGivesUp(t *testing.T) {
	service, db, push, now := newWebPushTestService(t)
	ctx := context.Background()
	userID := createNotificationTestUser(t, db, "alice")
	_, err := service.RegisterWebPushSubscription(ctx, userID, testSubscriptionInput(t, pushEndpoint("flaky")))
	require.NoError(t, err)
	retries := make([]webpush.Result, maxDeliveryAttempts)
	for index := range retries {
		retries[index] = webpush.Retry
	}
	push.results[pushEndpoint("flaky")] = retries
	notification := publishWithPush(t, service, userID, "flaky")

	current := now
	for attempt := uint8(1); attempt <= maxDeliveryAttempts; attempt++ {
		service.now = func() time.Time { return current }
		processed, err := service.DispatchDue(ctx, 10)
		require.NoError(t, err)
		assert.Equal(t, 1, processed)
		delivery := webPushDelivery(t, db, notification.ID)
		assert.Equal(t, attempt, delivery.Attempts)
		if attempt < maxDeliveryAttempts {
			assert.Equal(t, string(deliveryStatusPending), delivery.Status)
			assert.Equal(t, failureWebPushUnavailable, delivery.LastFailureCode)
		} else {
			assert.Equal(t, string(deliveryStatusFailed), delivery.Status)
			assert.Equal(t, failureWebPushRetryExhausted, delivery.LastFailureCode)
		}
		current = current.Add(time.Hour)
	}
	var subscription NotificationWebPushSubscriptionPO
	require.NoError(t, db.First(&subscription).Error)
	assert.Zero(t, subscription.FailureCount, "a transient failure never counts against the subscription")
}

func TestDispatchWebPushFailsWithoutSubscriptionsOrSender(t *testing.T) {
	service, db, _, _ := newWebPushTestService(t)
	ctx := context.Background()
	userID := createNotificationTestUser(t, db, "alice")
	subscription, err := service.RegisterWebPushSubscription(ctx, userID, testSubscriptionInput(t, pushEndpoint("removed")))
	require.NoError(t, err)
	orphan := publishWithPush(t, service, userID, "orphan")
	require.NoError(t, service.DeleteWebPushSubscription(ctx, userID, subscription.ID))
	_, err = service.DispatchDue(ctx, 10)
	require.NoError(t, err)
	delivery := webPushDelivery(t, db, orphan.ID)
	assert.Equal(t, string(deliveryStatusFailed), delivery.Status)
	assert.Equal(t, failureWebPushNoSubscriptions, delivery.LastFailureCode)

	_, err = service.RegisterWebPushSubscription(ctx, userID, testSubscriptionInput(t, pushEndpoint("unconfigured")))
	require.NoError(t, err)
	unconfigured := publishWithPush(t, service, userID, "unconfigured")
	service.push = nil
	_, err = service.DispatchDue(ctx, 10)
	require.NoError(t, err)
	delivery = webPushDelivery(t, db, unconfigured.ID)
	assert.Equal(t, string(deliveryStatusFailed), delivery.Status)
	assert.Equal(t, failureWebPushNotConfigured, delivery.LastFailureCode)
}

func TestWebPushPayloadStaysWithinTheLimit(t *testing.T) {
	long := strings.Repeat("é", 3_900)
	payload, err := webPushPayload(claimedEmailDelivery{
		NotificationID: 7,
		Title:          strings.Repeat("t", 160),
		Body:           long,
	})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(payload), webpush.MaxPayloadBytes)
	var decoded map[string]string
	require.NoError(t, json.Unmarshal(payload, &decoded))
	assert.Equal(t, "/", decoded["url"])
	assert.Equal(t, "n-7", decoded["tag"])
	assert.True(t, strings.HasPrefix(long, strings.TrimSuffix(decoded["body"], "…")))
}

func TestProvideWebPushSenderRequiresACompleteValidConfiguration(t *testing.T) {
	keys, err := webpush.GenerateVAPIDKeys()
	require.NoError(t, err)
	enabled := &config.Config{WebPush: config.WebPushConfig{
		VAPIDPublicKey: keys.PublicKey, VAPIDPrivateKey: keys.PrivateKey, Subject: "mailto:ops@example.com",
	}}
	sender := ProvideWebPushSender(enabled)
	require.NotNil(t, sender)
	assert.Equal(t, keys.PublicKey, sender.PublicKey())

	for name, value := range map[string]config.WebPushConfig{
		"empty":       {},
		"partial":     {VAPIDPublicKey: keys.PublicKey},
		"bad subject": {VAPIDPublicKey: keys.PublicKey, VAPIDPrivateKey: keys.PrivateKey, Subject: "ops@example.com"},
		"mismatch":    {VAPIDPublicKey: "AAAA", VAPIDPrivateKey: keys.PrivateKey, Subject: "mailto:ops@example.com"},
	} {
		assert.Nil(t, ProvideWebPushSender(&config.Config{WebPush: value}), name)
	}
	assert.Nil(t, ProvideWebPushSender(nil))
}
