package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
	"github.com/fastagi/zmail/api/pkg/response"
)

// publicMailboxFakeRepo wraps fakeRepository with tombstone tracking and publicMailboxStore so
// unit tests can exercise global per-user claim caps and soft-delete address reservation without
// modifying shared fixtures.
type publicMailboxFakeRepo struct {
	*fakeRepository
	deleted []*domain.Mailbox
}

var (
	_ publicMailboxStore              = (*publicMailboxFakeRepo)(nil)
	_ domain.MailboxDirectoryRepository = (*publicMailboxFakeRepo)(nil)
)

func newPublicMailboxFakeRepo() *publicMailboxFakeRepo {
	return &publicMailboxFakeRepo{fakeRepository: newFakeRepository()}
}

func (r *publicMailboxFakeRepo) DeleteMailbox(ctx context.Context, organizationID, id uint) error {
	mb, err := r.FindMailbox(ctx, organizationID, id)
	if err != nil {
		return err
	}
	if err := r.fakeRepository.DeleteMailbox(ctx, organizationID, id); err != nil {
		return err
	}
	r.deleted = append(r.deleted, mb)
	return nil
}

func (r *publicMailboxFakeRepo) MailboxAddressUsed(_ context.Context, address string) (bool, error) {
	address = strings.ToLower(strings.TrimSpace(address))
	for _, mb := range r.mailboxes {
		if mb.Address == address {
			return true, nil
		}
	}
	for _, mb := range r.deleted {
		if mb.Address == address {
			return true, nil
		}
	}
	return false, nil
}

func (r *publicMailboxFakeRepo) ListUserPublicMailboxes(_ context.Context, userID uint, domains []string) ([]publicMailboxClaim, error) {
	allowed := map[string]struct{}{}
	for _, d := range domains {
		allowed[normalizeSettingDomain(d)] = struct{}{}
	}
	var claims []publicMailboxClaim
	appendIfMatch := func(mb *domain.Mailbox, deleted bool) {
		if mb.Origin != "" && mb.Origin != domain.MailboxOriginZmail {
			return
		}
		if mb.OwnerUserID == nil || *mb.OwnerUserID != userID {
			return
		}
		if _, ok := allowed[addressDomain(strings.ToLower(mb.Address))]; !ok {
			return
		}
		claims = append(claims, publicMailboxClaim{
			ID:             mb.ID,
			OrganizationID: mb.OrganizationID,
			Address:        strings.ToLower(mb.Address),
			Deleted:        deleted,
		})
	}
	for _, mb := range r.mailboxes {
		appendIfMatch(mb, false)
	}
	for _, mb := range r.deleted {
		appendIfMatch(mb, true)
	}
	slices.SortFunc(claims, func(a, b publicMailboxClaim) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return claims, nil
}

func (r *publicMailboxFakeRepo) LockPublicMailboxClaim(context.Context, uint) error {
	return nil
}

type publicMailboxTestFixture struct {
	router    *gin.Engine
	service   *service
	repo      *publicMailboxFakeRepo
	domains   *fakeDomains
	transport *fakeTransport
	now       time.Time
	userID    uint
	org       *domain.OrganizationContext
}

func newPublicMailboxTestFixture(t *testing.T, settings Settings) *publicMailboxTestFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := newPublicMailboxFakeRepo()
	domains := &fakeDomains{domains: map[uint]*domain.MailDomain{}, mailbox: repo.fakeRepository}
	transport := &fakeTransport{}
	contacts := newFakeContacts()
	svc := NewService(
		repo,
		&fakeMemberships{},
		repo,
		contacts,
		&stubBroadcasts{},
		nil,
		domains,
		nil,
		nil,
		transport,
		&fakeNotifier{},
		settings,
		nil,
		ratelimit.NewMemoryStores(),
		nil,
		nil,
	)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	f := &publicMailboxTestFixture{
		service:   svc,
		repo:      repo,
		domains:   domains,
		transport: transport,
		now:       now,
		userID:    700,
	}
	svc.now = func() time.Time { return f.now }

	h := NewHandler(svc)
	response.DefaultErrorMapper.Register(domain.ErrInvalidInput, http.StatusBadRequest, domain.CodeInvalidInput)
	response.DefaultErrorMapper.Register(domain.ErrPermissionDenied, http.StatusForbidden, domain.CodePermissionDenied)
	response.DefaultErrorMapper.Register(domain.ErrOrganizationContextRequired, http.StatusBadRequest, domain.CodeOrganizationContextRequired)
	h.RegisterErrorMappings(response.DefaultErrorMapper)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		if f.userID != 0 {
			c.Set("userID", f.userID)
		}
		if f.org != nil {
			c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), *f.org))
		}
		c.Next()
	})
	router.GET("/public-mailbox/options", h.GetPublicMailboxOptions)
	router.GET("/public-mailbox/availability", h.CheckPublicMailboxAvailability)
	router.POST("/public-mailbox/claim", h.ClaimPublicMailbox)
	f.router = router
	return f
}

