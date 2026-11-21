package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

var outlookTestNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

// fakeMicrosoft is a Microsoft identity platform and Graph server in memory. It serves one
// account's Inbox through message delta: a skiptoken pages a round, a deltatoken starts the next
// round at the message count the previous one ended with. Every redemption issues a new refresh
// token; as at Microsoft, the redeemed one stays valid until it expires.
type fakeMicrosoft struct {
	t      *testing.T
	server *httptest.Server

	mu         sync.Mutex
	pageSize   int
	validRT    map[string]bool
	issued     int
	codes      map[string]string // code -> PKCE challenge
	accessOK   map[string]bool
	inbox      []map[string]any
	removed    []string
	profile    map[string]any
	tokenFail  *fakeMicrosoftFailure
	graphFail  *fakeMicrosoftFailure
	lastForm   url.Values
	lastPrefer []string
	lastQuery  url.Values
	deltaCalls int
}

type fakeMicrosoftFailure struct {
	status int
	code   string
}

func newFakeMicrosoft(t *testing.T) *fakeMicrosoft {
	t.Helper()
	f := &fakeMicrosoft{
		t: t, pageSize: 3, validRT: map[string]bool{}, codes: map[string]string{}, accessOK: map[string]bool{},
		profile: map[string]any{"mail": "dana@contoso.example", "userPrincipalName": "dana@contoso.onmicrosoft.example"},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

// source returns an Outlook source pointed at the fake server.
func (f *fakeMicrosoft) source(tenant string) *outlookMailSource {
	source := newOutlookMailSource(config.ExternalMailOAuthClient{ClientID: "client-1", ClientSecret: "secret-1", Tenant: tenant})
	source.loginBaseURL = f.server.URL + "/login"
	source.graphBaseURL = f.server.URL + "/v1.0"
	return source
}

func (f *fakeMicrosoft) grantRefreshToken(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.validRT[token] = true
}

func (f *fakeMicrosoft) deliver(messages ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inbox = append(f.inbox, messages...)
}

func (f *fakeMicrosoft) failNextGraph(status int, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.graphFail = &fakeMicrosoftFailure{status: status, code: code}
}

func outlookTestMessage(id, subject string, received time.Time) map[string]any {
	return map[string]any{
		"id": id, "internetMessageId": "<" + id + "@sender.example>", "subject": subject,
		"from":             map[string]any{"emailAddress": map[string]any{"name": "Carol", "address": "carol@sender.example"}},
		"toRecipients":     []any{map[string]any{"emailAddress": map[string]any{"name": "Dana", "address": "dana@contoso.example"}}},
		"ccRecipients":     []any{},
		"receivedDateTime": received.UTC().Format(time.RFC3339),
		"isDraft":          false,
		"body":             map[string]any{"contentType": "text", "content": "Body of " + subject},
	}
}

func (f *fakeMicrosoft) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasPrefix(r.URL.Path, "/login/") && strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
		f.serveToken(w, r)
	case r.URL.Path == "/v1.0/me":
		if !f.authorized(r) {
			writeFakeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "InvalidAuthenticationToken"}})
			return
		}
		writeFakeJSON(w, http.StatusOK, f.profile)
	case r.URL.Path == "/v1.0/me/mailFolders/inbox/messages/delta":
		f.serveDelta(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeMicrosoft) authorized(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && f.accessOK[token]
}

func (f *fakeMicrosoft) serveToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		return
	}
	f.lastForm = r.PostForm
	if failure := f.tokenFail; failure != nil {
		f.tokenFail = nil
		writeFakeJSON(w, failure.status, map[string]any{"error": failure.code})
		return
	}
	if r.PostForm.Get("client_id") != "client-1" || r.PostForm.Get("client_secret") != "secret-1" {
		writeFakeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_client"})
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		challenge, known := f.codes[r.PostForm.Get("code")]
		if !known || challenge != pkceChallenge(r.PostForm.Get("code_verifier")) {
			writeFakeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
			return
		}
		delete(f.codes, r.PostForm.Get("code"))
	case "refresh_token":
		token := r.PostForm.Get("refresh_token")
		if !f.validRT[token] {
			writeFakeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
			return
		}
	default:
		writeFakeJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported_grant_type"})
		return
	}
	f.issued++
	refresh, access := fmt.Sprintf("rt-%d", f.issued), fmt.Sprintf("at-%d", f.issued)
	f.validRT[refresh], f.accessOK[access] = true, true
	writeFakeJSON(w, http.StatusOK, map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "Bearer"})
}

