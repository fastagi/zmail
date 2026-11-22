package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// Outlook push: a Microsoft Graph subscription on the mailbox's Inbox messages (changeType
// created). Graph checks the notification URL with a validation handshake when the subscription is
// created, then posts change notifications that echo the subscription's clientState, and lifecycle
// notifications (reauthorizationRequired, subscriptionRemoved, missed) to the same URL. Zmail reads
// a notification's subscription id, clientState, and lifecycle event only.

const (
	// outlookPushLife is the expiry Zmail asks for: inside both the historic 4,230-minute and the
	// current 10,080-minute limit of mail subscriptions.
	outlookPushLife = 4200 * time.Minute
	// outlookMaxValidationToken bounds the handshake token echoed back.
	outlookMaxValidationToken = 1024
	// outlookMaxPushField bounds the subscription id and clientState of a notification.
	outlookMaxPushField = 255
)

// outlookLifecycleEvents are the lifecycle events Zmail acts on.
var outlookLifecycleEvents = map[string]bool{
	domain.ExternalMailPushReauthorize: true, domain.ExternalMailPushRemoved: true, domain.ExternalMailPushMissed: true,
}

// outlookPushSource subscribes Outlook mailboxes and reads Graph notifications.
type outlookPushSource struct {
	mail            *outlookMailSource
	notificationURL string
}

var _ ExternalMailPushSource = (*outlookPushSource)(nil)

func newOutlookPushSource(mail *outlookMailSource, push config.ExternalMailPushConfig) *outlookPushSource {
	return &outlookPushSource{mail: mail, notificationURL: push.OutlookNotificationURL}
}

// accessToken is a tenant's application token as it is, or a fresh token from the member's grant
// with the grant Microsoft rotated.
func (o *outlookPushSource) accessToken(ctx context.Context, token ExternalMailToken) (string, string, error) {
	if token.AccessToken != "" {
		return token.AccessToken, "", nil
	}
	if token.RefreshToken == "" {
		return "", "", ErrExternalMailAuth
	}
	grant, err := o.mail.redeem(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token.RefreshToken}})
	if err != nil {
		return "", "", err
	}
	return grant.AccessToken, grant.RefreshToken, nil
}

// outlookSubscription is Graph's answer for a subscription.
type outlookSubscription struct {
	ID                 string    `json:"id"`
	ExpirationDateTime time.Time `json:"expirationDateTime"`
}

// Subscribe renews the stored subscription, or creates one with a fresh clientState when there is
// none or Graph no longer knows it.
func (o *outlookPushSource) Subscribe(ctx context.Context, request ExternalMailPushRequest) (ExternalMailPushSubscription, error) {
	accessToken, rotated, err := o.accessToken(ctx, request.Token)
	if err != nil {
		return ExternalMailPushSubscription{}, err
	}
	expires := request.Now.Add(outlookPushLife).UTC()
	if request.SubscriptionID != "" && validOutlookPushField(request.SubscriptionID) {
		var renewed outlookSubscription
		status, renewErr := o.mail.graphSend(ctx, accessToken, http.MethodPatch,
			o.mail.graphURL("/subscriptions/"+url.PathEscape(request.SubscriptionID)),
			map[string]any{"expirationDateTime": expires.Format(time.RFC3339)}, &renewed)
		switch {
		case renewErr == nil:
			return o.accepted(renewed, request.SubscriptionID, false, expires, rotated)
		case status != http.StatusNotFound:
			return ExternalMailPushSubscription{RefreshToken: rotated}, renewErr
		}
		// Graph no longer knows it: create a new one below.
	}
	if request.Secret == "" {
		return ExternalMailPushSubscription{RefreshToken: rotated}, errors.New("outlook subscribe: no clientState")
	}
	resource := strings.TrimPrefix(outlookMailboxRoot(ExternalMailFetchRequest{Token: request.Token, AccountAddress: request.AccountAddress}), "/") +
		"/mailFolders('inbox')/messages"
	var created outlookSubscription
	if _, err = o.mail.graphSend(ctx, accessToken, http.MethodPost, o.mail.graphURL("/subscriptions"), map[string]any{
		"changeType": "created", "notificationUrl": o.notificationURL, "lifecycleNotificationUrl": o.notificationURL,
		"resource": resource, "expirationDateTime": expires.Format(time.RFC3339), "clientState": request.Secret,
		"latestSupportedTlsVersion": "v1_2",
	}, &created); err != nil {
		return ExternalMailPushSubscription{RefreshToken: rotated}, err
	}
	return o.accepted(created, "", true, expires, rotated)
}