func (f *publicMailboxTestFixture) do(method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	decoded := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec, decoded
}

func TestIsSharedDomainAndMultiPublicDomainWiring(t *testing.T) {
	f := newPublicMailboxTestFixture(t, Settings{
		Domain:               "zmail.im",
		PublicDomains:        []string{"zmail.im", "zmail.ai"},
		CustomDomainsEnabled: true,
		DailySendLimit:       200,
	})
	if !f.service.isSharedDomain("zmail.im") || !f.service.isSharedDomain("ZMAIL.AI.") {
		t.Fatalf("expected zmail.im and zmail.ai to be shared domains")
	}
	if f.service.isSharedDomain("acme.example") {
		t.Fatalf("custom domain must not be shared")
	}

	// Verified custom domain + promoted shared domain in repo: MailboxDomains keeps shared domains
	// marked shared and never duplicates them as organization domains.
	_ = f.domains.CreateDomain(t.Context(), &domain.MailDomain{OrganizationID: 7, Name: "acme.example", Status: domain.MailDomainStatusVerified}, 10)
	_ = f.domains.CreateDomain(t.Context(), &domain.MailDomain{OrganizationID: 7, Name: "zmail.ai", Status: domain.MailDomainStatusVerified}, 10)
	listed, err := f.service.MailboxDomains(t.Context(), 7)
	if err != nil {
		t.Fatalf("MailboxDomains: %v", err)
	}
	if len(listed) != 3 || !listed[0].Shared || listed[0].Name != "zmail.im" || !listed[1].Shared || listed[1].Name != "zmail.ai" || listed[2].Shared || listed[2].Name != "acme.example" {
		t.Fatalf("MailboxDomains = %+v", listed)
	}

	// checkReservedLocalPart guards secondary public domains too.
	if err := checkReservedLocalPart("admin", "zmail.ai", f.service.sharedDomains()...); !errors.Is(err, ErrMailboxAddressReserved) {
		t.Fatalf("reserved on secondary public domain = %v; want ErrMailboxAddressReserved", err)
	}
	if err := checkReservedLocalPart("admin", "acme.example", f.service.sharedDomains()...); err != nil {
		t.Fatalf("role address on custom domain = %v; want nil", err)
	}

	// transactional_direct blocks API-key sends on secondary public domains.
	ctx := domain.WithAPIKeyRestriction(t.Context(), domain.APIKeyRestriction{OrganizationID: 7})
	actor := TransactionalActor{OrganizationID: 7, UserID: 700, Mode: domain.APIKeyModeLive, Viewer: viewerOf(7, 700, domain.OrganizationRoleOwner)}
	if err := f.service.authorizeDirectSender(ctx, actor, "news@zmail.ai"); !errors.Is(err, domain.ErrAPIKeyDomainNotAllowed) {
		t.Fatalf("authorizeDirectSender on secondary public domain = %v; want ErrAPIKeyDomainNotAllowed", err)
	}

	// webhookNotifier marks secondary public domain as unverified sender origin.
	cfg := &config.Config{
		Starters: config.StarterConfig{Optional: []string{config.StarterWebhook}},
		Mailbox:  config.MailboxConfig{Domain: "zmail.im", PublicDomains: []string{"zmail.im", "zmail.ai"}},
	}
	notifier := NewWebhookNotifier(cfg, nil)
	if origin := notifier.transactionalOrigin("hi@zmail.ai"); origin.SenderDomainVerified {
		t.Fatalf("webhook origin on secondary public domain = %+v; want SenderDomainVerified=false", origin)
	}
	if origin := notifier.transactionalOrigin("hi@acme.example"); !origin.SenderDomainVerified {
		t.Fatalf("webhook origin on custom domain = %+v; want SenderDomainVerified=true", origin)
	}
}