func (f *fakeMicrosoft) serveDelta(w http.ResponseWriter, r *http.Request) {
	f.deltaCalls++
	f.lastPrefer = r.Header.Values("Prefer")
	f.lastQuery = r.URL.Query()
	if !f.authorized(r) {
		writeFakeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": "InvalidAuthenticationToken"}})
		return
	}
	if failure := f.graphFail; failure != nil {
		f.graphFail = nil
		writeFakeJSON(w, failure.status, map[string]any{"error": map[string]any{"code": failure.code}})
		return
	}
	query := r.URL.Query()
	var start, end int
	var since time.Time
	switch {
	case query.Get("$skiptoken") != "":
		parts := strings.Split(query.Get("$skiptoken"), "-")
		start, _ = strconv.Atoi(parts[0])
		end, _ = strconv.Atoi(parts[1])
		since, _ = time.Parse(time.RFC3339, query.Get("since"))
	case query.Get("$deltatoken") != "":
		start, _ = strconv.Atoi(query.Get("$deltatoken"))
		end = len(f.inbox)
	default:
		if query.Get("$select") == "" {
			writeFakeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "BadRequest"}})
			return
		}
		raw, ok := strings.CutPrefix(query.Get("$filter"), "receivedDateTime ge ")
		parsed, err := time.Parse(time.RFC3339, raw)
		if !ok || err != nil {
			writeFakeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "BadRequest"}})
			return
		}
		since, end = parsed, len(f.inbox)
	}
	var value []any
	if start == 0 || query.Get("$deltatoken") != "" {
		// Removals surface at the start of a round.
		for _, id := range f.removed {
			value = append(value, map[string]any{"id": id, "@removed": map[string]any{"reason": "deleted"}})
		}
		f.removed = nil
	}
	stop := min(end, start+f.pageSize)
	for _, message := range f.inbox[start:stop] {
		received, _ := time.Parse(time.RFC3339, message["receivedDateTime"].(string))
		if !since.IsZero() && received.Before(since) {
			continue
		}
		value = append(value, message)
	}
	base := f.server.URL + "/v1.0/me/mailFolders/inbox/messages/delta"
	page := map[string]any{"value": value}
	if stop < end {
		next := url.Values{"$skiptoken": {fmt.Sprintf("%d-%d", stop, end)}}
		if !since.IsZero() {
			next.Set("since", since.Format(time.RFC3339))
		}
		page["@odata.nextLink"] = base + "?" + next.Encode()
	} else {
		page["@odata.deltaLink"] = base + "?" + url.Values{"$deltatoken": {strconv.Itoa(end)}}.Encode()
	}
	writeFakeJSON(w, http.StatusOK, page)
}

func writeFakeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func TestOutlookAuthURL(t *testing.T) {
	tests := []struct {
		name, tenant, wantTenant string
	}{
		{"default tenant", "", "common"},
		{"named tenant", "contoso.onmicrosoft.com", "contoso.onmicrosoft.com"},
		{"organizations", "organizations", "organizations"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := newOutlookMailSource(config.ExternalMailOAuthClient{ClientID: "client-1", ClientSecret: "secret-1", Tenant: tt.tenant})
			raw := source.AuthURL(ExternalMailAuthRequest{State: "state-1", CodeChallenge: "challenge-1", RedirectURI: "https://app.example/callback"})
			parsed, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parse %q: %v", raw, err)
			}
			if got := parsed.Scheme + "://" + parsed.Host + parsed.Path; got != "https://login.microsoftonline.com/"+tt.wantTenant+"/oauth2/v2.0/authorize" {
				t.Fatalf("endpoint = %q", got)
			}
			want := map[string]string{
				"client_id": "client-1", "response_type": "code", "redirect_uri": "https://app.example/callback",
				"response_mode": "query", "state": "state-1", "code_challenge": "challenge-1", "code_challenge_method": "S256",
				"scope": "offline_access https://graph.microsoft.com/Mail.Read https://graph.microsoft.com/User.Read openid email",
			}
			for key, value := range want {
				if got := parsed.Query().Get(key); got != value {
					t.Errorf("%s = %q, want %q", key, got, value)
				}
			}
		})
	}
}

