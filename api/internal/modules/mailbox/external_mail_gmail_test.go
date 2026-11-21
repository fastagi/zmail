package mailbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

const (
	gmailTestClientID     = "client-id.test"
	gmailTestClientSecret = "client-secret-test"
	gmailTestRefresh      = "refresh-gmail-1"
	gmailTestAccess       = "access-gmail-1"
	gmailTestStartHistory = "1000"
)

// fakeGoogle serves the parts of Google's OAuth and Gmail APIs the connector calls, from memory.
type fakeGoogle struct {
	t      *testing.T
	server *httptest.Server

	mu sync.Mutex
	// refresh holds the refresh tokens the token endpoint accepts; rotateTo is handed out once.
	refresh  map[string]bool
	rotateTo string
	// codes maps an authorization code to the refresh token it grants.
	codes   map[string]string
	address string
	// historyID is the mailbox's current historyId, as the profile and history answer it.
	historyID uint64
	// messages are the stored messages by id; order lists the backfill ids newest first.
	messages map[string]gmailMessage
	order    []string
	// added are the history records after gmailTestStartHistory, oldest first.
	added []gmailTestHistory
	// fail forces a status (and optional reason) per operation: token, profile, list, history,
	// message, revoke.
	fail map[string]gmailTestFailure
	// requests counts requests per operation; queries keeps the last query per operation.
	requests map[string]int
	queries  map[string]url.Values
	forms    map[string]url.Values
	revoked  []string
}

type gmailTestHistory struct {
	id        uint64
	messageID string
}

type gmailTestFailure struct {
	status int
	reason string
	// times bounds how often the failure is served; zero serves it always.
	times int
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	g := &fakeGoogle{
		t: t, refresh: map[string]bool{gmailTestRefresh: true}, codes: map[string]string{},
		address: "Owner@Gmail.Example", historyID: 1000, messages: map[string]gmailMessage{},
		fail: map[string]gmailTestFailure{}, requests: map[string]int{}, queries: map[string]url.Values{},
		forms: map[string]url.Values{},
	}
	g.server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.server.Close)
	return g
}

// source returns a connector pointed at the fake with the given page size.
func (g *fakeGoogle) source(pageSize int) *gmailMailSource {
	source := newGmailMailSource(config.ExternalMailOAuthClient{ClientID: gmailTestClientID, ClientSecret: gmailTestClientSecret})
	source.authEndpoint = g.server.URL + "/o/oauth2/v2/auth"
	source.tokenEndpoint = g.server.URL + "/token"
	source.apiBase = g.server.URL + "/gmail/v1"
	source.revokeEndpoint = g.server.URL + "/revoke"
	source.httpClient = g.server.Client()
	source.pageSize = pageSize
	return source
}

// store adds messages to the backfill listing, newest first in the order given.
func (g *fakeGoogle) store(messages ...gmailMessage) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, message := range messages {
		g.messages[message.ID] = message
		g.order = append(g.order, message.ID)
	}
}

// arrive stores messages that arrive after the backfill started, each as a history record.
func (g *fakeGoogle) arrive(messages ...gmailMessage) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, message := range messages {
		g.messages[message.ID] = message
		g.historyID++
		g.added = append(g.added, gmailTestHistory{id: g.historyID, messageID: message.ID})
	}
}

func (g *fakeGoogle) failing(operation string, failure gmailTestFailure) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fail[operation] = failure
}

func (g *fakeGoogle) count(operation string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.requests[operation]
}

func (g *fakeGoogle) query(operation string) url.Values {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.queries[operation]
}

func (g *fakeGoogle) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		g.t.Errorf("encode fake answer: %v", err)
	}
}

