package mailbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

// Stable failure codes for sending through OAuth-backed external mail connectors.
const (
	failureOAuthAuth        = "oauth_auth_failed"
	failureOAuthRejected    = "oauth_rejected"
	failureOAuthUnavailable = "oauth_unavailable"
)

var (
	_ ExternalMailSender = (*gmailMailSource)(nil)
	_ ExternalMailSender = (*outlookMailSource)(nil)
)

// buildOAuthMIMEMessage builds an RFC 5322 MIME payload for API-based delivery (Gmail/Graph).
// Unlike SMTP (where Bcc is envelope-only), Gmail raw MIME and Microsoft Graph MIME delivery read
// the Bcc header to deliver blind copies and strip it before dispatching to other recipients.
func buildOAuthMIMEMessage(message mailtransport.Message, messageID string, now time.Time) ([]byte, error) {
	raw, err := buildMIMEMessage(message, messageID, now)
	if err != nil {
		return nil, err
	}
	if len(message.Bcc) == 0 {
		return raw, nil
	}
	bccLine := "Bcc: " + strings.Join(message.Bcc, ", ") + "\r\n"
	out := make([]byte, 0, len(bccLine)+len(raw))
	out = append(out, bccLine...)
	out = append(out, raw...)
	return out, nil
}

// SendMail delivers one message via the Gmail REST API (POST /users/me/messages/send) using the
// member's stored OAuth grant.
func (g *gmailMailSource) SendMail(ctx context.Context, token ExternalMailToken, message mailtransport.Message) (mailtransport.Result, error) {
	accessToken, err := g.resolveSendToken(ctx, token)
	if err != nil {
		return mailtransport.Result{}, err
	}
	messageID := message.MessageID
	if messageID == "" {
		messageID = newSMTPMessageID(message.FromAddress)
	}
	raw, err := buildOAuthMIMEMessage(message, messageID, time.Now())
	if err != nil {
		return mailtransport.Result{}, &mailtransport.Error{Code: failureOAuthRejected, Permanent: true, Responded: true}
	}
	payload, err := json.Marshal(struct {
		Raw string `json:"raw"`
	}{
		Raw: base64.RawURLEncoding.EncodeToString(raw),
	})
	if err != nil {
		return mailtransport.Result{}, &mailtransport.Error{Code: failureOAuthRejected, Permanent: true, Responded: true}
	}

	reqCtx, cancel := context.WithTimeout(ctx, gmailRequestTimeout)
	defer cancel()
	endpoint := strings.TrimRight(g.apiBase, "/") + "/users/me/messages/send"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return mailtransport.Result{}, &mailtransport.Error{Code: failureOAuthRejected, Permanent: true, Responded: true}
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return mailtransport.Result{}, &mailtransport.Error{Code: failureOAuthUnavailable, Responded: false}
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, gmailMaxErrorBytes))

	if err := mapOAuthSendStatus(resp.StatusCode); err != nil {
		return mailtransport.Result{}, err
	}
	var sent struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &sent)
	providerID := sent.ID
	if providerID == "" {
		providerID = messageID
	}
	return mailtransport.Result{MessageID: providerID, RFCMessageID: messageID}, nil
}

func (g *gmailMailSource) resolveSendToken(ctx context.Context, token ExternalMailToken) (string, error) {
	if token.AccessToken != "" {
		return token.AccessToken, nil
	}
	if token.RefreshToken == "" {
		return "", &mailtransport.Error{Code: failureOAuthAuth, Permanent: true, Responded: true}
	}
	tokens, err := g.token(ctx, "refresh", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {token.RefreshToken},
	})
	if err != nil {
		if errors.Is(err, ErrExternalMailAuth) {
			return "", &mailtransport.Error{Code: failureOAuthAuth, Permanent: true, Responded: true}
		}
		return "", &mailtransport.Error{Code: failureOAuthUnavailable, Responded: true}
	}
	return tokens.AccessToken, nil
}

// SendMail delivers one message via Microsoft Graph MIME send (POST /me/sendMail with
// Content-Type: text/plain and base64-encoded RFC 5322 body).
func (s *outlookMailSource) SendMail(ctx context.Context, token ExternalMailToken, message mailtransport.Message) (mailtransport.Result, error) {
	accessToken, err := s.resolveSendToken(ctx, token)
	if err != nil {
		return mailtransport.Result{}, err
	}
	messageID := message.MessageID
	if messageID == "" {
		messageID = newSMTPMessageID(message.FromAddress)
	}
	raw, err := buildOAuthMIMEMessage(message, messageID, time.Now())
	if err != nil {
		return mailtransport.Result{}, &mailtransport.Error{Code: failureOAuthRejected, Permanent: true, Responded: true}
	}
	encoded := base64.StdEncoding.EncodeToString(raw)

	reqCtx, cancel := context.WithTimeout(ctx, outlookRequestTimeout)
	defer cancel()
	endpoint := s.graphURL("/me/sendMail")
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, strings.NewReader(encoded))
	if err != nil {
		return mailtransport.Result{}, &mailtransport.Error{Code: failureOAuthRejected, Permanent: true, Responded: true}
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "text/plain")

	resp, err := s.client.Do(req)
	if err != nil {
		return mailtransport.Result{}, &mailtransport.Error{Code: failureOAuthUnavailable, Responded: false}
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, gmailMaxErrorBytes))

	if err := mapOAuthSendStatus(resp.StatusCode); err != nil {
		return mailtransport.Result{}, err
	}
	return mailtransport.Result{MessageID: messageID, RFCMessageID: messageID}, nil
}

func (s *outlookMailSource) resolveSendToken(ctx context.Context, token ExternalMailToken) (string, error) {
	if token.AccessToken != "" {
		return token.AccessToken, nil
	}
	if token.RefreshToken == "" {
		return "", &mailtransport.Error{Code: failureOAuthAuth, Permanent: true, Responded: true}
	}
	grant, err := s.redeem(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {token.RefreshToken},
	})
	if err != nil {
		if errors.Is(err, ErrExternalMailAuth) {
			return "", &mailtransport.Error{Code: failureOAuthAuth, Permanent: true, Responded: true}
		}
		return "", &mailtransport.Error{Code: failureOAuthUnavailable, Responded: true}
	}
	return grant.AccessToken, nil
}

func mapOAuthSendStatus(statusCode int) error {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return nil
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return &mailtransport.Error{Code: failureOAuthAuth, Permanent: true, Responded: true}
	case statusCode == http.StatusTooManyRequests || statusCode >= 500:
		return &mailtransport.Error{Code: failureOAuthUnavailable, Permanent: false, Responded: true}
	default:
		return &mailtransport.Error{Code: failureOAuthRejected, Permanent: true, Responded: true}
	}
}