func TestOutlookExchange(t *testing.T) {
	tests := []struct {
		name        string
		profile     map[string]any
		code        string
		verifier    string
		tokenFail   *fakeMicrosoftFailure
		wantAddress string
		wantAuthErr bool
	}{
		{name: "mail address", code: "code-1", verifier: "verifier-1", wantAddress: "dana@contoso.example",
			profile: map[string]any{"mail": "dana@contoso.example", "userPrincipalName": "dana@contoso.onmicrosoft.example"}},
		{name: "falls back to the principal name", code: "code-1", verifier: "verifier-1", wantAddress: "erin@outlook.example",
			profile: map[string]any{"mail": nil, "userPrincipalName": "erin@outlook.example"}},
		{name: "wrong verifier", code: "code-1", verifier: "other", wantAuthErr: true},
		{name: "unknown code", code: "code-2", verifier: "verifier-1", wantAuthErr: true},
		{name: "consent refused", code: "code-1", verifier: "verifier-1", wantAuthErr: true,
			tokenFail: &fakeMicrosoftFailure{status: http.StatusUnauthorized, code: "invalid_client"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := newFakeMicrosoft(t)
			if tt.profile != nil {
				ms.profile = tt.profile
			}
			ms.codes["code-1"] = pkceChallenge("verifier-1")
			ms.tokenFail = tt.tokenFail
			token, account, err := ms.source("").Exchange(context.Background(), ExternalMailCodeExchange{
				Code: tt.code, CodeVerifier: tt.verifier, RedirectURI: "https://app.example/callback",
			})
			if tt.wantAuthErr {
				if !errors.Is(err, ErrExternalMailAuth) {
					t.Fatalf("err = %v, want ErrExternalMailAuth", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Exchange: %v", err)
			}
			if account.Address != tt.wantAddress || token.RefreshToken != "rt-1" {
				t.Fatalf("account %q token %q", account.Address, token.RefreshToken)
			}
			if got := ms.lastForm.Get("redirect_uri"); got != "https://app.example/callback" {
				t.Fatalf("redirect_uri = %q", got)
			}
			if !strings.Contains(ms.lastForm.Get("scope"), "offline_access") {
				t.Fatalf("scope = %q", ms.lastForm.Get("scope"))
			}
		})
	}
}

// TestOutlookDeltaRounds: a first round pages through the backfill window and ends in a delta
// link; the next round reads only new mail and skips removals and drafts. Every fetch hands back
// the refresh token Microsoft rotated.
func TestOutlookDeltaRounds(t *testing.T) {
	ms := newFakeMicrosoft(t)
	ms.grantRefreshToken("rt-0")
	ms.deliver(outlookTestMessage("old", "Too old", outlookTestNow.AddDate(0, 0, -30)))
	for i := 1; i <= 6; i++ {
		ms.deliver(outlookTestMessage(fmt.Sprintf("m-%d", i), fmt.Sprintf("Message %d", i), outlookTestNow.Add(-time.Duration(i)*time.Hour)))
	}
	source := ms.source("")

	token, cursor := "rt-0", ""
	var got []string
	tokens := map[string]bool{token: true}
	fetch := func() ExternalMailFetch {
		t.Helper()
		fetched, err := source.Fetch(context.Background(), ExternalMailFetchRequest{
			Token: ExternalMailToken{RefreshToken: token}, Cursor: cursor, BackfillDays: 14, Now: outlookTestNow,
		})
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if fetched.RefreshToken == "" || tokens[fetched.RefreshToken] {
			t.Fatalf("fetch returned refresh token %q, want a new one", fetched.RefreshToken)
		}
		tokens[fetched.RefreshToken] = true
		token, cursor = fetched.RefreshToken, fetched.Cursor
		for _, message := range fetched.Messages {
			got = append(got, message.ExternalID)
		}
		return fetched
	}

	wantMore := []bool{true, true, false}
	for page, more := range wantMore {
		if fetched := fetch(); fetched.More != more {
			t.Fatalf("page %d: More = %v, want %v", page+1, fetched.More, more)
		}
		if page == 0 {
			wantFilter := "receivedDateTime ge " + outlookTestNow.AddDate(0, 0, -14).Format(time.RFC3339)
			if filter := ms.lastQuery.Get("$filter"); filter != wantFilter {
				t.Fatalf("$filter = %q, want %q", filter, wantFilter)
			}
			if !strings.Contains(ms.lastQuery.Get("$select"), "internetMessageHeaders") {
				t.Fatalf("$select = %q", ms.lastQuery.Get("$select"))
			}
		}
	}
	if strings.Join(got, ",") != "m-1,m-2,m-3,m-4,m-5,m-6" {
		t.Fatalf("first round = %v", got)
	}
	prefer := strings.Join(ms.lastPrefer, ";")
	if !strings.Contains(prefer, "odata.maxpagesize=25") || !strings.Contains(prefer, `IdType="ImmutableId"`) {
		t.Fatalf("Prefer = %q", prefer)
	}
	if !strings.Contains(cursor, "deltatoken") {
		t.Fatalf("cursor after the round holds no delta link: %s", cursor)
	}

	// The incremental round: two new messages, a removal, and a draft.
	got = nil
	draft := outlookTestMessage("draft-1", "Draft", outlookTestNow)
	draft["isDraft"] = true
	ms.deliver(outlookTestMessage("m-7", "Seven", outlookTestNow), draft, outlookTestMessage("m-8", "Eight", outlookTestNow))
	ms.removed = []string{"m-2"}
	if fetched := fetch(); fetched.More {
		t.Fatalf("incremental round has More set")
	}
	if strings.Join(got, ",") != "m-7,m-8" {
		t.Fatalf("incremental round = %v", got)
	}

	// Nothing new: a round with no messages still rotates the token and settles.
	got = nil
	if fetched := fetch(); fetched.More || len(got) != 0 {
		t.Fatalf("empty round: More %v messages %v", fetched.More, got)
	}

	// A revoked grant is refused.
	ms.mu.Lock()
	delete(ms.validRT, token)
	ms.mu.Unlock()
	_, err := source.Fetch(context.Background(), ExternalMailFetchRequest{Token: ExternalMailToken{RefreshToken: token}, Cursor: cursor, Now: outlookTestNow})
	if !errors.Is(err, ErrExternalMailAuth) {
		t.Fatalf("revoked token: err = %v, want ErrExternalMailAuth", err)
	}
}

func TestOutlookMessageMapping(t *testing.T) {
	ms := newFakeMicrosoft(t)
	ms.grantRefreshToken("rt-0")
	message := outlookTestMessage("AAkALgAAAAAAHYQDEapmEc2byACqAC-EWg0A", "Re: Plan", outlookTestNow)
	message["body"] = map[string]any{"contentType": "html", "content": "<p>Hello</p>"}
	message["ccRecipients"] = []any{map[string]any{"emailAddress": map[string]any{"address": "frank@contoso.example"}}}
	message["internetMessageHeaders"] = []any{
		map[string]any{"name": "In-Reply-To", "value": "<root@sender.example>"},
		map[string]any{"name": "References", "value": "<a@sender.example> <root@sender.example>"},
	}
	ms.deliver(message)
	fetched, err := ms.source("").Fetch(context.Background(), ExternalMailFetchRequest{
		Token: ExternalMailToken{RefreshToken: "rt-0"}, BackfillDays: 3, Now: outlookTestNow,
	})
	if err != nil || len(fetched.Messages) != 1 {
		t.Fatalf("Fetch: %v, %d messages", err, len(fetched.Messages))
	}
	got := fetched.Messages[0]
	if got.ExternalID != "AAkALgAAAAAAHYQDEapmEc2byACqAC-EWg0A" || got.RFCMessageID != "AAkALgAAAAAAHYQDEapmEc2byACqAC-EWg0A@sender.example" {
		t.Fatalf("ids: %q %q", got.ExternalID, got.RFCMessageID)
	}
	if got.FromAddress != "carol@sender.example" || got.FromName != "Carol" || got.Subject != "Re: Plan" {
		t.Fatalf("sender %q %q subject %q", got.FromAddress, got.FromName, got.Subject)
	}
	if strings.Join(got.To, ",") != "dana@contoso.example" || strings.Join(got.Cc, ",") != "frank@contoso.example" {
		t.Fatalf("to %v cc %v", got.To, got.Cc)
	}
	if got.HTMLBody != "<p>Hello</p>" || got.TextBody != "" {
		t.Fatalf("bodies: html %q text %q", got.HTMLBody, got.TextBody)
	}
	if got.InReplyTo != "root@sender.example" || strings.Join(got.References, ",") != "a@sender.example,root@sender.example" {
		t.Fatalf("threading: %q %v", got.InReplyTo, got.References)
	}
	if !got.ReceivedAt.Equal(outlookTestNow) {
		t.Fatalf("received %v", got.ReceivedAt)
	}
}

func TestOutlookFetchErrors(t *testing.T) {
	tests := []struct {
		name        string
		graph       *fakeMicrosoftFailure
		token       *fakeMicrosoftFailure
		wantExpired bool
		wantAuth    bool
	}{
		{name: "gone", graph: &fakeMicrosoftFailure{http.StatusGone, "SyncStateNotFound"}, wantExpired: true},
		{name: "sync state not found", graph: &fakeMicrosoftFailure{http.StatusBadRequest, "syncStateNotFound"}, wantExpired: true},
		{name: "resync required", graph: &fakeMicrosoftFailure{http.StatusBadRequest, "resyncRequired"}, wantExpired: true},
		{name: "graph unauthorized", graph: &fakeMicrosoftFailure{http.StatusUnauthorized, "InvalidAuthenticationToken"}, wantAuth: true},
		{name: "refresh refused", token: &fakeMicrosoftFailure{http.StatusBadRequest, "invalid_grant"}, wantAuth: true},
		{name: "throttled", graph: &fakeMicrosoftFailure{http.StatusTooManyRequests, "ApplicationThrottled"}},
		{name: "server error", graph: &fakeMicrosoftFailure{http.StatusServiceUnavailable, "ServiceNotAvailable"}},
		{name: "token endpoint down", token: &fakeMicrosoftFailure{http.StatusInternalServerError, "server_error"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := newFakeMicrosoft(t)
			ms.grantRefreshToken("rt-0")
			ms.graphFail, ms.tokenFail = tt.graph, tt.token
			_, err := ms.source("").Fetch(context.Background(), ExternalMailFetchRequest{
				Token: ExternalMailToken{RefreshToken: "rt-0"}, BackfillDays: 14, Now: outlookTestNow,
			})
			if err == nil {
				t.Fatal("Fetch succeeded, want an error")
			}
			if got := errors.Is(err, ErrExternalMailCursorExpired); got != tt.wantExpired {
				t.Fatalf("cursor expired = %v, want %v (%v)", got, tt.wantExpired, err)
			}
			if got := errors.Is(err, ErrExternalMailAuth); got != tt.wantAuth {
				t.Fatalf("auth = %v, want %v (%v)", got, tt.wantAuth, err)
			}
			if strings.Contains(err.Error(), "rt-") || strings.Contains(err.Error(), "at-") {
				t.Fatalf("error leaks a token: %v", err)
			}
		})
	}
}

// TestOutlookCursorRefusesForeignHost: a cursor whose link points anywhere but the configured
// Graph host never receives the access token; the sync starts over instead.
func TestOutlookCursorRefusesForeignHost(t *testing.T) {
	var hits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		writeFakeJSON(w, http.StatusOK, map[string]any{"value": []any{}, "@odata.deltaLink": "x"})
	}))
	t.Cleanup(evil.Close)
	ms := newFakeMicrosoft(t)
	ms.grantRefreshToken("rt-0")
	source := ms.source("")

	for _, link := range []string{
		evil.URL + "/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=1",
		strings.Replace(ms.server.URL, "http://", "https://", 1) + "/v1.0/me/mailFolders/inbox/messages/delta",
		ms.server.URL + "/beta/me/mailFolders/inbox/messages/delta",
		"http://user@" + strings.TrimPrefix(ms.server.URL, "http://") + "/v1.0/me/messages",
	} {
		cursor, _ := json.Marshal(outlookCursor{Version: outlookCursorVersion, Folders: []*outlookFolderState{{Folder: "inbox", Link: link}}})
		_, err := source.Fetch(context.Background(), ExternalMailFetchRequest{
			Token: ExternalMailToken{RefreshToken: "rt-0"}, Cursor: string(cursor), Now: outlookTestNow,
		})
		if !errors.Is(err, ErrExternalMailCursorExpired) {
			t.Fatalf("link %q: err = %v, want ErrExternalMailCursorExpired", link, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("the foreign host received %d requests", hits.Load())
	}
	if ms.deltaCalls != 0 {
		t.Fatalf("graph received %d delta calls for refused cursors", ms.deltaCalls)
	}
	// A cursor that does not decode is expired, not fatal.
	_, err := source.Fetch(context.Background(), ExternalMailFetchRequest{Token: ExternalMailToken{RefreshToken: "rt-0"}, Cursor: "17", Now: outlookTestNow})
	if !errors.Is(err, ErrExternalMailCursorExpired) {
		t.Fatalf("garbage cursor: err = %v", err)
	}
}

// TestOutlookCursorAddsFolders: a stored cursor without a newly configured folder keeps the old
// folder's link and starts a first round for the new one.
func TestOutlookCursorAddsFolders(t *testing.T) {
	ms := newFakeMicrosoft(t)
	source := ms.source("")
	source.folders = []string{"inbox", "archive"}
	link := ms.server.URL + "/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=4"
	stored, _ := json.Marshal(outlookCursor{Version: outlookCursorVersion, Folders: []*outlookFolderState{{Folder: "inbox", Link: link}}})
	cursor, err := source.parseCursor(string(stored))
	if err != nil {
		t.Fatalf("parseCursor: %v", err)
	}
	next := cursor.next()
	if next.Folder != "archive" || next.Link != "" {
		t.Fatalf("next folder = %+v, want archive's first round", next)
	}
	if cursor.Folders[0].Link != link || cursor.Folders[0].Pending {
		t.Fatalf("inbox state = %+v", cursor.Folders[0])
	}
}

func TestOutlookRevokeIsLocal(t *testing.T) {
	ms := newFakeMicrosoft(t)
	if err := ms.source("").Revoke(context.Background(), ExternalMailToken{RefreshToken: "rt-0"}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if ms.lastForm != nil || ms.deltaCalls != 0 {
		t.Fatal("Revoke reached Microsoft")
	}
}

func TestOutlookRegistration(t *testing.T) {
	client := config.ExternalMailOAuthClient{ClientID: "client-1", ClientSecret: "secret-1"}
	tests := []struct {
		name    string
		cfg     config.ExternalMailConfig
		enabled bool
	}{
		{"enabled", config.ExternalMailConfig{Providers: []string{"outlook"}, OAuth: map[string]config.ExternalMailOAuthClient{"outlook": client}}, true},
		{"not listed", config.ExternalMailConfig{Providers: []string{"gmail"}, OAuth: map[string]config.ExternalMailOAuthClient{"outlook": client}}, false},
		{"no client", config.ExternalMailConfig{Providers: []string{"outlook"}}, false},
		{"no secret", config.ExternalMailConfig{Providers: []string{"outlook"}, OAuth: map[string]config.ExternalMailOAuthClient{"outlook": {ClientID: "client-1"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector, enabled := buildOutlookConnector(tt.cfg)
			if enabled != tt.enabled {
				t.Fatalf("enabled = %v, want %v", enabled, tt.enabled)
			}
			registered, found := newExternalMailRegistry(tt.cfg).lookup(domain.ExternalMailProviderOutlook)
			if found != tt.enabled {
				t.Fatalf("registry has outlook = %v, want %v", found, tt.enabled)
			}
			if !tt.enabled {
				return
			}
			for _, c := range []ExternalMailConnector{connector, registered} {
				if c.ID != domain.ExternalMailProviderOutlook || c.AuthKind != ExternalMailAuthOAuth2 || !c.Capabilities.Read {
					t.Fatalf("connector = %+v", c)
				}
				if _, ok := c.Source.(*outlookMailSource); !ok {
					t.Fatalf("source is %T", c.Source)
				}
			}
		})
	}
}

// TestOutlookSyncStoresOnceAndRotatesToken runs the sync worker against the fake Microsoft: the
// backfill pages in, every copy is stored once with source outlook, and the sealed refresh token
// is replaced by the one Microsoft rotated.
func TestOutlookSyncStoresOnceAndRotatesToken(t *testing.T) {
	f := newExternalFixture(t, config.ExternalMailSourceFake)
	ms := newFakeMicrosoft(t)
	ms.grantRefreshToken(externalTestToken)
	now := f.service.now().UTC()
	for i := 1; i <= 5; i++ {
		ms.deliver(outlookTestMessage(fmt.Sprintf("o-%d", i), fmt.Sprintf("Outlook %d", i), now.Add(-time.Duration(i)*time.Hour)))
	}
	f.service.externalConnectors = &externalMailRegistry{connectors: []ExternalMailConnector{{
		ID: domain.ExternalMailProviderOutlook, AuthKind: ExternalMailAuthOAuth2,
		Capabilities: ExternalMailCapabilities{Read: true}, Source: ms.source(""),
	}}}
	if err := f.repo.(*repository).db.Exec(`UPDATE external_mail_connections SET provider = ? WHERE id = ?`,
		domain.ExternalMailProviderOutlook, f.connection.ID).Error; err != nil {
		t.Fatalf("set provider: %v", err)
	}

	// Two pages of three: the second pass runs at once because the first left More.
	for pass := 1; pass <= 2; pass++ {
		if handled := f.sync(t); handled != 1 {
			t.Fatalf("pass %d handled %d connections, want 1", pass, handled)
		}
	}
	connection := f.reload(t)
	if connection.Status != domain.ExternalMailStatusActive || connection.LastErrorCode != "" {
		t.Fatalf("status %q error %q", connection.Status, connection.LastErrorCode)
	}
	if connection.EncryptedRefreshToken == f.connection.EncryptedRefreshToken {
		t.Fatal("the sealed refresh token did not change after a rotating sync")
	}
	if opened, err := f.service.encryptor.DecryptString(connection.EncryptedRefreshToken); err != nil || opened != fmt.Sprintf("rt-%d", ms.issued) {
		t.Fatalf("stored token = %q (%v), want the last one Microsoft issued, rt-%d", opened, err, ms.issued)
	}
	stored := f.storedExternal(t)
	if len(stored) != 5 {
		t.Fatalf("stored %d messages, want 5", len(stored))
	}
	for _, message := range stored {
		if message.Source != domain.MailSourceOutlook || message.Direction != domain.MailDirectionInbound {
			t.Fatalf("message %d: source %q direction %q", message.ID, message.Source, message.Direction)
		}
	}

	// A new message arrives; an expired delta token restarts the round, which refetches the
	// backfill without storing a second copy.
	ms.deliver(outlookTestMessage("o-6", "Outlook 6", now))
	ms.failNextGraph(http.StatusGone, "SyncStateNotFound")
	f.advance(externalMailSyncInterval)
	for pass := 0; pass < 5 && f.reload(t).NextSyncAt.Compare(f.service.now().UTC()) <= 0; pass++ {
		f.sync(t)
	}
	if connection = f.reload(t); connection.Status != domain.ExternalMailStatusActive || connection.LastErrorCode != "" {
		t.Fatalf("after restart: status %q error %q", connection.Status, connection.LastErrorCode)
	}
	if stored = f.storedExternal(t); len(stored) != 6 {
		t.Fatalf("after restart the mailbox holds %d messages, want 6", len(stored))
	}
	seen := map[string]bool{}
	for _, message := range stored {
		if seen[message.Subject] {
			t.Fatalf("message %q stored twice", message.Subject)
		}
		seen[message.Subject] = true
	}
}