func (g *fakeGoogle) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var operation string
	switch {
	case r.URL.Path == "/token":
		operation = "token"
	case r.URL.Path == "/revoke":
		operation = "revoke"
	case r.URL.Path == "/gmail/v1/users/me/profile":
		operation = "profile"
	case r.URL.Path == "/gmail/v1/users/me/messages":
		operation = "list"
	case r.URL.Path == "/gmail/v1/users/me/history":
		operation = "history"
	case strings.HasPrefix(r.URL.Path, "/gmail/v1/users/me/messages/"):
		operation = "message"
	default:
		g.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	g.requests[operation]++
	g.queries[operation] = r.URL.Query()
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			g.t.Errorf("parse form: %v", err)
		}
		g.forms[operation] = r.PostForm
	}
	if operation != "token" && operation != "revoke" && r.Header.Get("Authorization") != "Bearer "+gmailTestAccess {
		g.writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"status": "UNAUTHENTICATED"}})
		return
	}
	if failure, failing := g.fail[operation]; failing {
		if failure.times > 0 {
			failure.times--
			if failure.times == 0 {
				delete(g.fail, operation)
			} else {
				g.fail[operation] = failure
			}
		}
		if operation == "token" {
			g.writeJSON(w, failure.status, map[string]any{"error": failure.reason})
		} else {
			g.writeJSON(w, failure.status, map[string]any{"error": map[string]any{
				"code": failure.status, "errors": []map[string]any{{"reason": failure.reason}},
			}})
		}
		return
	}
	switch operation {
	case "token":
		g.serveToken(w, r.PostForm)
	case "revoke":
		g.revoked = append(g.revoked, r.PostForm.Get("token"))
		w.WriteHeader(http.StatusOK)
	case "profile":
		g.writeJSON(w, http.StatusOK, map[string]any{"emailAddress": g.address, "historyId": strconv.FormatUint(g.historyID, 10)})
	case "list":
		g.serveList(w, r.URL.Query())
	case "history":
		g.serveHistory(w, r.URL.Query())
	case "message":
		id := strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me/messages/")
		message, found := g.messages[id]
		if !found {
			g.writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": 404, "status": "NOT_FOUND"}})
			return
		}
		if r.URL.Query().Get("format") == "metadata" {
			message.Payload.Parts, message.Payload.Body.Data = nil, ""
		}
		g.writeJSON(w, http.StatusOK, message)
	}
}

func (g *fakeGoogle) serveToken(w http.ResponseWriter, form url.Values) {
	if form.Get("client_id") != gmailTestClientID || form.Get("client_secret") != gmailTestClientSecret {
		g.writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_client"})
		return
	}
	switch form.Get("grant_type") {
	case "authorization_code":
		refresh, known := g.codes[form.Get("code")]
		if !known || form.Get("code_verifier") == "" {
			g.writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
			return
		}
		delete(g.codes, form.Get("code"))
		g.refresh[refresh] = true
		g.writeJSON(w, http.StatusOK, map[string]any{"access_token": gmailTestAccess, "refresh_token": refresh, "expires_in": 3599})
	case "refresh_token":
		if !g.refresh[form.Get("refresh_token")] {
			g.writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
			return
		}
		answer := map[string]any{"access_token": gmailTestAccess, "expires_in": 3599}
		if g.rotateTo != "" {
			delete(g.refresh, form.Get("refresh_token"))
			g.refresh[g.rotateTo] = true
			answer["refresh_token"] = g.rotateTo
			g.rotateTo = ""
		}
		g.writeJSON(w, http.StatusOK, answer)
	default:
		g.writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported_grant_type"})
	}
}

// gmailTestPageOf serves a page of n items from a "page-<offset>" token.
func gmailTestPageOf(query url.Values, n int) (start, end int, next string) {
	size, _ := strconv.Atoi(query.Get("maxResults"))
	if size <= 0 {
		size = 100
	}
	start, _ = strconv.Atoi(strings.TrimPrefix(query.Get("pageToken"), "page-"))
	start = min(start, n)
	end = min(start+size, n)
	if end < n {
		next = "page-" + strconv.Itoa(end)
	}
	return start, end, next
}

func (g *fakeGoogle) serveList(w http.ResponseWriter, query url.Values) {
	if !strings.HasPrefix(query.Get("q"), "after:") {
		g.t.Errorf("list without an after: query: %q", query.Get("q"))
	}
	start, end, next := gmailTestPageOf(query, len(g.order))
	listed := []map[string]string{}
	for _, id := range g.order[start:end] {
		listed = append(listed, map[string]string{"id": id, "threadId": "t-" + id})
	}
	answer := map[string]any{"messages": listed, "resultSizeEstimate": len(g.order)}
	if next != "" {
		answer["nextPageToken"] = next
	}
	g.writeJSON(w, http.StatusOK, answer)
}

