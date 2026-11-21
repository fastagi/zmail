package mailbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// Google endpoints the Gmail connector talks to. Each is a field of gmailMailSource so tests point
// them at a local server.
const (
	gmailAuthEndpoint   = "https://accounts.google.com/o/oauth2/v2/auth"
	gmailTokenEndpoint  = "https://oauth2.googleapis.com/token"
	gmailAPIBase        = "https://gmail.googleapis.com/gmail/v1"
	gmailRevokeEndpoint = "https://oauth2.googleapis.com/revoke"
	// gmailScope reads mail and labels and nothing else; openid and email let the consent screen
	// name the account.
	gmailScope = "https://www.googleapis.com/auth/gmail.readonly openid email"

	// gmailPageSize keeps one fetch bounded: at most this many messages are listed and read.
	gmailPageSize = 25
	// gmailRequestTimeout bounds every single HTTP request.
	gmailRequestTimeout = 20 * time.Second
	// Response size bounds. A message larger than gmailMaxMessageBytes in full format is read
	// again in metadata format, so it is stored with its headers instead of blocking the sync.
	gmailMaxTokenBytes   = 64 << 10
	gmailMaxListBytes    = 1 << 20
	gmailMaxMessageBytes = 8 << 20
	gmailMaxErrorBytes   = 16 << 10
	// gmailMaxMIMEDepth and gmailMaxMIMEParts bound the walk over a message's parts.
	gmailMaxMIMEDepth = maxMIMEDepth
	gmailMaxMIMEParts = maxMIMEParts
	// gmailCursorVersion tags the cursor encoding so a later format can tell an old one apart.
	gmailCursorVersion = 1
)

var (
	// errGmailTooLarge means a response exceeded its size bound.
	errGmailTooLarge = errors.New("gmail: response too large")
	// errGmailPageTokenRefused means Google refused a backfill page token.
	errGmailPageTokenRefused = errors.New("gmail: page token refused")
)

// gmailGrantReasons are the API 403 reasons that mean the grant no longer reads the mailbox, so
// the member must reconnect. Quota, rate limit, and unknown reasons are retried instead.
var gmailGrantReasons = map[string]bool{
	"insufficientPermissions": true, "ACCESS_TOKEN_SCOPE_INSUFFICIENT": true, "authError": true,
	"domainPolicy": true,
}

// gmailGrantRefused reports whether an API 403 means the grant lacks access. A generic forbidden
// counts only outside the usage-limit domain, where Google reports quotas.
func gmailGrantRefused(reason, domain string) bool {
	return gmailGrantReasons[reason] || (reason == "forbidden" && domain != "usageLimits")
}

func init() {
	registerExternalMailConnector(func(cfg config.ExternalMailConfig) (ExternalMailConnector, bool) {
		client := cfg.OAuth[config.ExternalMailProviderGmail]
		if !cfg.Provides(config.ExternalMailProviderGmail) || !client.Configured() {
			return ExternalMailConnector{}, false
		}
		return ExternalMailConnector{
			ID: domain.ExternalMailProviderGmail, AuthKind: ExternalMailAuthOAuth2,
			Capabilities: ExternalMailCapabilities{Read: true}, Source: newGmailMailSource(client),
		}, true
	})
}

// gmailMailSource reads a Gmail account over the Gmail REST API with a refresh token. The first
// fetch of a connection records the account's current historyId and backfills the window with
// messages.list, newest first; once the backfill pages run out, history.list reads what was added
// since that historyId. Only inbound mail is returned: drafts, spam, trash, and sent mail that is
// not also in the inbox are skipped.
type gmailMailSource struct {
	clientID     string
	clientSecret string
	// Endpoints; tests replace them with a local server.
	authEndpoint   string
	tokenEndpoint  string
	apiBase        string
	revokeEndpoint string
	httpClient     *http.Client
	// pageSize caps the messages of one fetch.
	pageSize int
	// maxMessageBytes bounds one message in full format.
	maxMessageBytes int64
}

var _ ExternalMailSource = (*gmailMailSource)(nil)

