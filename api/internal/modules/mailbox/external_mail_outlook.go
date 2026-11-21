package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// The Outlook connector reads Outlook.com and Microsoft 365 mailboxes through Microsoft Graph with
// the OAuth 2.0 authorization code flow and PKCE. It syncs each folder with Graph message delta:
// a delta round pages through the folder with @odata.nextLink and ends in an @odata.deltaLink,
// which the next round follows to read only what changed.

const (
	outlookLoginBaseURL = "https://login.microsoftonline.com"
	outlookGraphBaseURL = "https://graph.microsoft.com/v1.0"
	// outlookScopes are the delegated permissions the member grants: read mail, read the profile
	// for the account address, and a refresh token (offline_access).
	outlookScopes = "offline_access https://graph.microsoft.com/Mail.Read https://graph.microsoft.com/User.Read openid email"
	// outlookPageSize is the Graph page size a delta request prefers.
	outlookPageSize = 25
	// outlookMessageFields are the message properties a delta page selects.
	outlookMessageFields  = "id,internetMessageId,subject,from,toRecipients,ccRecipients,receivedDateTime,body,isDraft,internetMessageHeaders"
	outlookRequestTimeout = 30 * time.Second
	// outlookMaxTokenResponse bounds token and profile responses; outlookMaxPageResponse bounds one
	// delta page of 25 messages with their bodies.
	outlookMaxTokenResponse = 64 << 10
	outlookMaxPageResponse  = 32 << 20
	// outlookDefaultBackfillDays is the look-back of a first round when the request names none.
	outlookDefaultBackfillDays = 14
	outlookCursorVersion       = 1
)

// outlookSyncedFolders are the folders the connector syncs, by Graph well-known name, in order.
// Sent Items, Drafts, Deleted Items, and Junk Email are left out. Adding a folder here needs no
// cursor migration: a stored cursor without the folder starts its first round on the next fetch.
var outlookSyncedFolders = []string{"inbox"}

func init() {
	registerExternalMailConnector(buildOutlookConnector)
}

// buildOutlookConnector enables the connector when the provider list names outlook and its OAuth
// client is configured.
func buildOutlookConnector(cfg config.ExternalMailConfig) (ExternalMailConnector, bool) {
	client := cfg.OAuth[config.ExternalMailProviderOutlook]
	if !cfg.Provides(config.ExternalMailProviderOutlook) || !client.Configured() {
		return ExternalMailConnector{}, false
	}
	return ExternalMailConnector{
		ID:           domain.ExternalMailProviderOutlook,
		AuthKind:     ExternalMailAuthOAuth2,
		Capabilities: ExternalMailCapabilities{Read: true},
		Source:       newOutlookMailSource(client),
	}, true
}

// outlookMailSource speaks Microsoft identity platform OAuth and Microsoft Graph.
type outlookMailSource struct {
	clientID     string
	clientSecret string
	tenant       string
	// loginBaseURL and graphBaseURL are fields so tests point them at fake servers.
	loginBaseURL string
	graphBaseURL string
	folders      []string
	client       *http.Client
}

var _ ExternalMailSource = (*outlookMailSource)(nil)

func newOutlookMailSource(client config.ExternalMailOAuthClient) *outlookMailSource {
	tenant := strings.TrimSpace(client.Tenant)
	if tenant == "" {
		tenant = config.DefaultExternalMailOutlookTenant
	}
	return &outlookMailSource{
		clientID: strings.TrimSpace(client.ClientID), clientSecret: client.ClientSecret, tenant: tenant,
		loginBaseURL: outlookLoginBaseURL, graphBaseURL: outlookGraphBaseURL,
		folders: outlookSyncedFolders, client: &http.Client{Timeout: outlookRequestTimeout},
	}
}

// AuthURL is the Microsoft sign-in page of the configured tenant.
func (s *outlookMailSource) AuthURL(request ExternalMailAuthRequest) string {
	query := url.Values{
		"client_id":             {s.clientID},
		"response_type":         {"code"},
		"redirect_uri":          {request.RedirectURI},
		"response_mode":         {"query"},
		"scope":                 {outlookScopes},
		"state":                 {request.State},
		"code_challenge":        {request.CodeChallenge},
		"code_challenge_method": {"S256"},
	}
	return s.oauthEndpoint("authorize") + "?" + query.Encode()
}

func (s *outlookMailSource) oauthEndpoint(name string) string {
	return strings.TrimRight(s.loginBaseURL, "/") + "/" + url.PathEscape(s.tenant) + "/oauth2/v2.0/" + name
}