func (g *fakeGoogle) serveHistory(w http.ResponseWriter, query url.Values) {
	startID, err := strconv.ParseUint(query.Get("startHistoryId"), 10, 64)
	if err != nil || startID < 1000 {
		g.writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": 404, "status": "NOT_FOUND"}})
		return
	}
	if query.Get("historyTypes") != "messageAdded" {
		g.t.Errorf("historyTypes = %q", query.Get("historyTypes"))
	}
	var after []gmailTestHistory
	for _, record := range g.added {
		if record.id > startID {
			after = append(after, record)
		}
	}
	start, end, next := gmailTestPageOf(query, len(after))
	records := []map[string]any{}
	for _, record := range after[start:end] {
		records = append(records, map[string]any{
			"id":            strconv.FormatUint(record.id, 10),
			"messagesAdded": []map[string]any{{"message": map[string]any{"id": record.messageID}}},
		})
	}
	answer := map[string]any{"history": records, "historyId": strconv.FormatUint(g.historyID, 10)}
	if next != "" {
		answer["nextPageToken"] = next
	}
	g.writeJSON(w, http.StatusOK, answer)
}

// gmailTestPart builds a message part with headers given as name, value pairs.
func gmailTestPart(mimeType, data string, headers ...string) gmailMessagePart {
	part := gmailMessagePart{MimeType: mimeType}
	for i := 0; i+1 < len(headers); i += 2 {
		part.Headers = append(part.Headers, struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{headers[i], headers[i+1]})
	}
	part.Body.Data = data
	return part
}

// gmailTestMessage is a plain inbound message received at the given time.
func gmailTestMessage(id string, received time.Time, labels ...string) gmailMessage {
	if len(labels) == 0 {
		labels = []string{"INBOX", "UNREAD"}
	}
	return gmailMessage{
		ID: id, LabelIDs: labels, InternalDate: strconv.FormatInt(received.UnixMilli(), 10),
		Payload: gmailTestPart("text/plain", base64.RawURLEncoding.EncodeToString([]byte("Body of "+id)),
			"From", "Carol <Carol@Sender.Test>", "To", "owner@gmail.example", "Subject", "Subject "+id,
			"Message-ID", "<"+id+"@sender.test>", "Content-Type", "text/plain; charset=UTF-8"),
	}
}

func gmailIDs(messages []ExternalMessage) []string {
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ExternalID)
	}
	return ids
}

func TestGmailAuthURL(t *testing.T) {
	google := newFakeGoogle(t)
	raw := google.source(25).AuthURL(ExternalMailAuthRequest{
		State: "state-1", CodeChallenge: pkceChallenge("verifier-1"), RedirectURI: "https://app.example/external-mail/callback",
	})
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	if got := parsed.Scheme + "://" + parsed.Host + parsed.Path; got != google.server.URL+"/o/oauth2/v2/auth" {
		t.Fatalf("auth endpoint = %s", got)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"client_id": gmailTestClientID, "redirect_uri": "https://app.example/external-mail/callback",
		"response_type": "code", "access_type": "offline", "prompt": "consent", "state": "state-1",
		"code_challenge": pkceChallenge("verifier-1"), "code_challenge_method": "S256",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	scopes := strings.Fields(query.Get("scope"))
	for _, want := range []string{"https://www.googleapis.com/auth/gmail.readonly", "openid", "email"} {
		found := false
		for _, scope := range scopes {
			found = found || scope == want
		}
		if !found {
			t.Errorf("scope %q missing from %v", want, scopes)
		}
	}
	if query.Has("client_secret") {
		t.Fatal("the auth url carries the client secret")
	}
	if production := newGmailMailSource(config.ExternalMailOAuthClient{ClientID: "x", ClientSecret: "y"}).AuthURL(ExternalMailAuthRequest{}); !strings.HasPrefix(production, gmailAuthEndpoint+"?") {
		t.Fatalf("default auth url = %s", production)
	}
}

func TestGmailExchange(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		verifier string
		fail     map[string]gmailTestFailure
		wantErr  error
		wantAuth bool
	}{
		{name: "success", code: "code-1", verifier: "verifier-1"},
		{name: "unknown code is invalid_grant", code: "code-unknown", verifier: "verifier-1", wantAuth: true},
		{name: "missing verifier", code: "code-1", verifier: "", wantAuth: true},
		{name: "token 401", code: "code-1", verifier: "verifier-1", fail: map[string]gmailTestFailure{"token": {status: 401, reason: "invalid_client"}}, wantAuth: true},
		{name: "token 503 retries", code: "code-1", verifier: "verifier-1", fail: map[string]gmailTestFailure{"token": {status: 503}}},
		{name: "profile 401", code: "code-1", verifier: "verifier-1", fail: map[string]gmailTestFailure{"profile": {status: 401}}, wantAuth: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			google := newFakeGoogle(t)
			google.codes["code-1"] = "refresh-from-code-1"
			for operation, failure := range test.fail {
				google.failing(operation, failure)
			}
			token, account, err := google.source(25).Exchange(context.Background(), ExternalMailCodeExchange{
				Code: test.code, CodeVerifier: test.verifier, RedirectURI: "https://app.example/cb",
			})
			success := !test.wantAuth && test.fail == nil
			switch {
			case success:
				if err != nil {
					t.Fatalf("Exchange: %v", err)
				}
				if token.RefreshToken != "refresh-from-code-1" || account.Address != "owner@gmail.example" {
					t.Fatalf("token %q account %q", token.RefreshToken, account.Address)
				}
				form := google.forms["token"]
				if form.Get("code_verifier") != "verifier-1" || form.Get("redirect_uri") != "https://app.example/cb" || form.Get("grant_type") != "authorization_code" {
					t.Fatalf("token form = %v", form)
				}
			case test.wantAuth:
				if !errors.Is(err, ErrExternalMailAuth) {
					t.Fatalf("err = %v, want ErrExternalMailAuth", err)
				}
			default:
				if err == nil || errors.Is(err, ErrExternalMailAuth) {
					t.Fatalf("err = %v, want a retryable error", err)
				}
			}
			if err != nil && strings.Contains(err.Error(), test.code) && test.code != "" {
				t.Fatalf("the error carries the code: %v", err)
			}
		})
	}
}