func newGmailMailSource(client config.ExternalMailOAuthClient) *gmailMailSource {
	return &gmailMailSource{
		clientID: strings.TrimSpace(client.ClientID), clientSecret: strings.TrimSpace(client.ClientSecret),
		authEndpoint: gmailAuthEndpoint, tokenEndpoint: gmailTokenEndpoint, apiBase: gmailAPIBase,
		revokeEndpoint:  gmailRevokeEndpoint,
		httpClient:      &http.Client{Timeout: gmailRequestTimeout},
		pageSize:        gmailPageSize,
		maxMessageBytes: gmailMaxMessageBytes,
	}
}

// AuthURL is Google's consent page. access_type=offline with prompt=consent makes Google hand out a
// refresh token on every sign-in, including a reconnect.
func (g *gmailMailSource) AuthURL(request ExternalMailAuthRequest) string {
	query := url.Values{
		"client_id":             {g.clientID},
		"redirect_uri":          {request.RedirectURI},
		"response_type":         {"code"},
		"scope":                 {gmailScope},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
		"state":                 {request.State},
		"code_challenge":        {request.CodeChallenge},
		"code_challenge_method": {"S256"},
	}
	return g.authEndpoint + "?" + query.Encode()
}

// gmailTokenResponse is Google's token endpoint answer.
type gmailTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
}

// Exchange trades the code and PKCE verifier for a grant, then reads the account's address from
// the Gmail profile.
func (g *gmailMailSource) Exchange(ctx context.Context, exchange ExternalMailCodeExchange) (ExternalMailToken, ExternalMailAccount, error) {
	if exchange.Code == "" || exchange.CodeVerifier == "" {
		return ExternalMailToken{}, ExternalMailAccount{}, ErrExternalMailAuth
	}
	tokens, err := g.token(ctx, "exchange", url.Values{
		"grant_type": {"authorization_code"}, "code": {exchange.Code},
		"code_verifier": {exchange.CodeVerifier}, "redirect_uri": {exchange.RedirectURI},
	})
	if err != nil {
		return ExternalMailToken{}, ExternalMailAccount{}, err
	}
	if tokens.RefreshToken == "" {
		// Without a refresh token the account cannot be synced; the member signs in again.
		return ExternalMailToken{}, ExternalMailAccount{}, fmt.Errorf("%w: gmail granted no refresh token", ErrExternalMailAuth)
	}
	profile, err := g.profile(ctx, tokens.AccessToken)
	if err != nil {
		return ExternalMailToken{}, ExternalMailAccount{}, err
	}
	address := strings.ToLower(strings.TrimSpace(profile.EmailAddress))
	if address == "" {
		return ExternalMailToken{}, ExternalMailAccount{}, errors.New("gmail: profile carries no address")
	}
	return ExternalMailToken{RefreshToken: tokens.RefreshToken}, ExternalMailAccount{Address: address}, nil
}

// gmailCursor is the opaque sync position: the historyId the incremental phase starts from, and
// the page token of the listing in progress. Backfill marks the messages.list phase; After is the
// backfill's lower bound in Unix seconds, fixed when it starts so every page sends the same query.
type gmailCursor struct {
	Version   int    `json:"v"`
	HistoryID string `json:"h"`
	PageToken string `json:"p,omitempty"`
	Backfill  bool   `json:"b,omitempty"`
	After     int64  `json:"a,omitempty"`
}

func (c gmailCursor) encode() string {
	c.Version = gmailCursorVersion
	encoded, err := json.Marshal(c)
	if err != nil {
		// Unreachable for this struct; an empty cursor would start the backfill over.
		return ""
	}
	return string(encoded)
}

// decodeGmailCursor reads a cursor; one this connector did not write cannot be honored.
func decodeGmailCursor(raw string) (gmailCursor, error) {
	var cursor gmailCursor
	if err := json.Unmarshal([]byte(raw), &cursor); err != nil || cursor.Version != gmailCursorVersion || !validHistoryID(cursor.HistoryID) {
		return gmailCursor{}, fmt.Errorf("%w: unreadable gmail cursor", ErrExternalMailCursorExpired)
	}
	return cursor, nil
}