// Exchange redeems the code with its PKCE verifier and reads the account address from /me.
func (s *outlookMailSource) Exchange(ctx context.Context, exchange ExternalMailCodeExchange) (ExternalMailToken, ExternalMailAccount, error) {
	if exchange.Code == "" {
		return ExternalMailToken{}, ExternalMailAccount{}, ErrExternalMailAuth
	}
	grant, err := s.redeem(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {exchange.Code},
		"code_verifier": {exchange.CodeVerifier},
		"redirect_uri":  {exchange.RedirectURI},
	})
	if err != nil {
		return ExternalMailToken{}, ExternalMailAccount{}, err
	}
	if grant.RefreshToken == "" {
		// Without offline_access there is nothing to sync with later.
		return ExternalMailToken{}, ExternalMailAccount{}, fmt.Errorf("%w: microsoft issued no refresh token", ErrExternalMailAuth)
	}
	var profile struct {
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	profileURL := strings.TrimRight(s.graphBaseURL, "/") + "/me?$select=mail,userPrincipalName"
	if err := s.graphGet(ctx, grant.AccessToken, profileURL, outlookMaxTokenResponse, &profile); err != nil {
		return ExternalMailToken{}, ExternalMailAccount{}, err
	}
	address := strings.TrimSpace(profile.Mail)
	if address == "" {
		address = strings.TrimSpace(profile.UserPrincipalName)
	}
	if address == "" {
		return ExternalMailToken{}, ExternalMailAccount{}, errors.New("outlook: the account has no mail address")
	}
	return ExternalMailToken{RefreshToken: grant.RefreshToken}, ExternalMailAccount{Address: address}, nil
}

// Revoke does nothing at Microsoft: the identity platform has no endpoint that revokes a single
// delegated refresh token (only the member, or an administrator, can revoke the app's consent or
// all of the user's sessions). The caller erases the sealed token it stored, which ends Zmail's
// access in practice; the grant itself lapses when Microsoft expires it.
func (s *outlookMailSource) Revoke(context.Context, ExternalMailToken) error {
	return nil
}

// Fetch redeems the refresh token and follows one delta page of one folder. Microsoft rotates
// refresh tokens on every redemption, so the token it issues is always handed back to be stored.
func (s *outlookMailSource) Fetch(ctx context.Context, request ExternalMailFetchRequest) (ExternalMailFetch, error) {
	cursor, err := s.parseCursor(request.Cursor)
	if err != nil {
		return ExternalMailFetch{}, err
	}
	grant, err := s.redeem(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {request.Token.RefreshToken},
	})
	if err != nil {
		return ExternalMailFetch{}, err
	}

	folder := cursor.next()
	link := folder.Link
	if link == "" {
		link = s.initialDeltaURL(folder.Folder, request)
	}
	var page outlookDeltaPage
	if err := s.graphGet(ctx, grant.AccessToken, link, outlookMaxPageResponse, &page); err != nil {
		return ExternalMailFetch{}, err
	}
	switch {
	case page.NextLink != "":
		folder.Link, folder.Pending = page.NextLink, true
	case page.DeltaLink != "":
		folder.Link, folder.Pending = page.DeltaLink, false
	default:
		return ExternalMailFetch{}, errors.New("outlook: delta page carries neither a next nor a delta link")
	}
	// Links come back from the provider and are checked again when the next fetch reads them.
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return ExternalMailFetch{}, fmt.Errorf("outlook: encode cursor: %w", err)
	}
	messages := make([]ExternalMessage, 0, len(page.Value))
	for _, item := range page.Value {
		if message, ok := item.message(); ok {
			messages = append(messages, message)
		}
	}
	return ExternalMailFetch{
		Messages: messages, Cursor: string(encoded), More: cursor.pending(), RefreshToken: grant.RefreshToken,
	}, nil
}

// initialDeltaURL starts a folder's first delta round, bounded by the backfill window.
func (s *outlookMailSource) initialDeltaURL(folder string, request ExternalMailFetchRequest) string {
	days := request.BackfillDays
	if days <= 0 {
		days = outlookDefaultBackfillDays
	}
	now := request.Now
	if now.IsZero() {
		now = time.Now()
	}
	since := now.UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	query := url.Values{
		"$select": {outlookMessageFields},
		"$filter": {"receivedDateTime ge " + since},
	}
	return strings.TrimRight(s.graphBaseURL, "/") + "/me/mailFolders/" + url.PathEscape(folder) +
		"/messages/delta?" + query.Encode()
}

// outlookCursor is the opaque sync position: one Graph link per folder. A pending folder is in the
// middle of a delta round and holds an @odata.nextLink; a settled one holds the @odata.deltaLink
// that starts its next round.
type outlookCursor struct {
	Version int                   `json:"v"`
	Folders []*outlookFolderState `json:"folders"`
}