// TestGmailBackfillPagesThenHistory: an empty cursor records the profile historyId, lists the
// window newest first across pages, then switches to history from that historyId.
func TestGmailBackfillPagesThenHistory(t *testing.T) {
	google := newFakeGoogle(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	for i := 7; i >= 1; i-- {
		google.store(gmailTestMessage(fmt.Sprintf("b%d", i), now.Add(-time.Duration(i)*time.Hour)))
	}
	source := google.source(3)
	ctx := context.Background()
	request := ExternalMailFetchRequest{Token: ExternalMailToken{RefreshToken: gmailTestRefresh}, BackfillDays: 7, Now: now}

	var pages [][]string
	var mores []bool
	for page := 0; page < 3; page++ {
		fetched, err := source.Fetch(ctx, request)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if page == 0 {
			// Mail arriving while the backfill runs is replayed by history afterwards.
			google.arrive(gmailTestMessage("new-during", now))
		}
		pages = append(pages, gmailIDs(fetched.Messages))
		mores = append(mores, fetched.More)
		if fetched.RefreshToken != "" {
			t.Fatalf("an unrotated grant came back as %q", fetched.RefreshToken)
		}
		request.Cursor = fetched.Cursor
	}
	want := [][]string{{"b7", "b6", "b5"}, {"b4", "b3", "b2"}, {"b1"}}
	if fmt.Sprint(pages) != fmt.Sprint(want) || fmt.Sprint(mores) != "[true true false]" {
		t.Fatalf("pages %v more %v, want %v [true true false]", pages, mores, want)
	}
	if got, want := google.query("list").Get("q"), "after:"+strconv.FormatInt(now.AddDate(0, 0, -7).Unix(), 10); got != want {
		t.Fatalf("list q = %q, want %q", got, want)
	}
	if google.count("profile") != 1 {
		t.Fatalf("profile read %d times, want once at the backfill start", google.count("profile"))
	}
	cursor, err := decodeGmailCursor(request.Cursor)
	if err != nil || cursor.Backfill || cursor.HistoryID != gmailTestStartHistory || cursor.PageToken != "" {
		t.Fatalf("cursor after the backfill = %+v, %v; want history from %s", cursor, err, gmailTestStartHistory)
	}

	fetched, err := source.Fetch(ctx, request)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if got := gmailIDs(fetched.Messages); fmt.Sprint(got) != "[new-during]" || fetched.More {
		t.Fatalf("history page = %v more %v", got, fetched.More)
	}
	if got := google.query("history").Get("startHistoryId"); got != gmailTestStartHistory {
		t.Fatalf("history started at %q, want %s", got, gmailTestStartHistory)
	}
	if cursor, _ = decodeGmailCursor(fetched.Cursor); cursor.HistoryID != "1001" {
		t.Fatalf("cursor after history = %+v, want 1001", cursor)
	}
}

// TestGmailIncrementalPages: history pages replay the same start with a page token, then move to
// the mailbox's current historyId; drafts, spam, and sent-only mail are dropped.
func TestGmailIncrementalPages(t *testing.T) {
	google := newFakeGoogle(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	google.arrive(
		gmailTestMessage("n1", now),
		gmailTestMessage("draft", now, "DRAFT"),
		gmailTestMessage("sent", now, "SENT"),
		gmailTestMessage("self", now, "SENT", "INBOX"),
		gmailTestMessage("spam", now, "SPAM"),
		gmailTestMessage("archived", now, "CATEGORY_UPDATES"),
	)
	source := google.source(4)
	request := ExternalMailFetchRequest{
		Token: ExternalMailToken{RefreshToken: gmailTestRefresh}, Cursor: gmailCursor{HistoryID: gmailTestStartHistory}.encode(), Now: now,
	}
	first, err := source.Fetch(context.Background(), request)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if got := gmailIDs(first.Messages); fmt.Sprint(got) != "[n1 self]" || !first.More {
		t.Fatalf("first page = %v more %v", got, first.More)
	}
	request.Cursor = first.Cursor
	second, err := source.Fetch(context.Background(), request)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if got := gmailIDs(second.Messages); fmt.Sprint(got) != "[archived]" || second.More {
		t.Fatalf("second page = %v more %v", got, second.More)
	}
	if query := google.query("history"); query.Get("startHistoryId") != gmailTestStartHistory || query.Get("pageToken") != "page-4" {
		t.Fatalf("second history query = %v", query)
	}
	if cursor, _ := decodeGmailCursor(second.Cursor); cursor.HistoryID != "1006" || cursor.PageToken != "" {
		t.Fatalf("cursor = %+v, want 1006 without a page", cursor)
	}
	request.Cursor = second.Cursor
	idle, err := source.Fetch(context.Background(), request)
	if err != nil || len(idle.Messages) != 0 || idle.More || idle.Cursor != second.Cursor {
		t.Fatalf("idle fetch = %+v, %v", idle, err)
	}
}

func TestGmailFetchErrors(t *testing.T) {
	historyCursor := gmailCursor{HistoryID: gmailTestStartHistory}.encode()
	tests := []struct {
		name    string
		cursor  string
		token   string
		fail    map[string]gmailTestFailure
		want    error
		wantAny bool
	}{
		{name: "history 404 expires the cursor", cursor: gmailCursor{HistoryID: "5"}.encode(), want: ErrExternalMailCursorExpired},
		{name: "unreadable cursor expires", cursor: "17", want: ErrExternalMailCursorExpired},
		{name: "backfill page token refused", cursor: gmailCursor{HistoryID: "1000", PageToken: "stale", Backfill: true}.encode(),
			fail: map[string]gmailTestFailure{"list": {status: 400, reason: "invalidArgument"}}, want: ErrExternalMailCursorExpired},
		{name: "revoked refresh token", cursor: historyCursor, token: "refresh-revoked", want: ErrExternalMailAuth},
		{name: "token 401", cursor: historyCursor, fail: map[string]gmailTestFailure{"token": {status: 401, reason: "invalid_client"}}, want: ErrExternalMailAuth},
		{name: "api 401", cursor: historyCursor, fail: map[string]gmailTestFailure{"history": {status: 401}}, want: ErrExternalMailAuth},
		{name: "scope withdrawn 403", cursor: "", fail: map[string]gmailTestFailure{"profile": {status: 403, reason: "insufficientPermissions"}}, want: ErrExternalMailAuth},
		{name: "token 429", cursor: historyCursor, fail: map[string]gmailTestFailure{"token": {status: 429}}, wantAny: true},
		{name: "history 429", cursor: historyCursor, fail: map[string]gmailTestFailure{"history": {status: 429, reason: "rateLimitExceeded"}}, wantAny: true},
		{name: "rate limit 403", cursor: "", fail: map[string]gmailTestFailure{"list": {status: 403, reason: "userRateLimitExceeded"}}, wantAny: true},
		{name: "message 500", cursor: historyCursor, fail: map[string]gmailTestFailure{"message": {status: 500, reason: "backendError"}}, wantAny: true},
		{name: "list 503", cursor: "", fail: map[string]gmailTestFailure{"list": {status: 503}}, wantAny: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			google := newFakeGoogle(t)
			now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
			google.store(gmailTestMessage("b1", now))
			google.arrive(gmailTestMessage("n1", now))
			for operation, failure := range test.fail {
				google.failing(operation, failure)
			}
			token := test.token
			if token == "" {
				token = gmailTestRefresh
			}
			_, err := google.source(25).Fetch(context.Background(), ExternalMailFetchRequest{
				Token: ExternalMailToken{RefreshToken: token}, Cursor: test.cursor, Now: now,
			})
			if err == nil {
				t.Fatal("Fetch succeeded")
			}
			if test.wantAny {
				if errors.Is(err, ErrExternalMailAuth) || errors.Is(err, ErrExternalMailCursorExpired) {
					t.Fatalf("err = %v, want a plain retryable error", err)
				}
			} else if !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
			for _, secret := range []string{token, gmailTestAccess, gmailTestClientSecret, "Body of"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("the error %q carries %q", err, secret)
				}
			}
		})
	}
}