func validHistoryID(id string) bool {
	_, err := strconv.ParseUint(id, 10, 64)
	return err == nil
}

// Fetch refreshes the access token and reads one page: a backfill page from an empty or backfill
// cursor, otherwise a page of history. A refresh token Google rotated is returned even when the
// rest of the fetch fails, since the old one may already be spent.
func (g *gmailMailSource) Fetch(ctx context.Context, request ExternalMailFetchRequest) (ExternalMailFetch, error) {
	if request.Token.RefreshToken == "" {
		return ExternalMailFetch{}, ErrExternalMailAuth
	}
	tokens, err := g.token(ctx, "refresh", url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {request.Token.RefreshToken},
	})
	if err != nil {
		return ExternalMailFetch{}, err
	}
	fetched, err := g.fetchPage(ctx, tokens.AccessToken, request)
	if err != nil {
		fetched = ExternalMailFetch{}
	}
	if tokens.RefreshToken != "" && tokens.RefreshToken != request.Token.RefreshToken {
		fetched.RefreshToken = tokens.RefreshToken
	}
	return fetched, err
}

// fetchPage reads the page the cursor points at.
func (g *gmailMailSource) fetchPage(ctx context.Context, accessToken string, request ExternalMailFetchRequest) (ExternalMailFetch, error) {
	if request.Cursor == "" {
		return g.startBackfill(ctx, accessToken, request)
	}
	cursor, err := decodeGmailCursor(request.Cursor)
	if err != nil {
		return ExternalMailFetch{}, err
	}
	if !cursor.Backfill {
		return g.historyPage(ctx, accessToken, cursor)
	}
	fetched, err := g.backfillPage(ctx, accessToken, request, cursor)
	if errors.Is(err, errGmailPageTokenRefused) {
		// Google no longer honors the page token: start the backfill over, once in this fetch. The
		// restarted listing has no page token, so a second refusal is a plain error.
		return g.startBackfill(ctx, accessToken, request)
	}
	return fetched, err
}

// startBackfill records the account's current historyId before listing, so mail that arrives
// while the backfill pages are read is picked up by the incremental phase.
func (g *gmailMailSource) startBackfill(ctx context.Context, accessToken string, request ExternalMailFetchRequest) (ExternalMailFetch, error) {
	profile, err := g.profile(ctx, accessToken)
	if err != nil {
		return ExternalMailFetch{}, err
	}
	if !validHistoryID(profile.HistoryID) {
		return ExternalMailFetch{}, errors.New("gmail: profile carries no historyId")
	}
	return g.backfillPage(ctx, accessToken, request, gmailCursor{
		HistoryID: profile.HistoryID, Backfill: true, After: gmailBackfillAfter(request),
	})
}

// gmailBackfillAfter is the lower bound of a backfill that starts now, in Unix seconds.
func gmailBackfillAfter(request ExternalMailFetchRequest) int64 {
	now := request.Now
	if now.IsZero() {
		now = time.Now()
	}
	days := request.BackfillDays
	if days <= 0 {
		days = domain.DefaultExternalMailBackfillDays
	}
	days = min(days, domain.MaxExternalMailBackfillDays)
	return now.AddDate(0, 0, -days).Unix()
}