type outlookFolderState struct {
	Folder  string `json:"folder"`
	Link    string `json:"link,omitempty"`
	Pending bool   `json:"pending,omitempty"`
}

// parseCursor reads a stored cursor, keeping the configured folders in order: a folder the cursor
// lacks starts its first round, and one no longer configured is dropped. Every stored link must
// point at the configured Graph host, so a tampered cursor cannot send the access token elsewhere.
func (s *outlookMailSource) parseCursor(raw string) (*outlookCursor, error) {
	stored := map[string]*outlookFolderState{}
	if raw != "" {
		var decoded outlookCursor
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil || decoded.Version != outlookCursorVersion {
			return nil, ErrExternalMailCursorExpired
		}
		for _, folder := range decoded.Folders {
			if folder == nil || folder.Link == "" {
				continue
			}
			if !s.graphLink(folder.Link) {
				return nil, fmt.Errorf("%w: cursor link does not point at the Graph host", ErrExternalMailCursorExpired)
			}
			stored[folder.Folder] = folder
		}
	}
	cursor := &outlookCursor{Version: outlookCursorVersion}
	for _, name := range s.folders {
		if folder, ok := stored[name]; ok {
			cursor.Folders = append(cursor.Folders, &outlookFolderState{Folder: name, Link: folder.Link, Pending: folder.Pending})
			continue
		}
		cursor.Folders = append(cursor.Folders, &outlookFolderState{Folder: name, Pending: true})
	}
	return cursor, nil
}

// next returns the folder to read a page of: the first one in the middle of a round. When every
// folder has settled, a new round starts for all of them.
func (c *outlookCursor) next() *outlookFolderState {
	for _, folder := range c.Folders {
		if folder.Pending {
			return folder
		}
	}
	for _, folder := range c.Folders {
		folder.Pending = true
	}
	return c.Folders[0]
}

// pending reports whether a round is still in progress for any folder.
func (c *outlookCursor) pending() bool {
	for _, folder := range c.Folders {
		if folder.Pending {
			return true
		}
	}
	return false
}

// graphLink reports whether link has the configured Graph scheme, host, and path prefix.
func (s *outlookMailSource) graphLink(link string) bool {
	base, err := url.Parse(s.graphBaseURL)
	if err != nil {
		return false
	}
	parsed, err := url.Parse(link)
	if err != nil || parsed.User != nil {
		return false
	}
	prefix := strings.TrimRight(base.Path, "/") + "/"
	return parsed.Scheme == base.Scheme && strings.EqualFold(parsed.Host, base.Host) && strings.HasPrefix(parsed.Path, prefix)
}

// outlookGrant is a token endpoint answer.
type outlookGrant struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// redeem posts a grant to the token endpoint. A refused grant (invalid_grant, or any 400 or 401)
// is ErrExternalMailAuth; throttling and server errors are plain errors the worker retries.
func (s *outlookMailSource) redeem(ctx context.Context, form url.Values) (outlookGrant, error) {
	form.Set("client_id", s.clientID)
	form.Set("client_secret", s.clientSecret)
	form.Set("scope", outlookScopes)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.oauthEndpoint("token"), strings.NewReader(form.Encode()))
	if err != nil {
		return outlookGrant{}, fmt.Errorf("outlook: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return outlookGrant{}, fmt.Errorf("outlook: token request: %w", outlookRedactURLError(err))
	}
	defer resp.Body.Close()
	body, err := outlookReadBounded(resp.Body, outlookMaxTokenResponse)
	if err != nil {
		return outlookGrant{}, fmt.Errorf("outlook: token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &failure)
		if failure.Error == "invalid_grant" || resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
			return outlookGrant{}, fmt.Errorf("%w: token endpoint answered %d %s", ErrExternalMailAuth, resp.StatusCode, outlookErrorCode(failure.Error))
		}
		return outlookGrant{}, fmt.Errorf("outlook: token endpoint answered %d", resp.StatusCode)
	}
	var grant outlookGrant
	if err := json.Unmarshal(body, &grant); err != nil || grant.AccessToken == "" {
		return outlookGrant{}, errors.New("outlook: token endpoint answered without an access token")
	}
	return grant, nil
}