// TestGmailRotatedRefreshToken: a refresh token Google rotates comes back for the worker to seal;
// an unchanged one does not.
func TestGmailRotatedRefreshToken(t *testing.T) {
	google := newFakeGoogle(t)
	google.rotateTo = "refresh-gmail-2"
	source := google.source(25)
	request := ExternalMailFetchRequest{Token: ExternalMailToken{RefreshToken: gmailTestRefresh}, Cursor: gmailCursor{HistoryID: gmailTestStartHistory}.encode()}
	fetched, err := source.Fetch(context.Background(), request)
	if err != nil || fetched.RefreshToken != "refresh-gmail-2" {
		t.Fatalf("rotated fetch = %q, %v", fetched.RefreshToken, err)
	}
	if _, err = source.Fetch(context.Background(), request); !errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("the spent token: err = %v, want ErrExternalMailAuth", err)
	}
	request.Token.RefreshToken = "refresh-gmail-2"
	if fetched, err = source.Fetch(context.Background(), request); err != nil || fetched.RefreshToken != "" {
		t.Fatalf("fetch with the rotated token = %q, %v", fetched.RefreshToken, err)
	}
}

func TestGmailMessageDecoding(t *testing.T) {
	latin1 := base64.URLEncoding.EncodeToString([]byte("Caf\xe9 cr\xe8me")) // padded base64url
	html := base64.RawURLEncoding.EncodeToString([]byte("<p>Café ✓ ?>?</p>"))
	alternative := gmailTestPart("multipart/alternative", "")
	alternative.Parts = []gmailMessagePart{
		gmailTestPart("text/plain", latin1, "Content-Type", "text/plain; charset=ISO-8859-1"),
		gmailTestPart("text/html", html, "Content-Type", "text/html; charset=utf-8"),
	}
	attachment := gmailTestPart("text/plain", base64.RawURLEncoding.EncodeToString([]byte("attached notes")),
		"Content-Disposition", "attachment; filename=notes.txt")
	attachment.Filename = "notes.txt"
	root := gmailTestPart("multipart/mixed", "",
		"From", "=?UTF-8?B?w4lsaXNl?= <Elise@Sender.Test>", "To", "a@x.test, B <b@x.test>", "Cc", "c@x.test",
		"Subject", "=?ISO-8859-1?Q?R=E9union?=  du\r\n lundi", "Message-ID", "<m1@sender.test>",
		"In-Reply-To", "<parent@sender.test>", "References", "<root@sender.test> <parent@sender.test>")
	root.Parts = []gmailMessagePart{alternative, attachment}
	received := time.Date(2026, 11, 20, 8, 30, 0, 0, time.UTC)

	tests := []struct {
		name    string
		message gmailMessage
		check   func(t *testing.T, got ExternalMessage)
	}{
		{name: "multipart alternative with charsets", message: gmailMessage{
			ID: "m1", LabelIDs: []string{"INBOX"}, InternalDate: strconv.FormatInt(received.UnixMilli(), 10), Payload: root,
		}, check: func(t *testing.T, got ExternalMessage) {
			want := ExternalMessage{
				ExternalID: "m1", FromAddress: "elise@sender.test", FromName: "Élise", To: []string{"a@x.test", "b@x.test"},
				Cc: []string{"c@x.test"}, Subject: "Réunion du lundi", TextBody: "Café crème", HTMLBody: "<p>Café ✓ ?>?</p>",
				RFCMessageID: "m1@sender.test", InReplyTo: "parent@sender.test",
				References: []string{"root@sender.test", "parent@sender.test"}, ReceivedAt: received,
			}
			if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
				t.Fatalf("got  %+v\nwant %+v", got, want)
			}
		}},
		{name: "unknown charset keeps the bytes", message: gmailMessage{ID: "m2", Payload: gmailTestPart("text/plain",
			base64.RawURLEncoding.EncodeToString([]byte("plain words")), "Content-Type", "text/plain; charset=x-no-such-charset")},
			check: func(t *testing.T, got ExternalMessage) {
				if got.TextBody != "plain words" || !got.ReceivedAt.IsZero() {
					t.Fatalf("text %q received %v", got.TextBody, got.ReceivedAt)
				}
			}},
		{name: "invalid bytes become valid UTF-8", message: gmailMessage{ID: "m3", Payload: gmailTestPart("text/html",
			base64.RawURLEncoding.EncodeToString([]byte("a\xffb\x00c")))},
			check: func(t *testing.T, got ExternalMessage) {
				if got.HTMLBody != "a�bc" || got.TextBody != "" {
					t.Fatalf("html %q text %q", got.HTMLBody, got.TextBody)
				}
			}},
		{name: "oversized body is truncated", message: gmailMessage{ID: "m4", Payload: gmailTestPart("text/plain",
			base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("é", domain.MaxMailBodyBytes))))},
			check: func(t *testing.T, got ExternalMessage) {
				if len(got.TextBody) > domain.MaxMailBodyBytes || len(got.TextBody) < domain.MaxMailBodyBytes-1 || !strings.HasSuffix(got.TextBody, "é") {
					t.Fatalf("truncated body is %d bytes", len(got.TextBody))
				}
			}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.check(t, test.message.external())
		})
	}
}