// backfillPage lists one page of the backfill window, newest first.
func (g *gmailMailSource) backfillPage(ctx context.Context, accessToken string, request ExternalMailFetchRequest, cursor gmailCursor) (ExternalMailFetch, error) {
	if cursor.After <= 0 {
		cursor.After = gmailBackfillAfter(request)
	}
	query := url.Values{
		"q":          {"after:" + strconv.FormatInt(cursor.After, 10)},
		"maxResults": {strconv.Itoa(g.pageSize)},
	}
	if cursor.PageToken != "" {
		query.Set("pageToken", cursor.PageToken)
	}
	var listed struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := g.getJSON(ctx, accessToken, "list", "/users/me/messages", query, gmailMaxListBytes, &listed); err != nil {
		var status *gmailStatusError
		if cursor.PageToken != "" && errors.As(err, &status) && status.Status == http.StatusBadRequest {
			return ExternalMailFetch{}, errGmailPageTokenRefused
		}
		return ExternalMailFetch{}, err
	}
	ids := make([]string, 0, len(listed.Messages))
	for _, message := range listed.Messages {
		ids = append(ids, message.ID)
	}
	messages, err := g.messages(ctx, accessToken, ids)
	if err != nil {
		return ExternalMailFetch{}, err
	}
	next := gmailCursor{HistoryID: cursor.HistoryID}
	if listed.NextPageToken != "" {
		next.PageToken, next.Backfill, next.After = listed.NextPageToken, true, cursor.After
	}
	return ExternalMailFetch{Messages: messages, Cursor: next.encode(), More: listed.NextPageToken != ""}, nil
}

// historyPage reads one page of the messages added after the cursor's historyId, and of those that
// gained the INBOX label, such as mail marked as not spam. Google answers 404 once the historyId is
// too old to replay.
func (g *gmailMailSource) historyPage(ctx context.Context, accessToken string, cursor gmailCursor) (ExternalMailFetch, error) {
	query := url.Values{
		"startHistoryId": {cursor.HistoryID},
		"historyTypes":   {"messageAdded", "labelAdded"},
		"maxResults":     {strconv.Itoa(g.pageSize)},
	}
	if cursor.PageToken != "" {
		query.Set("pageToken", cursor.PageToken)
	}
	var history struct {
		History []struct {
			MessagesAdded []struct {
				Message struct {
					ID string `json:"id"`
				} `json:"message"`
			} `json:"messagesAdded"`
			LabelsAdded []struct {
				Message struct {
					ID string `json:"id"`
				} `json:"message"`
				LabelIDs []string `json:"labelIds"`
			} `json:"labelsAdded"`
		} `json:"history"`
		NextPageToken string `json:"nextPageToken"`
		HistoryID     string `json:"historyId"`
	}
	if err := g.getJSON(ctx, accessToken, "history", "/users/me/history", query, gmailMaxListBytes, &history); err != nil {
		var status *gmailStatusError
		if errors.As(err, &status) && status.Status == http.StatusNotFound {
			return ExternalMailFetch{}, fmt.Errorf("%w: gmail history id expired", ErrExternalMailCursorExpired)
		}
		return ExternalMailFetch{}, err
	}
	var ids []string
	seen := map[string]bool{}
	keep := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, record := range history.History {
		for _, added := range record.MessagesAdded {
			keep(added.Message.ID)
		}
		for _, labeled := range record.LabelsAdded {
			if slices.Contains(labeled.LabelIDs, "INBOX") {
				// A copy already stored is not repeated.
				keep(labeled.Message.ID)
			}
		}
	}
	messages, err := g.messages(ctx, accessToken, ids)
	if err != nil {
		return ExternalMailFetch{}, err
	}
	// While pages remain, the same start is replayed with the page token; the last page moves the
	// start to the mailbox's current historyId.
	next := gmailCursor{HistoryID: cursor.HistoryID, PageToken: history.NextPageToken}
	if history.NextPageToken == "" && validHistoryID(history.HistoryID) {
		next.HistoryID = history.HistoryID
	}
	return ExternalMailFetch{Messages: messages, Cursor: next.encode(), More: history.NextPageToken != ""}, nil
}

// messages reads each listed message and keeps the inbound ones. A message deleted since it was
// listed is skipped.
func (g *gmailMailSource) messages(ctx context.Context, accessToken string, ids []string) ([]ExternalMessage, error) {
	messages := make([]ExternalMessage, 0, len(ids))
	for _, id := range ids {
		if id == "" || len(id) > maxExternalIDLength {
			continue
		}
		message, found, err := g.message(ctx, accessToken, id)
		if err != nil {
			return nil, err
		}
		if !found || !gmailInbound(message.LabelIDs) {
			continue
		}
		messages = append(messages, message.external())
	}
	return messages, nil
}