// graphGet reads one Graph resource into out. 401 is ErrExternalMailAuth; 410 and the sync state
// error codes are ErrExternalMailCursorExpired; 429 and 5xx are plain errors the worker retries.
func (s *outlookMailSource) graphGet(ctx context.Context, accessToken, link string, limit int64, out any) error {
	if !s.graphLink(link) {
		return errors.New("outlook: refusing a link outside the Graph host")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return fmt.Errorf("outlook: build graph request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Add("Prefer", fmt.Sprintf("odata.maxpagesize=%d", outlookPageSize))
	req.Header.Add("Prefer", `IdType="ImmutableId"`)
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("outlook: graph request: %w", outlookRedactURLError(err))
	}
	defer resp.Body.Close()
	body, err := outlookReadBounded(resp.Body, limit)
	if err != nil {
		return fmt.Errorf("outlook: graph response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &failure)
		code := failure.Error.Code
		switch {
		case resp.StatusCode == http.StatusUnauthorized || strings.EqualFold(code, "invalid_grant"):
			return fmt.Errorf("%w: graph answered %d %s", ErrExternalMailAuth, resp.StatusCode, outlookErrorCode(code))
		case resp.StatusCode == http.StatusGone || outlookSyncStateCode(code):
			return fmt.Errorf("%w: graph answered %d %s", ErrExternalMailCursorExpired, resp.StatusCode, outlookErrorCode(code))
		case resp.StatusCode == http.StatusTooManyRequests:
			return fmt.Errorf("outlook: graph throttled the request (429), retry later")
		default:
			return fmt.Errorf("outlook: graph answered %d %s", resp.StatusCode, outlookErrorCode(code))
		}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("outlook: decode graph response: %w", err)
	}
	return nil
}

// outlookSyncStateCode reports the Graph error codes of a delta token it no longer honours.
func outlookSyncStateCode(code string) bool {
	switch strings.ToLower(code) {
	case "syncstatenotfound", "resyncrequired", "syncstateinvalid":
		return true
	}
	return false
}

// outlookErrorCode keeps a provider error code fit for an error message: short and plain.
func outlookErrorCode(code string) string {
	if len(code) > 64 {
		code = code[:64]
	}
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r == '.' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			return r
		}
		return -1
	}, code)
}

// outlookRedactURLError drops the request URL from a transport error: a delta link carries the sync
// state, which is not for logs.
func outlookRedactURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// outlookReadBounded reads at most limit bytes and fails on a longer body.
func outlookReadBounded(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return body, nil
}

// outlookDeltaPage is one page of a Graph message delta.
type outlookDeltaPage struct {
	Value     []outlookMessage `json:"value"`
	NextLink  string           `json:"@odata.nextLink"`
	DeltaLink string           `json:"@odata.deltaLink"`
}

type outlookRecipient struct {
	EmailAddress struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	} `json:"emailAddress"`
}

type outlookMessage struct {
	ID                string             `json:"id"`
	Removed           json.RawMessage    `json:"@removed"`
	InternetMessageID string             `json:"internetMessageId"`
	Subject           string             `json:"subject"`
	From              *outlookRecipient  `json:"from"`
	ToRecipients      []outlookRecipient `json:"toRecipients"`
	CcRecipients      []outlookRecipient `json:"ccRecipients"`
	ReceivedDateTime  string             `json:"receivedDateTime"`
	IsDraft           bool               `json:"isDraft"`
	Body              struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
	InternetMessageHeaders []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"internetMessageHeaders"`
}

// message maps a delta entry to an ExternalMessage; removed entries and drafts are skipped.
func (m outlookMessage) message() (ExternalMessage, bool) {
	if len(m.Removed) > 0 || m.IsDraft || m.ID == "" {
		return ExternalMessage{}, false
	}
	message := ExternalMessage{
		ExternalID: m.ID, Subject: m.Subject,
		To: outlookAddresses(m.ToRecipients), Cc: outlookAddresses(m.CcRecipients),
	}
	if m.From != nil {
		message.FromAddress = strings.TrimSpace(m.From.EmailAddress.Address)
		message.FromName = strings.TrimSpace(m.From.EmailAddress.Name)
	}
	if id, ok := normalizeMessageID(m.InternetMessageID); ok {
		message.RFCMessageID = id
	}
	if received, err := time.Parse(time.RFC3339, m.ReceivedDateTime); err == nil {
		message.ReceivedAt = received.UTC()
	}
	if strings.EqualFold(m.Body.ContentType, "html") {
		message.HTMLBody = m.Body.Content
	} else {
		message.TextBody = m.Body.Content
	}
	for _, header := range m.InternetMessageHeaders {
		switch strings.ToLower(header.Name) {
		case "in-reply-to":
			if id, ok := normalizeMessageID(header.Value); ok {
				message.InReplyTo = id
			}
		case "references":
			message.References = parseReferences(header.Value)
		}
	}
	return message, true
}

func outlookAddresses(recipients []outlookRecipient) []string {
	var addresses []string
	for _, recipient := range recipients {
		if address := strings.TrimSpace(recipient.EmailAddress.Address); address != "" {
			addresses = append(addresses, address)
		}
	}
	return addresses
}