// accepted checks Graph's answer; a renewal that names no id keeps the stored one.
func (o *outlookPushSource) accepted(answer outlookSubscription, stored string, created bool, asked time.Time, rotated string) (ExternalMailPushSubscription, error) {
	id := answer.ID
	if id == "" {
		id = stored
	}
	if !validOutlookPushField(id) {
		return ExternalMailPushSubscription{RefreshToken: rotated}, errors.New("outlook subscribe: graph answered without a usable subscription id")
	}
	expires := answer.ExpirationDateTime.UTC()
	if expires.IsZero() || expires.After(asked) {
		expires = asked
	}
	return ExternalMailPushSubscription{LookupKey: id, SubscriptionID: id, Created: created, ExpiresAt: expires, RefreshToken: rotated}, nil
}

// Unsubscribe deletes the subscription; one Graph no longer knows is stopped.
func (o *outlookPushSource) Unsubscribe(ctx context.Context, request ExternalMailPushRequest) error {
	if !validOutlookPushField(request.SubscriptionID) {
		return nil
	}
	accessToken, _, err := o.accessToken(ctx, request.Token)
	if err != nil {
		return err
	}
	status, err := o.mail.graphSend(ctx, accessToken, http.MethodDelete,
		o.mail.graphURL("/subscriptions/"+url.PathEscape(request.SubscriptionID)), nil, nil)
	if status == http.StatusNotFound {
		return nil
	}
	return err
}

// ParseNotification answers the validation handshake, or reads the subscription id, clientState,
// and lifecycle event of each notification; the service checks the clientState.
func (o *outlookPushSource) ParseNotification(_ context.Context, delivery ExternalMailPushDelivery) (ExternalMailPushParsed, error) {
	if delivery.Query.Has("validationToken") {
		token := delivery.Query.Get("validationToken")
		if token == "" || len(token) > outlookMaxValidationToken || !printableASCII(token) {
			return ExternalMailPushParsed{}, fmt.Errorf("%w: validation token", domain.ErrInvalidInput)
		}
		return ExternalMailPushParsed{Validation: token}, nil
	}
	var collection struct {
		Value []struct {
			SubscriptionID string `json:"subscriptionId"`
			ClientState    string `json:"clientState"`
			LifecycleEvent string `json:"lifecycleEvent"`
		} `json:"value"`
	}
	if err := json.Unmarshal(delivery.Body, &collection); err != nil {
		return ExternalMailPushParsed{}, fmt.Errorf("%w: notification is not a Graph collection", domain.ErrInvalidInput)
	}
	hints := make([]ExternalMailPushHint, 0, min(len(collection.Value), maxExternalMailPushHints))
	for _, item := range collection.Value {
		if len(hints) == maxExternalMailPushHints {
			break
		}
		if !validOutlookPushField(item.SubscriptionID) || len(item.ClientState) > outlookMaxPushField {
			continue
		}
		lifecycle := ""
		if outlookLifecycleEvents[item.LifecycleEvent] {
			lifecycle = item.LifecycleEvent
		}
		hints = append(hints, ExternalMailPushHint{Key: item.SubscriptionID, Secret: item.ClientState, Lifecycle: lifecycle})
	}
	return ExternalMailPushParsed{Hints: hints}, nil
}

// validOutlookPushField accepts a Graph subscription id: a GUID, or another short token of letters,
// digits, and dashes.
func validOutlookPushField(value string) bool {
	if value == "" || len(value) > outlookMaxPushField {
		return false
	}
	for _, char := range value {
		if char != '-' && (char < '0' || char > '9') && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') {
			return false
		}
	}
	return true
}

// printableASCII reports whether every byte is printable ASCII.
func printableASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// graphSend sends one Graph request with a JSON body and decodes a bounded answer into out, when
// given. It returns the HTTP status with the error: 401 is ErrExternalMailAuth, and any other
// failure a plain error that names the status and Graph's error code only.
func (s *outlookMailSource) graphSend(ctx context.Context, accessToken, method, link string, payload, out any) (int, error) {
	if !s.graphLink(link) {
		return 0, errors.New("outlook: refusing a link outside the Graph host")
	}
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			return 0, fmt.Errorf("outlook: encode graph request: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, link, &body)
	if err != nil {
		return 0, fmt.Errorf("outlook: build graph request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("outlook: graph request: %w", outlookRedactURLError(err))
	}
	defer resp.Body.Close()
	raw, err := outlookReadBounded(resp.Body, outlookMaxTokenResponse)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("outlook: graph response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &failure) != nil {
			failure.Error.Code = ""
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return resp.StatusCode, fmt.Errorf("%w: graph answered 401 %s", ErrExternalMailAuth, outlookErrorCode(failure.Error.Code))
		}
		return resp.StatusCode, fmt.Errorf("outlook: graph answered %d %s", resp.StatusCode, outlookErrorCode(failure.Error.Code))
	}
	if out != nil && len(raw) > 0 {
		if err = json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("outlook: decode graph response: %w", err)
		}
	}
	return resp.StatusCode, nil
}