// message reads one message in full format, or in metadata format when the full one is too large.
func (g *gmailMailSource) message(ctx context.Context, accessToken, id string) (gmailMessage, bool, error) {
	path := "/users/me/messages/" + url.PathEscape(id)
	var message gmailMessage
	err := g.getJSON(ctx, accessToken, "message", path, url.Values{"format": {"full"}}, g.maxMessageBytes, &message)
	if errors.Is(err, errGmailTooLarge) {
		message = gmailMessage{}
		err = g.getJSON(ctx, accessToken, "message", path, url.Values{
			"format":          {"metadata"},
			"metadataHeaders": {"From", "To", "Cc", "Subject", "Message-ID", "In-Reply-To", "References"},
		}, gmailMaxListBytes, &message)
	}
	var status *gmailStatusError
	if errors.As(err, &status) && status.Status == http.StatusNotFound {
		return gmailMessage{}, false, nil
	}
	if err != nil {
		return gmailMessage{}, false, err
	}
	if message.ID == "" {
		message.ID = id
	}
	return message, true, nil
}

// gmailInbound reports whether a message with these labels is inbound mail the connector keeps.
func gmailInbound(labels []string) bool {
	inbox, sent := false, false
	for _, label := range labels {
		switch label {
		case "DRAFT", "SPAM", "TRASH", "CHAT":
			return false
		case "INBOX":
			inbox = true
		case "SENT":
			sent = true
		}
	}
	return inbox || !sent
}

// gmailMessage is the part of a Gmail message resource the connector reads.
type gmailMessage struct {
	ID           string           `json:"id"`
	LabelIDs     []string         `json:"labelIds"`
	InternalDate string           `json:"internalDate"`
	Payload      gmailMessagePart `json:"payload"`
}

type gmailMessagePart struct {
	MimeType string `json:"mimeType"`
	Filename string `json:"filename"`
	Headers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Body struct {
		Data string `json:"data"`
	} `json:"body"`
	Parts []gmailMessagePart `json:"parts"`
}

// header returns the part's headers as a mail.Header, for the shared header decoders.
func (p gmailMessagePart) header() mail.Header {
	header := mail.Header{}
	for _, entry := range p.Headers {
		key := http.CanonicalHeaderKey(entry.Name)
		header[key] = append(header[key], entry.Value)
	}
	return header
}

// external maps the message as inbound mail would be parsed.
func (m gmailMessage) external() ExternalMessage {
	header := m.Payload.header()
	external := ExternalMessage{
		ExternalID: m.ID,
		To:         headerAddresses(header, "To"),
		Cc:         headerAddresses(header, "Cc"),
		Subject:    strings.Join(strings.Fields(truncateText(decodeHeader(header.Get("Subject")), domain.MaxMailSubjectLength)), " "),
		References: parseReferences(header.Get("References")),
	}
	parser := mail.AddressParser{WordDecoder: headerDecoder}
	if from, err := parser.Parse(header.Get("From")); err == nil {
		external.FromAddress = strings.ToLower(from.Address)
		external.FromName = truncateText(cleanText(from.Name), 255)
	}
	if id, ok := normalizeMessageID(header.Get("Message-Id")); ok {
		external.RFCMessageID = id
	}
	if id, ok := normalizeMessageID(firstField(header.Get("In-Reply-To"))); ok {
		external.InReplyTo = id
	}
	if millis, err := strconv.ParseInt(m.InternalDate, 10, 64); err == nil && millis > 0 {
		external.ReceivedAt = time.UnixMilli(millis).UTC()
	}
	walker := gmailBodyWalker{}
	walker.walk(m.Payload, 0)
	external.TextBody = truncateText(walker.text, domain.MaxMailBodyBytes)
	external.HTMLBody = truncateText(walker.html, domain.MaxMailBodyBytes)
	return external
}

// gmailBodyWalker finds the first text/plain and text/html parts that are not attachments.
type gmailBodyWalker struct {
	text, html string
	parts      int
}