func TestPublicMailboxOptionsAndAvailabilityHTTP(t *testing.T) {
	f := newPublicMailboxTestFixture(t, Settings{
		Domain:                  "zmail.im",
		PublicDomains:           []string{"zmail.im", "zmail.ai"},
		PublicLimitPerUser:      1,
		PublicMinLocalLen:       4,
		PublicDailySendLimitNew: 10,
		PublicDailySendLimit:    30,
		PublicMaxRecipientsNew:  3,
		PublicMaxRecipients:     5,
	})
	// No organization_context is set on f.org: auth-only routes must still work!
	rec, body := f.do(http.MethodGet, "/public-mailbox/options", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /public-mailbox/options = %d %v", rec.Code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["enabled"] != true || data["min_local_length"] != float64(4) || data["limit_per_user"] != float64(1) ||
		data["claimed_count"] != float64(0) || data["claimed_address"] != "" ||
		data["daily_send_limit_new"] != float64(10) || data["daily_send_limit"] != float64(30) ||
		data["max_recipients_new"] != float64(3) || data["max_recipients"] != float64(5) {
		t.Fatalf("unexpected options payload: %v", data)
	}

	// Pre-seed a taken address and one of its default suggestions to prove suggestions skip taken ones.
	ownerOther := uint(999)
	f.repo.mailboxes[1] = &domain.Mailbox{ID: 1, OrganizationID: 1, Address: "alex@zmail.im", Type: domain.MailboxTypePersonal, OwnerUserID: &ownerOther}
	f.repo.mailboxes[2] = &domain.Mailbox{ID: 2, OrganizationID: 1, Address: "alex88@zmail.im", Type: domain.MailboxTypePersonal, OwnerUserID: &ownerOther}
	// Soft-deleted tombstone must still count as taken.
	f.repo.deleted = append(f.repo.deleted, &domain.Mailbox{ID: 3, OrganizationID: 2, Address: "ghost@zmail.im", Type: domain.MailboxTypePersonal, OwnerUserID: &ownerOther})

	check := func(query, wantStatus string) map[string]any {
		t.Helper()
		r, b := f.do(http.MethodGet, "/public-mailbox/availability?"+query, "")
		if r.Code != http.StatusOK {
			t.Fatalf("availability(%s) = %d %v", query, r.Code, b)
		}
		d, _ := b["data"].(map[string]any)
		if d["status"] != wantStatus {
			t.Fatalf("availability(%s) status = %v; want %s (%v)", query, d["status"], wantStatus, d)
		}
		return d
	}

	avail := check("local_part=Taylor", PublicMailboxStatusAvailable)
	if avail["address"] != "taylor@zmail.im" || avail["domain"] != "zmail.im" {
		t.Fatalf("unexpected available payload: %v", avail)
	}
	availAlt := check("local_part=taylor&domain=zmail.ai", PublicMailboxStatusAvailable)
	if availAlt["address"] != "taylor@zmail.ai" {
		t.Fatalf("unexpected secondary domain payload: %v", availAlt)
	}

	taken := check("local_part=alex", PublicMailboxStatusTaken)
	rawSuggestions, _ := taken["suggestions"].([]any)
	if len(rawSuggestions) != 3 {
		t.Fatalf("taken suggestions = %v; want 3 free suggestions", rawSuggestions)
	}
	for _, s := range rawSuggestions {
		if s == "alex88@zmail.im" {
			t.Fatalf("suggestions included already-taken address alex88@zmail.im: %v", rawSuggestions)
		}
	}

	check("local_part=ghost", PublicMailboxStatusTaken)
	check("local_part=zmail", PublicMailboxStatusReserved)
	check("local_part=official", PublicMailboxStatusReserved)
	check("local_part=billing", PublicMailboxStatusReserved)
	check("local_part=abc", PublicMailboxStatusTooShort)
	check("local_part=bad..name", PublicMailboxStatusInvalid)

	// Unknown domain rejected with 404.
	badDomain, _ := f.do(http.MethodGet, "/public-mailbox/availability?local_part=taylor&domain=gmail.com", "")
	if badDomain.Code != http.StatusNotFound {
		t.Fatalf("unknown domain status = %d; want 404", badDomain.Code)
	}

	// Rate limit 30/min per user: burn remaining budget and expect 429.
	f.userID = 701
	for i := 0; i < publicMailboxAvailabilityMax; i++ {
		r, _ := f.do(http.MethodGet, "/public-mailbox/availability?local_part=taylor", "")
		if r.Code != http.StatusOK {
			t.Fatalf("request %d = %d; want 200", i+1, r.Code)
		}
	}
	limited, body := f.do(http.MethodGet, "/public-mailbox/availability?local_part=taylor", "")
	if limited.Code != http.StatusTooManyRequests || body["error_code"] != domain.CodeRateLimited {
		t.Fatalf("31st availability check = %d %v; want 429 %s", limited.Code, body, domain.CodeRateLimited)
	}
}

func TestPublicMailboxClaimGlobalCapTombstoneAndWelcomeEmail(t *testing.T) {
	f := newPublicMailboxTestFixture(t, Settings{
		Domain:                  "zmail.im",
		PublicDomains:           []string{"zmail.im", "zmail.ai"},
		PublicLimitPerUser:      1,
		PublicMinLocalLen:       4,
		PublicDailySendLimitNew: 10,
		PublicDailySendLimit:    30,
		PublicMaxRecipientsNew:  3,
		PublicMaxRecipients:     5,
	})

	// Member cannot claim (must manage organization).
	memberViewer := viewerOf(7, f.userID, domain.OrganizationRoleMember)
	f.org = &memberViewer
	rec, body := f.do(http.MethodPost, "/public-mailbox/claim", `{"local_part":"stark"}`)
	if rec.Code != http.StatusForbidden || body["error_code"] != domain.CodePermissionDenied {
		t.Fatalf("member claim = %d %v; want 403", rec.Code, body)
	}

	ownerOrg7 := viewerOf(7, f.userID, domain.OrganizationRoleOwner)
	f.org = &ownerOrg7

	// Short local part rejected.
	shortRec, body := f.do(http.MethodPost, "/public-mailbox/claim", `{"local_part":"abc"}`)
	if shortRec.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailboxAddressInvalid {
		t.Fatalf("short claim = %d %v; want 422", shortRec.Code, body)
	}

	// Expanded reserved handle rejected.
	reservedRec, body := f.do(http.MethodPost, "/public-mailbox/claim", `{"local_part":"official"}`)
	if reservedRec.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailboxAddressReserved {
		t.Fatalf("reserved claim = %d %v; want 422", reservedRec.Code, body)
	}

	// Valid claim succeeds with 201 Created and inserts local Welcome email without SMTP.
	createdRec, body := f.do(http.MethodPost, "/public-mailbox/claim", `{"local_part":"stark","display_name":"Tony Stark"}`)
	if createdRec.Code != http.StatusCreated {
		t.Fatalf("valid claim = %d %v; want 201", createdRec.Code, body)
	}
	data, _ := body["data"].(map[string]any)
	if data["address"] != "stark@zmail.im" || data["type"] != "personal" || data["unread_count"] != float64(1) {
		t.Fatalf("unexpected claimed mailbox response: %v", data)
	}
	if len(f.transport.sent) != 0 {
		t.Fatalf("welcome email must be local-only; transport sent %d messages", len(f.transport.sent))
	}
	if len(f.repo.messages) != 1 {
		t.Fatalf("stored messages = %d; want 1 local welcome email", len(f.repo.messages))
	}
	for _, msg := range f.repo.messages {
		if msg.Direction != domain.MailDirectionInbound || msg.FromAddress != "notify@zmail.im" || msg.FromName != "Zmail" ||
			!strings.Contains(msg.Subject, "stark@zmail.im") {
			t.Fatalf("unexpected welcome message: %+v", msg)
		}
	}

	// Options endpoint reports the live claim across organizations (even without org context).
	f.org = nil
	optRec, optBody := f.do(http.MethodGet, "/public-mailbox/options", "")
	if optRec.Code != http.StatusOK {
		t.Fatalf("options after claim = %d %v", optRec.Code, optBody)
	}
	optData, _ := optBody["data"].(map[string]any)
	if optData["claimed_count"] != float64(1) || optData["claimed_address"] != "stark@zmail.im" ||
		optData["claimed_organization_id"] != float64(7) || optData["claimed_mailbox_id"] == nil {
		t.Fatalf("options after claim = %v", optData)
	}

	// Global cap: same user cannot claim a second public mailbox even in a different organization.
	ownerOrg8 := viewerOf(8, f.userID, domain.OrganizationRoleOwner)
	f.org = &ownerOrg8
	secondRec, secondBody := f.do(http.MethodPost, "/public-mailbox/claim", `{"local_part":"stark2","domain":"zmail.ai"}`)
	if secondRec.Code != http.StatusConflict || secondBody["error_code"] != domain.CodePublicMailboxLimitReached {
		t.Fatalf("second claim across orgs = %d %v; want 409 %s", secondRec.Code, secondBody, domain.CodePublicMailboxLimitReached)
	}

	// Deleting the mailbox leaves a tombstone: user STILL cannot reclaim a new address, AND
	// another user cannot recycle stark@zmail.im!
	var claimedID uint
	for id := range f.repo.mailboxes {
		claimedID = id
	}
	if err := f.service.DeleteMailbox(t.Context(), ownerOrg7, claimedID); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	f.org = &ownerOrg7
	reclaimRec, reclaimBody := f.do(http.MethodPost, "/public-mailbox/claim", `{"local_part":"stark3"}`)
	if reclaimRec.Code != http.StatusConflict || reclaimBody["error_code"] != domain.CodePublicMailboxLimitReached {
		t.Fatalf("reclaim after delete = %d %v; want 409 %s", reclaimRec.Code, reclaimBody, domain.CodePublicMailboxLimitReached)
	}

	// Stranger cannot take over deleted stark@zmail.im.
	f.userID = 888
	strangerOrg := viewerOf(9, f.userID, domain.OrganizationRoleOwner)
	f.org = &strangerOrg
	hijackRec, hijackBody := f.do(http.MethodPost, "/public-mailbox/claim", `{"local_part":"stark"}`)
	if hijackRec.Code != http.StatusConflict || hijackBody["error_code"] != domain.CodeMailboxAddressTaken {
		t.Fatalf("recycle deleted address = %d %v; want 409 %s", hijackRec.Code, hijackBody, domain.CodeMailboxAddressTaken)
	}

	// Claim rate limit: 5/hour per user (stranger already used 1 above -> 4 more allowed, 6th 429).
	for i := 0; i < publicMailboxClaimMax-1; i++ {
		r, _ := f.do(http.MethodPost, "/public-mailbox/claim", `{"local_part":"abc"}`)
		if r.Code != http.StatusUnprocessableEntity {
			t.Fatalf("claim attempt %d = %d; want 422", i+2, r.Code)
		}
	}
	rateRec, rateBody := f.do(http.MethodPost, "/public-mailbox/claim", `{"local_part":"freshname"}`)
	if rateRec.Code != http.StatusTooManyRequests || rateBody["error_code"] != domain.CodeRateLimited {
		t.Fatalf("6th claim in hour = %d %v; want 429 %s", rateRec.Code, rateBody, domain.CodeRateLimited)
	}
}

func TestSharedDomainOutboundAntiAbuseGuardrails(t *testing.T) {
	f := newPublicMailboxTestFixture(t, Settings{
		Domain:                  "zmail.im",
		PublicDomains:           []string{"zmail.im", "zmail.ai"},
		CustomDomainsEnabled:    true,
		DailySendLimit:          200,
		PublicDailySendLimitNew: 10,
		PublicDailySendLimit:    30,
		PublicMaxRecipientsNew:  3,
		PublicMaxRecipients:     5,
	})
	viewer := viewerOf(7, 700, domain.OrganizationRoleOwner)
	ownerID := viewer.UserID

	// Seed a new shared-domain mailbox (< 7d old) and a custom-domain mailbox.
	sharedBox := &domain.Mailbox{
		ID: 10, OrganizationID: 7, Address: "alice@zmail.im", Type: domain.MailboxTypePersonal,
		OwnerUserID: &ownerID, CreatedAt: f.now.Add(-2 * time.Hour),
	}
	customBox := &domain.Mailbox{
		ID: 11, OrganizationID: 7, Address: "team@acme.example", Type: domain.MailboxTypePersonal,
		OwnerUserID: &ownerID, CreatedAt: f.now.Add(-2 * time.Hour),
	}
	f.repo.mailboxes[sharedBox.ID] = sharedBox
	f.repo.mailboxes[customBox.ID] = customBox

	// 1. Recipient cap: new shared mailbox rejects 4 recipients, allows 3.
	fourRecipients := SendInput{
		To: []string{"a@ex.com", "b@ex.com", "c@ex.com"}, Cc: []string{"d@ex.com"},
		Subject: "Hi", Text: "Hello",
	}
	if _, _, err := f.service.SendMessage(t.Context(), viewer, sharedBox.ID, fourRecipients); !errors.Is(err, ErrMailMessageInvalid) {
		t.Fatalf("new shared mailbox 4 recipients = %v; want ErrMailMessageInvalid", err)
	}
	threeRecipients := SendInput{
		To: []string{"a@ex.com", "b@ex.com"}, Bcc: []string{"c@ex.com"},
		Subject: "Hi", Text: "Hello",
	}
	if _, _, err := f.service.SendMessage(t.Context(), viewer, sharedBox.ID, threeRecipients); err != nil {
		t.Fatalf("new shared mailbox 3 recipients = %v; want nil", err)
	}

	// 2. Burst rate limit: 2 sends per minute on shared mailbox, 3rd returns ErrMailSendLimitReached.
	oneRecipient := SendInput{To: []string{"a@ex.com"}, Subject: "Hi", Text: "Hello"}
	if _, _, err := f.service.SendMessage(t.Context(), viewer, sharedBox.ID, oneRecipient); err != nil {
		t.Fatalf("2nd send in minute = %v; want nil", err)
	}
	if _, _, err := f.service.SendMessage(t.Context(), viewer, sharedBox.ID, oneRecipient); !errors.Is(err, ErrMailSendLimitReached) {
		t.Fatalf("3rd send in minute = %v; want ErrMailSendLimitReached", err)
	}

	// 3. Daily send cap: reset burst limiter, seed 10 sends for new mailbox -> 11th rejected with ErrMailSendLimitReached (not queued).
	f.service.publicLimits = newPublicMailboxLimits(ratelimit.NewMemoryStores())
	for i := 0; i < 8; i++ { // 2 already sent above -> total 10
		at := f.now.Add(-time.Minute)
		f.repo.messages[uint(100+i)] = &domain.MailMessage{
			ID: uint(100 + i), MailboxID: sharedBox.ID, Direction: domain.MailDirectionOutbound,
			SendLane: domain.SendLanePersonal, SendAttemptedAt: &at,
		}
	}
	if _, _, err := f.service.SendMessage(t.Context(), viewer, sharedBox.ID, oneRecipient); !errors.Is(err, ErrMailSendLimitReached) {
		t.Fatalf("11th daily send on new shared mailbox = %v; want ErrMailSendLimitReached", err)
	}

	// Mature shared mailbox (>= 7d old) gets limit 30/day and 5 recipients (rejects 6).
	sharedBox.CreatedAt = f.now.Add(-8 * 24 * time.Hour)
	f.service.publicLimits = newPublicMailboxLimits(ratelimit.NewMemoryStores())
	sixRecipients := SendInput{
		To: []string{"a@ex.com", "b@ex.com", "c@ex.com", "d@ex.com", "e@ex.com", "f@ex.com"},
		Subject: "Hi", Text: "Hello",
	}
	if _, _, err := f.service.SendMessage(t.Context(), viewer, sharedBox.ID, sixRecipients); !errors.Is(err, ErrMailMessageInvalid) {
		t.Fatalf("mature shared mailbox 6 recipients = %v; want ErrMailMessageInvalid", err)
	}
	fiveRecipients := SendInput{
		To: []string{"a@ex.com", "b@ex.com", "c@ex.com", "d@ex.com", "e@ex.com"},
		Subject: "Hi", Text: "Hello",
	}
	if _, _, err := f.service.SendMessage(t.Context(), viewer, sharedBox.ID, fiveRecipients); err != nil {
		t.Fatalf("mature shared mailbox 5 recipients = %v; want nil", err)
	}

	// 4. CreateBroadcast rejected on shared domain.
	if _, err := f.service.CreateBroadcast(t.Context(), viewer, BroadcastInput{
		Name: "Promo", MailboxID: sharedBox.ID, TemplateID: 1, ListID: 1,
	}); !errors.Is(err, ErrMailDomainNotVerified) {
		t.Fatalf("CreateBroadcast on shared domain = %v; want ErrMailDomainNotVerified", err)
	}

	// 5. API-key SendFromAddress rejected on shared domain, allowed on custom domain.
	if _, _, err := f.service.SendFromAddress(t.Context(), viewer, sharedBox.Address, oneRecipient); !errors.Is(err, ErrMailDomainNotVerified) {
		t.Fatalf("SendFromAddress on shared domain = %v; want ErrMailDomainNotVerified", err)
	}
	if _, _, err := f.service.SendFromAddress(t.Context(), viewer, customBox.Address, sixRecipients); err != nil {
		t.Fatalf("SendFromAddress on custom domain = %v; want nil", err)
	}
}
