package mailbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/infra/config"
)

// Gmail push: users.watch publishes a message to the operator's Cloud Pub/Sub topic whenever the
// Inbox changes, and a Pub/Sub push subscription delivers it to Zmail with a Google-signed OIDC
// token. The message names the account's address and a historyId; Zmail reads neither for mail
// and only marks the account's connections due.

const (
	// gmailWatchLife is how long a watch lasts when Google reports no expiration.
	gmailWatchLife = 7 * 24 * time.Hour
	// gmailMaxPushDataBytes bounds the decoded message data of one push request.
	gmailMaxPushDataBytes = 4 << 10
)

// gmailPushSource subscribes Gmail accounts and reads Pub/Sub push requests.
type gmailPushSource struct {
	mail     *gmailMailSource
	topic    string
	verifier *googleOIDCVerifier
}

var _ ExternalMailPushSource = (*gmailPushSource)(nil)

func newGmailPushSource(mail *gmailMailSource, push config.ExternalMailPushConfig) *gmailPushSource {
	return &gmailPushSource{
		mail: mail, topic: push.GmailTopic,
		verifier: newGoogleOIDCVerifier(push.JWKSURL(), push.GmailAudience, push.GmailServiceAccount),
	}
}

// accessToken is the token a push call uses: a tenant's token for the one mailbox as it is, or a
// fresh one from the member's grant, with the grant Google rotated, if any.
func (g *gmailPushSource) accessToken(ctx context.Context, token ExternalMailToken) (string, string, error) {
	if token.AccessToken != "" {
		return token.AccessToken, "", nil
	}
	if token.RefreshToken == "" {
		return "", "", ErrExternalMailAuth
	}
	tokens, err := g.mail.token(ctx, "refresh", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token.RefreshToken}})
	if err != nil {
		return "", "", err
	}
	rotated := ""
	if tokens.RefreshToken != "" && tokens.RefreshToken != token.RefreshToken {
		rotated = tokens.RefreshToken
	}
	return tokens.AccessToken, rotated, nil
}

// Subscribe starts or renews the account's watch on its Inbox; Gmail renews by watching again. The
// subscription is keyed by the address Gmail's profile reports, which is the one its notifications
// name, so an alias the connection was bound with still matches.
//
// A mailbox assigned from a Workspace tenant is not watched: its tokens come from the customer's own
// service account in the customer's Cloud project, and users.watch only publishes to a topic of the
// project the token's client belongs to, never the operator's.
func (g *gmailPushSource) Subscribe(ctx context.Context, request ExternalMailPushRequest) (ExternalMailPushSubscription, error) {
	if request.TenantMailbox {
		return ExternalMailPushSubscription{}, ErrExternalMailPushUnsupported
	}
	accessToken, rotated, err := g.accessToken(ctx, request.Token)
	if err != nil {
		return ExternalMailPushSubscription{}, err
	}
	profile, err := g.mail.profile(ctx, accessToken)
	if err != nil {
		return ExternalMailPushSubscription{RefreshToken: rotated}, err
	}
	key := normalizedPushKey(profile.EmailAddress)
	if key == "" || len(key) > 320 || !strings.Contains(key, "@") {
		return ExternalMailPushSubscription{RefreshToken: rotated}, fmt.Errorf("gmail watch: the profile carries no address")
	}
	var watched struct {
		HistoryID  string `json:"historyId"`
		Expiration string `json:"expiration"`
	}
	payload := map[string]any{"topicName": g.topic, "labelIds": []string{"INBOX"}, "labelFilterBehavior": "INCLUDE"}
	if err = g.mail.postJSON(ctx, accessToken, "watch", "/users/me/watch", payload, gmailMaxTokenBytes, &watched); err != nil {
		return ExternalMailPushSubscription{RefreshToken: rotated}, err
	}
	expires := request.Now.Add(gmailWatchLife)
	if millis, parseErr := strconv.ParseInt(watched.Expiration, 10, 64); parseErr == nil && millis > 0 {
		expires = time.UnixMilli(millis).UTC()
	}
	return ExternalMailPushSubscription{LookupKey: key, ExpiresAt: expires, RefreshToken: rotated}, nil
}

// Unsubscribe stops the account's watch.
func (g *gmailPushSource) Unsubscribe(ctx context.Context, request ExternalMailPushRequest) error {
	if request.TenantMailbox {
		return nil
	}
	accessToken, _, err := g.accessToken(ctx, request.Token)
	if err != nil {
		return err
	}
	return g.mail.postJSON(ctx, accessToken, "stop", "/users/me/stop", nil, gmailMaxErrorBytes, nil)
}

// ParseNotification checks the push request's OIDC token first and reads the body only after it.
// An authenticated message Zmail cannot read is acknowledged without a hint, since Pub/Sub would
// only redeliver it.
func (g *gmailPushSource) ParseNotification(ctx context.Context, delivery ExternalMailPushDelivery) (ExternalMailPushParsed, error) {
	authorization := delivery.Header.Get("Authorization")
	scheme, token, found := strings.Cut(authorization, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ExternalMailPushParsed{}, ErrExternalMailPushUnauthenticated
	}
	if err := g.verifier.Verify(ctx, strings.TrimSpace(token), delivery.Now); err != nil {
		return ExternalMailPushParsed{}, ErrExternalMailPushUnauthenticated
	}
	key := gmailPushAddress(delivery.Body)
	if key == "" {
		return ExternalMailPushParsed{Authenticated: true}, nil
	}
	return ExternalMailPushParsed{Hints: []ExternalMailPushHint{{Key: key, Authenticated: true}}, Authenticated: true}, nil
}

// gmailPushAddress reads the lowercased address of a Pub/Sub push body's Gmail message; empty when
// the body cannot be read.
func gmailPushAddress(body []byte) string {
	var envelope struct {
		Message struct {
			Data string `json:"data"`
		} `json:"message"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Message.Data) > base64.StdEncoding.EncodedLen(gmailMaxPushDataBytes) {
		return ""
	}
	data, err := base64.StdEncoding.DecodeString(envelope.Message.Data)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(envelope.Message.Data)
	}
	var message struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err != nil || json.Unmarshal(data, &message) != nil {
		return ""
	}
	key := normalizedPushKey(message.EmailAddress)
	if len(key) > 320 || !strings.Contains(key, "@") {
		return ""
	}
	return key
}

// postJSON posts to the Gmail API and decodes a bounded answer into target, when given. It maps
// errors like getJSON.
func (g *gmailMailSource) postJSON(ctx context.Context, accessToken, operation, path string, payload any, limit int64, target any) error {
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			return fmt.Errorf("gmail %s: %w", operation, err)
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.apiBase+path, &body)
	if err != nil {
		return fmt.Errorf("gmail %s: %w", operation, err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := g.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("gmail %s: %w", operation, redactURLError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		reason, errorDomain := gmailErrorReason(response.Body)
		statusErr := &gmailStatusError{Operation: operation, Status: response.StatusCode, Reason: reason}
		if response.StatusCode == http.StatusUnauthorized ||
			(response.StatusCode == http.StatusForbidden && gmailGrantRefused(reason, errorDomain)) {
			return fmt.Errorf("%w: %w", ErrExternalMailAuth, statusErr)
		}
		return statusErr
	}
	if target == nil {
		return nil
	}
	raw, err := readBounded(response.Body, limit)
	if err != nil {
		return fmt.Errorf("gmail %s: %w", operation, err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("gmail %s: malformed answer", operation)
	}
	return nil
}