func (w *gmailBodyWalker) walk(part gmailMessagePart, depth int) {
	if depth > gmailMaxMIMEDepth || w.parts >= gmailMaxMIMEParts {
		return
	}
	w.parts++
	mediaType := strings.ToLower(strings.TrimSpace(part.MimeType))
	if strings.HasPrefix(mediaType, "multipart/") {
		for _, child := range part.Parts {
			w.walk(child, depth+1)
		}
		return
	}
	header := part.header()
	if part.Filename != "" || strings.HasPrefix(strings.ToLower(strings.TrimSpace(header.Get("Content-Disposition"))), "attachment") {
		return
	}
	switch {
	case mediaType == "text/plain" && w.text == "":
		w.text = gmailPartText(header, part.Body.Data)
	case mediaType == "text/html" && w.html == "":
		w.html = gmailPartText(header, part.Body.Data)
	}
}

// gmailPartText decodes a part's base64url body and converts its charset to UTF-8. Gmail has
// already undone the transfer encoding; an unknown charset keeps the bytes as they are.
func gmailPartText(header mail.Header, data string) string {
	// Decode no more than the shared per-part bound.
	if limit := base64.RawURLEncoding.EncodedLen(maxMIMEPartBytes); len(data) > limit {
		data = data[:limit]
	}
	data = strings.TrimRight(data, "=")
	raw, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		if raw, err = base64.RawStdEncoding.DecodeString(data); err != nil {
			return ""
		}
	}
	// Parameters parsed before an invalid one are still returned and usable.
	_, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil && !errors.Is(err, mime.ErrInvalidMediaParameter) {
		params = nil
	}
	if label := strings.TrimSpace(params["charset"]); label != "" {
		raw = gmailConvertCharset(label, raw)
	}
	return cleanText(string(raw))
}

// gmailConvertCharset converts text in the named charset to UTF-8; an unknown charset keeps the
// bytes, and a conversion that breaks off keeps what was converted.
func gmailConvertCharset(label string, raw []byte) []byte {
	converted, err := charsetReader(label, strings.NewReader(string(raw)))
	if err != nil {
		return raw
	}
	decoded, err := io.ReadAll(io.LimitReader(converted, maxMIMEPartBytes))
	if err != nil && len(decoded) == 0 {
		return raw
	}
	return decoded
}

// gmailProfile is the Gmail users/me/profile resource.
type gmailProfile struct {
	EmailAddress string `json:"emailAddress"`
	HistoryID    string `json:"historyId"`
}

func (g *gmailMailSource) profile(ctx context.Context, accessToken string) (gmailProfile, error) {
	var profile gmailProfile
	err := g.getJSON(ctx, accessToken, "profile", "/users/me/profile", nil, gmailMaxListBytes, &profile)
	return profile, err
}