// TestGmailMessageEdges: a message deleted after listing is skipped, and one too large in full
// format is read again with headers only.
func TestGmailMessageEdges(t *testing.T) {
	google := newFakeGoogle(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	big := gmailTestMessage("big", now)
	big.Payload.Body.Data = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 64<<10)))
	google.arrive(gmailTestMessage("gone", now), big)
	delete(google.messages, "gone")
	source := google.source(25)
	source.maxMessageBytes = 16 << 10
	fetched, err := source.Fetch(context.Background(), ExternalMailFetchRequest{
		Token: ExternalMailToken{RefreshToken: gmailTestRefresh}, Cursor: gmailCursor{HistoryID: gmailTestStartHistory}.encode(),
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(fetched.Messages) != 1 || fetched.Messages[0].ExternalID != "big" || fetched.Messages[0].Subject != "Subject big" || fetched.Messages[0].TextBody != "" {
		t.Fatalf("messages = %+v", fetched.Messages)
	}
	if google.query("message").Get("format") != "metadata" {
		t.Fatalf("the oversized message was not read again as metadata: %v", google.query("message"))
	}
}

func TestGmailInboundLabels(t *testing.T) {
	tests := []struct {
		labels []string
		want   bool
	}{
		{[]string{"INBOX", "UNREAD"}, true},
		{[]string{"CATEGORY_PROMOTIONS"}, true},
		{nil, true},
		{[]string{"SENT"}, false},
		{[]string{"SENT", "INBOX"}, true},
		{[]string{"DRAFT"}, false},
		{[]string{"INBOX", "SPAM"}, false},
		{[]string{"TRASH"}, false},
	}
	for _, test := range tests {
		if got := gmailInbound(test.labels); got != test.want {
			t.Errorf("gmailInbound(%v) = %v, want %v", test.labels, got, test.want)
		}
	}
}

func TestGmailRevoke(t *testing.T) {
	google := newFakeGoogle(t)
	source := google.source(25)
	if err := source.Revoke(context.Background(), ExternalMailToken{RefreshToken: gmailTestRefresh}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if len(google.revoked) != 1 || google.revoked[0] != gmailTestRefresh {
		t.Fatalf("revoked = %v", google.revoked)
	}
	google.failing("revoke", gmailTestFailure{status: 400, reason: "invalid_token"})
	if err := source.Revoke(context.Background(), ExternalMailToken{RefreshToken: gmailTestRefresh}); err != nil {
		t.Fatalf("revoking an unknown token: %v", err)
	}
	google.failing("revoke", gmailTestFailure{status: 503})
	if err := source.Revoke(context.Background(), ExternalMailToken{RefreshToken: gmailTestRefresh}); err == nil || strings.Contains(err.Error(), gmailTestRefresh) {
		t.Fatalf("revoke during an outage: %v", err)
	}
}

func TestGmailRegistration(t *testing.T) {
	client := config.ExternalMailOAuthClient{ClientID: gmailTestClientID, ClientSecret: gmailTestClientSecret}
	tests := []struct {
		name string
		cfg  config.ExternalMailConfig
		want bool
	}{
		{name: "enabled", cfg: config.ExternalMailConfig{Providers: []string{"gmail"}, OAuth: map[string]config.ExternalMailOAuthClient{"gmail": client}}, want: true},
		{name: "not listed", cfg: config.ExternalMailConfig{Providers: []string{"outlook"}, OAuth: map[string]config.ExternalMailOAuthClient{"gmail": client}}},
		{name: "no client", cfg: config.ExternalMailConfig{Providers: []string{"gmail"}}},
		{name: "no secret", cfg: config.ExternalMailConfig{Providers: []string{"gmail"}, OAuth: map[string]config.ExternalMailOAuthClient{"gmail": {ClientID: "id"}}}},
		{name: "off", cfg: config.ExternalMailConfig{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connector, found := newExternalMailRegistry(test.cfg).lookup(domain.ExternalMailProviderGmail)
			if found != test.want {
				t.Fatalf("gmail registered = %v, want %v", found, test.want)
			}
			if !found {
				return
			}
			source, real := connector.Source.(*gmailMailSource)
			if !real || connector.AuthKind != ExternalMailAuthOAuth2 || !connector.Capabilities.Read {
				t.Fatalf("connector = %+v", connector)
			}
			if source.tokenEndpoint != gmailTokenEndpoint || source.apiBase != gmailAPIBase || source.clientID != gmailTestClientID {
				t.Fatalf("source endpoints %q %q client %q", source.tokenEndpoint, source.apiBase, source.clientID)
			}
		})
	}
}

// TestGmailSyncStoresOnce runs the sync worker against the fake Google: the backfill pages and the
// history are stored once, as gmail copies, and a restarted backfill adds no second copy.
func TestGmailSyncStoresOnce(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailProviderGmail)
	google := newFakeGoogle(t)
	google.refresh = map[string]bool{externalTestToken: true}
	now := f.service.now().UTC()
	for i := 5; i >= 1; i-- {
		google.store(gmailTestMessage(fmt.Sprintf("b%d", i), now.Add(-time.Duration(i)*time.Hour)))
	}
	google.store(gmailTestMessage("draft", now.Add(-time.Hour), "DRAFT"))
	source := google.source(2)
	f.service.externalConnectors = &externalMailRegistry{connectors: []ExternalMailConnector{{
		ID: domain.ExternalMailProviderGmail, AuthKind: ExternalMailAuthOAuth2,
		Capabilities: ExternalMailCapabilities{Read: true}, Source: source,
	}}}

	// Three backfill pages are fetched back to back without waiting for the interval.
	for pass := 0; pass < 3; pass++ {
		if handled := f.sync(t); handled != 1 {
			t.Fatalf("pass %d handled %d connections, want 1", pass, handled)
		}
	}
	if handled := f.sync(t); handled != 0 {
		t.Fatalf("after the backfill a pass within the interval handled %d", handled)
	}
	stored := f.storedExternal(t)
	if len(stored) != 5 {
		t.Fatalf("stored %d messages, want 5", len(stored))
	}
	for _, message := range stored {
		if message.Source != domain.MailSourceGmail || message.Direction != domain.MailDirectionInbound || message.ExternalID == "draft" {
			t.Fatalf("stored %+v", message)
		}
	}
	connection := f.reload(t)
	cursor, err := decodeGmailCursor(connection.SyncCursor)
	if err != nil || cursor.Backfill || cursor.HistoryID != gmailTestStartHistory || connection.LastErrorCode != "" {
		t.Fatalf("connection cursor %q error %q", connection.SyncCursor, connection.LastErrorCode)
	}

	google.arrive(gmailTestMessage("n1", now))
	f.advance(externalMailSyncInterval)
	f.sync(t)
	if stored = f.storedExternal(t); len(stored) != 6 {
		t.Fatalf("after history the mailbox holds %d messages, want 6", len(stored))
	}

	// An expired history id starts the backfill over; nothing is stored twice.
	google.mu.Lock()
	google.added = nil
	google.mu.Unlock()
	if err := f.repo.(*repository).db.Exec(`UPDATE external_mail_connections SET sync_cursor = ? WHERE id = ?`,
		gmailCursor{HistoryID: "5"}.encode(), f.connection.ID).Error; err != nil {
		t.Fatalf("age the cursor: %v", err)
	}
	f.advance(externalMailSyncInterval)
	for pass := 0; pass < 4; pass++ {
		f.sync(t)
	}
	if stored = f.storedExternal(t); len(stored) != 6 {
		t.Fatalf("after a restarted backfill the mailbox holds %d messages, want 6", len(stored))
	}
	if connection = f.reload(t); connection.Status != domain.ExternalMailStatusActive || connection.LastErrorCode != "" {
		t.Fatalf("connection status %q error %q", connection.Status, connection.LastErrorCode)
	}
}