// Revoke hands the grant back to Google. A token Google no longer knows counts as revoked.
func (g *gmailMailSource) Revoke(ctx context.Context, token ExternalMailToken) error {
	if token.RefreshToken == "" {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.revokeEndpoint,
		strings.NewReader(url.Values{"token": {token.RefreshToken}}.Encode()))
	if err != nil {
		return fmt.Errorf("gmail revoke: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := g.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("gmail revoke: %w", redactURLError(err))
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusBadRequest {
		return nil
	}
	return &gmailStatusError{Operation: "revoke", Status: response.StatusCode}
}

// gmailStatusError is a non-success answer from Google. It carries the status and Google's short
// error reason only, never a token or response content.
type gmailStatusError struct {
	Operation string
	Status    int
	Reason    string
}

func (e *gmailStatusError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("gmail %s: HTTP %d (%s)", e.Operation, e.Status, e.Reason)
	}
	return fmt.Sprintf("gmail %s: HTTP %d", e.Operation, e.Status)
}

// token calls the token endpoint with the client credentials. Only a refused grant (invalid_grant)
// is ErrExternalMailAuth. Any other refusal, such as invalid_client after the client secret
// expired, is the operator's configuration and stays a plain error the worker retries, so it does
// not send every member to reconnect; so do 429 and 5xx.
func (g *gmailMailSource) token(ctx context.Context, operation string, form url.Values) (gmailTokenResponse, error) {
	form.Set("client_id", g.clientID)
	form.Set("client_secret", g.clientSecret)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return gmailTokenResponse{}, fmt.Errorf("gmail %s: %w", operation, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := g.httpClient.Do(request)
	if err != nil {
		return gmailTokenResponse{}, fmt.Errorf("gmail %s: %w", operation, redactURLError(err))
	}
	defer response.Body.Close()
	var tokens gmailTokenResponse
	body, readErr := readBounded(response.Body, gmailMaxTokenBytes)
	if readErr == nil {
		readErr = json.Unmarshal(body, &tokens)
	}
	if response.StatusCode != http.StatusOK {
		statusErr := &gmailStatusError{Operation: operation, Status: response.StatusCode, Reason: shortReason(tokens.Error)}
		if tokens.Error == "invalid_grant" {
			return gmailTokenResponse{}, fmt.Errorf("%w: %w", ErrExternalMailAuth, statusErr)
		}
		return gmailTokenResponse{}, statusErr
	}
	if readErr != nil {
		return gmailTokenResponse{}, fmt.Errorf("gmail %s: malformed answer", operation)
	}
	if tokens.AccessToken == "" {
		return gmailTokenResponse{}, fmt.Errorf("gmail %s: no access token in the answer", operation)
	}
	return tokens, nil
}

// getJSON calls the Gmail API and decodes a bounded answer. 401, and 403 with a reason in
// gmailGrantReasons, mean the grant no longer reads the mailbox: ErrExternalMailAuth.
func (g *gmailMailSource) getJSON(ctx context.Context, accessToken, operation, path string, query url.Values, limit int64, target any) error {
	address := g.apiBase + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("gmail %s: %w", operation, err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	response, err := g.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("gmail %s: %w", operation, redactURLError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		reason, errorDomain := gmailErrorReason(response.Body)
		statusErr := &gmailStatusError{Operation: operation, Status: response.StatusCode, Reason: reason}
		switch {
		case response.StatusCode == http.StatusUnauthorized:
			return fmt.Errorf("%w: %w", ErrExternalMailAuth, statusErr)
		case response.StatusCode == http.StatusForbidden && gmailGrantRefused(reason, errorDomain):
			return fmt.Errorf("%w: %w", ErrExternalMailAuth, statusErr)
		}
		return statusErr
	}
	body, err := readBounded(response.Body, limit)
	if err != nil {
		return fmt.Errorf("gmail %s: %w", operation, err)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("gmail %s: malformed answer", operation)
	}
	return nil
}

// gmailErrorReason reads the first error reason, and its domain, of a Google API error body.
func gmailErrorReason(body io.Reader) (reason, errorDomain string) {
	raw, err := io.ReadAll(io.LimitReader(body, gmailMaxErrorBytes))
	if err != nil {
		return "", ""
	}
	var decoded struct {
		Error struct {
			Errors []struct {
				Reason string `json:"reason"`
				Domain string `json:"domain"`
			} `json:"errors"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
			Status string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &decoded) != nil {
		return "", ""
	}
	switch {
	case len(decoded.Error.Errors) > 0:
		return shortReason(decoded.Error.Errors[0].Reason), shortReason(decoded.Error.Errors[0].Domain)
	case len(decoded.Error.Details) > 0 && decoded.Error.Details[0].Reason != "":
		return shortReason(decoded.Error.Details[0].Reason), ""
	}
	return shortReason(decoded.Error.Status), ""
}

// shortReason keeps a machine-readable reason only, so an error string never carries content.
func shortReason(reason string) string {
	if len(reason) > 64 {
		return ""
	}
	for _, char := range reason {
		switch {
		case char == '_', char == '-', char == '.':
		case 'a' <= char && char <= 'z', 'A' <= char && char <= 'Z', '0' <= char && char <= '9':
		default:
			return ""
		}
	}
	return reason
}

// readBounded reads at most limit bytes and reports errGmailTooLarge past it.
func readBounded(body io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errGmailTooLarge
	}
	return raw, nil
}

// redactURLError drops the request URL from a transport error: a query may carry a page token.
func redactURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}
	return err
}
