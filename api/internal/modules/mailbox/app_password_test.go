package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
	"github.com/fastagi/zmail/api/pkg/response"
)

// testAppPasswordPepper is a fixed pepper for tests only; production has no default.
const testAppPasswordPepper = "test-only-mail-app-password-pepper-0123456789"

// fakeReverifier accepts the password "right" and, with two-factor sign-in on, the code "123456".
type fakeReverifier struct {
	twoFactor bool
	calls     int
}

func (f *fakeReverifier) ReverifyCredentials(_ context.Context, _ uint, password, secondFactor string) error {
	f.calls++
	if password != "right" {
		return domain.ErrInvalidCredentials
	}
	if !f.twoFactor {
		return nil
	}
	if secondFactor == "" {
		return domain.ErrTOTPRequired
	}
	if secondFactor != "123456" {
		return domain.ErrTOTPInvalid
	}
	return nil
}

type appPasswordFixture struct {
	service                     *service
	db                          *gorm.DB
	reverifier                  *fakeReverifier
	owner, member, peer, abroad domain.OrganizationContext
}

// newAppPasswordFixture migrates a disposable schema with the real migrations: organization 10
// with owner 901, members 902 and 903; organization 11 where 902 is also a member.
func newAppPasswordFixture(t *testing.T) *appPasswordFixture {
	t.Helper()
	db := openMigratedPostgres(t)
	for _, statement := range []string{
		`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES
			(901, 'owner', 'owner@example.com', 'x', now(), now()),
			(902, 'alice', 'alice@example.com', 'x', now(), now()),
			(903, 'bob', 'bob@example.com', 'x', now(), now())`,
		`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES
			(10, 'Acme', 'acme', 901, now(), now()), (11, 'Other', 'other', 901, now(), now())`,
		`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at) VALUES
			(100, 10, 901, 'owner', now(), now()), (101, 10, 902, 'member', now(), now()),
			(102, 10, 903, 'member', now(), now()), (110, 11, 902, 'member', now(), now())`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	repo := NewRepository(db)
	settings := accessSettings()
	settings.AppPasswordPepper = testAppPasswordPepper
	settings.ClientIMAPHost, settings.ClientIMAPPort = "imap.example.test", 993
	settings.ClientSMTPHost, settings.ClientSMTPPort, settings.ClientSubmissionPort = "smtp.example.test", 465, 587
	svc := NewService(repo, newAccessMemberships(), repo, repo, repo, repo, nil, nil, nil, &fakeTransport{},
		&fakeNotifier{}, settings, nil, ratelimit.NewMemoryStores(), nil, nil)
	reverifier := &fakeReverifier{}
	svc.reverifier = reverifier
	context := func(organizationID, membershipID, userID uint, role domain.OrganizationRole) domain.OrganizationContext {
		return domain.OrganizationContext{
			OrganizationID: organizationID, OrganizationName: "Org", OrganizationSlug: "org",
			MembershipID: membershipID, UserID: userID, Role: role, Features: domain.DefaultMemberFeatures(),
		}
	}
	return &appPasswordFixture{
		service: svc, db: db, reverifier: reverifier,
		owner:  context(10, 100, 901, domain.OrganizationRoleOwner),
		member: context(10, 101, 902, domain.OrganizationRoleMember),
		peer:   context(10, 102, 903, domain.OrganizationRoleMember),
		abroad: context(11, 110, 902, domain.OrganizationRoleMember),
	}
}

func (f *appPasswordFixture) create(t *testing.T, viewer domain.OrganizationContext, name string) (*domain.MailAppPassword, string) {
	t.Helper()
	created, secret, err := f.service.CreateAppPassword(context.Background(), viewer, CreateAppPasswordInput{
		Name: name, Scopes: []domain.MailClientProtocol{domain.MailClientProtocolIMAP}, CurrentPassword: "right",
	})
	if err != nil {
		t.Fatalf("CreateAppPassword(%q): %v", name, err)
	}
	return created, secret
}

func fieldErrorOf(err error, field string) bool {
	var validation *ValidationError
	return errors.As(err, &validation) && len(validation.Fields[field]) > 0
}

var appPasswordSecretPattern = regexp.MustCompile(`^[a-z]{4}( [a-z]{4}){3}$`)

func TestCreateAppPasswordReturnsSecretOnce(t *testing.T) {
	f := newAppPasswordFixture(t)
	ctx := context.Background()
	created, secret := f.create(t, f.member, "  Alice's iPhone  ")
	if !appPasswordSecretPattern.MatchString(secret) {
		t.Fatalf("secret = %q", secret)
	}
	normalized := normalizeAppPassword(secret)
	if created.Name != "Alice's iPhone" || created.Hint != normalized[12:] || !created.ScopeIMAP || created.ScopeSMTP {
		t.Fatalf("created = %+v", created)
	}
	var row struct {
		SecretHash string
		Hint       string
		Name       string
	}
	if err := f.db.Raw(`SELECT secret_hash, hint, name FROM mail_app_passwords WHERE id = ?`, created.ID).Scan(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.SecretHash != hashAppPassword([]byte(testAppPasswordPepper), normalized) || strings.Contains(row.SecretHash, normalized) {
		t.Fatalf("stored hash = %q", row.SecretHash)
	}

	list, err := f.service.ListAppPasswords(ctx, f.member)
	if err != nil || len(list.Items) != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if !list.Access.Enabled || list.Access.Reason != "" || list.Server.IMAPHost != "imap.example.test" ||
		list.Server.SMTPHost != "smtp.example.test" || list.Server.SMTPPort != 465 || list.Server.SMTPSecurity != "ssl_tls" || list.Server.SMTPStartTLSPort != 587 || list.Limit != MaxAppPasswordsPerMember {
		t.Fatalf("list access and server = %+v %+v", list.Access, list.Server)
	}
	encoded, _ := json.Marshal(toMailAppPasswordListResponse(list))
	for _, leak := range []string{`"secret"`, normalized, secret, row.SecretHash} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("the listing carries %q: %s", leak, encoded)
		}
	}
	created2, _ := encodeCreated(t, created, secret)
	if !strings.Contains(created2, `"secret":"`+secret+`"`) {
		t.Fatalf("the creation response must carry the secret once: %s", created2)
	}
	if again, err := f.service.ListAppPasswords(ctx, f.peer); err != nil || len(again.Items) != 0 {
		t.Fatalf("another member sees %+v, %v", again, err)
	}
}

func encodeCreated(t *testing.T, created *domain.MailAppPassword, secret string) (string, error) {
	t.Helper()
	encoded, err := json.Marshal(toMailAppPasswordCreatedResponse(created, secret))
	return string(encoded), err
}

func TestCreateAppPasswordRequiresCurrentPasswordAndTOTP(t *testing.T) {
	f := newAppPasswordFixture(t)
	ctx := context.Background()
	input := CreateAppPasswordInput{Name: "Phone", Scopes: []domain.MailClientProtocol{domain.MailClientProtocolIMAP}, CurrentPassword: "wrong"}
	if _, _, err := f.service.CreateAppPassword(ctx, f.member, input); !fieldErrorOf(err, "current_password") {
		t.Fatalf("wrong password = %v; want a current_password field error", err)
	}
	f.reverifier.twoFactor = true
	input.CurrentPassword = "right"
	if _, _, err := f.service.CreateAppPassword(ctx, f.member, input); !fieldErrorOf(err, "totp_code") {
		t.Fatalf("missing code = %v; want a totp_code field error", err)
	}
	input.TOTPCode = "000000"
	for attempt := 3; attempt <= 5; attempt++ {
		if _, _, err := f.service.CreateAppPassword(ctx, f.member, input); !fieldErrorOf(err, "totp_code") {
			t.Fatalf("attempt %d = %v; want a totp_code field error", attempt, err)
		}
	}
	input.TOTPCode = "123456"
	_, _, err := f.service.CreateAppPassword(ctx, f.member, input)
	var retry *RetryAfterError
	if !errors.As(err, &retry) || !errors.Is(err, ErrMailAppPasswordAttemptsLimited) || retry.RetryAfter <= 0 {
		t.Fatalf("sixth attempt = %v; want a rate limit with Retry-After", err)
	}
	if _, _, err := f.service.CreateAppPassword(ctx, f.peer, input); err != nil {
		t.Fatalf("another member keeps their own budget: %v", err)
	}
	if _, _, err := f.service.CreateAppPassword(ctx, f.member, CreateAppPasswordInput{Scopes: nil, CurrentPassword: "right"}); !fieldErrorOf(err, "name") || !fieldErrorOf(err, "scopes") {
		t.Fatalf("empty name and scopes = %v", err)
	}
}

// TestCreateAppPasswordAcceptsSMTP stores the smtp scope alone or with imap.
func TestCreateAppPasswordAcceptsSMTP(t *testing.T) {
	f := newAppPasswordFixture(t)
	for _, scopes := range [][]domain.MailClientProtocol{
		{domain.MailClientProtocolIMAP, domain.MailClientProtocolSMTP},
		{domain.MailClientProtocolSMTP},
	} {
		created, _, err := f.service.CreateAppPassword(context.Background(), f.member, CreateAppPasswordInput{
			Name: "Phone", Scopes: scopes, CurrentPassword: "right",
		})
		if err != nil {
			t.Fatalf("scopes %v = %v", scopes, err)
		}
		if !created.ScopeSMTP || created.ScopeIMAP != (len(scopes) == 2) {
			t.Fatalf("scopes %v stored imap=%t smtp=%t", scopes, created.ScopeIMAP, created.ScopeSMTP)
		}
	}
	_, _, err := f.service.CreateAppPassword(context.Background(), f.member, CreateAppPasswordInput{
		Name: "Phone", Scopes: []domain.MailClientProtocol{domain.MailClientProtocolSMTP, domain.MailClientProtocolSMTP}, CurrentPassword: "right",
	})
	if !fieldErrorOf(err, "scopes") {
		t.Fatalf("a repeated smtp scope = %v", err)
	}
}

func TestCreateAppPasswordLimit(t *testing.T) {
	f := newAppPasswordFixture(t)
	ctx := context.Background()
	var first *domain.MailAppPassword
	for i := range MaxAppPasswordsPerMember {
		created, _ := f.create(t, f.member, "Device")
		if i == 0 {
			first = created
		}
	}
	input := CreateAppPasswordInput{Name: "One more", Scopes: []domain.MailClientProtocol{domain.MailClientProtocolIMAP}, CurrentPassword: "right"}
	if _, _, err := f.service.CreateAppPassword(ctx, f.member, input); !errors.Is(err, ErrMailAppPasswordLimitReached) {
		t.Fatalf("21st password = %v; want ErrMailAppPasswordLimitReached", err)
	}
	if _, _, err := f.service.CreateAppPassword(ctx, f.abroad, input); err != nil {
		t.Fatalf("the limit is per organization: %v", err)
	}
	if err := f.service.RevokeAppPassword(ctx, f.member, first.ID); err != nil {
		t.Fatalf("RevokeAppPassword: %v", err)
	}
	if _, _, err := f.service.CreateAppPassword(ctx, f.member, input); err != nil {
		t.Fatalf("after a revocation: %v", err)
	}
}

func TestCreateAppPasswordFeatureAndSwitch(t *testing.T) {
	f := newAppPasswordFixture(t)
	ctx := context.Background()
	withheld := f.member
	withheld.Features.MailClients = false
	input := CreateAppPasswordInput{Name: "Phone", Scopes: []domain.MailClientProtocol{domain.MailClientProtocolIMAP}, CurrentPassword: "right"}
	if _, _, err := f.service.CreateAppPassword(ctx, withheld, input); !errors.Is(err, domain.ErrOrganizationFeatureNotGranted) {
		t.Fatalf("without the feature = %v", err)
	}
	if list, err := f.service.ListAppPasswords(ctx, withheld); err != nil || list.Access.Enabled || list.Access.Reason != MailClientAccessFeatureNotGranted {
		t.Fatalf("list without the feature = %+v, %v", list, err)
	}
	if _, err := f.service.UpdateMailClientSettings(ctx, f.member, false); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("a member changing the switch = %v", err)
	}
	settings, err := f.service.UpdateMailClientSettings(ctx, f.owner, false)
	if err != nil || settings.AccessEnabled || settings.UpdatedAt == nil {
		t.Fatalf("UpdateMailClientSettings = %+v, %v", settings, err)
	}
	if _, _, err := f.service.CreateAppPassword(ctx, f.member, input); !errors.Is(err, ErrMailClientAccessDisabled) {
		t.Fatalf("with the switch off = %v", err)
	}
	ownerWithoutGrants := f.owner
	ownerWithoutGrants.Features = domain.MemberFeatures{}
	if _, _, err := f.service.CreateAppPassword(ctx, ownerWithoutGrants, input); !errors.Is(err, ErrMailClientAccessDisabled) {
		t.Fatalf("an owner always has the feature, so only the switch refuses: %v", err)
	}
	if list, err := f.service.ListAppPasswords(ctx, f.member); err != nil || list.Access.Enabled || list.Access.Reason != MailClientAccessOrganizationDisabled {
		t.Fatalf("list with the switch off = %+v, %v", list, err)
	}
	if read, err := f.service.MailClientSettings(ctx, f.member); err != nil || read.AccessEnabled {
		t.Fatalf("a member reads the switch = %+v, %v", read, err)
	}
	if read, err := f.service.MailClientSettings(ctx, f.abroad); err != nil || !read.AccessEnabled || read.UpdatedAt != nil {
		t.Fatalf("an organization that never saved it = %+v, %v", read, err)
	}
	if f.reverifier.calls != 0 {
		t.Fatalf("credentials were checked %d times before the feature and switch", f.reverifier.calls)
	}
}

func TestRevokeAppPasswordOwnOnly(t *testing.T) {
	f := newAppPasswordFixture(t)
	ctx := context.Background()
	mine, _ := f.create(t, f.member, "Phone")
	other, _ := f.create(t, f.member, "Laptop")
	for name, viewer := range map[string]domain.OrganizationContext{"another member": f.peer, "the owner": f.owner, "another organization": f.abroad} {
		if err := f.service.RevokeAppPassword(ctx, viewer, mine.ID); !errors.Is(err, ErrMailAppPasswordNotFound) {
			t.Fatalf("%s revoking = %v", name, err)
		}
	}
	if err := f.service.RevokeAppPassword(ctx, f.member, mine.ID); err != nil {
		t.Fatalf("revoke own = %v", err)
	}
	if err := f.service.RevokeAppPassword(ctx, f.member, mine.ID); !errors.Is(err, ErrMailAppPasswordNotFound) {
		t.Fatalf("revoke twice = %v", err)
	}
	if err := f.service.ForceRevokeAppPassword(ctx, f.member, other.ID); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("a member force-revoking = %v", err)
	}
	abroadOwner := f.abroad
	abroadOwner.Role = domain.OrganizationRoleAdmin
	if err := f.service.ForceRevokeAppPassword(ctx, abroadOwner, other.ID); !errors.Is(err, ErrMailAppPasswordNotFound) {
		t.Fatalf("another organization's admin = %v", err)
	}
	if err := f.service.ForceRevokeAppPassword(ctx, f.owner, other.ID); err != nil {
		t.Fatalf("ForceRevokeAppPassword = %v", err)
	}
	var reasons []string
	if err := f.db.Raw(`SELECT revoke_reason FROM mail_app_passwords ORDER BY id`).Scan(&reasons).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Join(reasons, ",") != "user,admin" {
		t.Fatalf("revoke reasons = %v", reasons)
	}
	if list, err := f.service.ListAppPasswords(ctx, f.member); err != nil || len(list.Items) != 0 {
		t.Fatalf("revoked passwords are not listed: %+v, %v", list, err)
	}
}

func TestAppPasswordDirectoryHidesSecretsAndContent(t *testing.T) {
	f := newAppPasswordFixture(t)
	ctx := context.Background()
	phone, secret := f.create(t, f.member, "Phone")
	old, _ := f.create(t, f.peer, "Old")
	f.create(t, f.abroad, "Elsewhere")
	if err := f.service.RevokeAppPassword(ctx, f.peer, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.AppPasswordDirectory(ctx, f.member, 1, 20); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("a member reading the directory = %v", err)
	}
	items, total, err := f.service.AppPasswordDirectory(ctx, f.owner, 1, 20)
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("directory = %d items of %d, %v", len(items), total, err)
	}
	if items[0].ID != old.ID || items[0].RevokeReason != domain.MailAppPasswordRevokedByUser || items[1].ID != phone.ID ||
		items[1].UserEmail != "u902@people.test" || items[1].UserName != "user902" {
		t.Fatalf("directory items = %+v / %+v", items[0], items[1])
	}
	encoded, _ := json.Marshal(toMailAppPasswordDirectoryResponses(items))
	for _, leak := range []string{`"secret"`, "secret_hash", normalizeAppPassword(secret), "Elsewhere"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("the directory carries %q: %s", leak, encoded)
		}
	}
	if execErr := f.db.Exec(`UPDATE mail_app_passwords SET revoked_at = now() - interval '91 days' WHERE id = ?`, old.ID).Error; execErr != nil {
		t.Fatal(execErr)
	}
	if _, recent, listErr := f.service.AppPasswordDirectory(ctx, f.owner, 1, 20); listErr != nil || recent != 1 {
		t.Fatalf("a password revoked 91 days ago is still listed: %d, %v", recent, listErr)
	}
	purged, err := f.service.PurgeRevokedAppPasswords(ctx, time.Now(), 10)
	if err != nil || purged != 1 {
		t.Fatalf("PurgeRevokedAppPasswords = %d, %v", purged, err)
	}
}

func TestHashAppPasswordNormalizes(t *testing.T) {
	pepper := []byte(testAppPasswordPepper)
	if hashAppPassword(pepper, normalizeAppPassword("ABCD efgh IJKL mnop")) != hashAppPassword(pepper, normalizeAppPassword("abcdefghijklmnop")) {
		t.Fatal("case and spaces must not matter")
	}
	if hashAppPassword(pepper, "abcdefghijklmnop") == hashAppPassword([]byte("another-pepper-0123456789abcdef0123"), "abcdefghijklmnop") {
		t.Fatal("the pepper must key the hash")
	}
	if digest := hashAppPassword(pepper, "abcdefghijklmnop"); !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(digest) {
		t.Fatalf("digest = %q", digest)
	}
	seen := map[string]bool{}
	for range 200 {
		display, normalized := newAppPasswordSecret()
		if !appPasswordSecretPattern.MatchString(display) || normalizeAppPassword(display) != normalized || seen[normalized] {
			t.Fatalf("secret %q / %q", display, normalized)
		}
		seen[normalized] = true
	}
}

func TestAppPasswordsNeedAPepper(t *testing.T) {
	f := newAppPasswordFixture(t)
	f.service.settings.AppPasswordPepper = ""
	ctx := context.Background()
	input := CreateAppPasswordInput{Name: "Phone", Scopes: []domain.MailClientProtocol{domain.MailClientProtocolIMAP}, CurrentPassword: "right"}
	if _, _, err := f.service.CreateAppPassword(ctx, f.member, input); !errors.Is(err, ErrMailAppPasswordPepperMissing) {
		t.Fatalf("without a pepper = %v", err)
	}
	if list, err := f.service.ListAppPasswords(ctx, f.member); err != nil || list.Access.Enabled || list.Access.Reason != MailClientAccessUnavailable {
		t.Fatalf("list without a pepper = %+v, %v", list, err)
	}
}

// TestAppPasswordRoutesAnswerTheContract checks the HTTP statuses and envelopes of the routes.
func TestAppPasswordRoutesAnswerTheContract(t *testing.T) {
	f := newAppPasswordFixture(t)
	gin.SetMode(gin.TestMode)
	handler := NewHandler(f.service)
	response.DefaultErrorMapper.Register(domain.ErrPermissionDenied, http.StatusForbidden, domain.CodePermissionDenied)
	response.DefaultErrorMapper.Register(domain.ErrOrganizationFeatureNotGranted, http.StatusForbidden, domain.CodeOrganizationFeatureNotGranted)
	handler.RegisterErrorMappings(response.DefaultErrorMapper)
	viewer := f.member
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(domain.WithOrganizationContext(c.Request.Context(), viewer))
		c.Next()
	})
	engine.GET("/mail-app-passwords", handler.ListAppPasswords)
	engine.POST("/mail-app-passwords", handler.CreateAppPassword)
	engine.DELETE("/mail-app-passwords/:password_id", handler.RevokeAppPassword)
	engine.GET("/mail-app-password-directory", handler.ListAppPasswordDirectory)
	engine.DELETE("/mail-app-password-directory/:password_id", handler.ForceRevokeAppPassword)
	engine.GET("/mail-client-settings", handler.GetMailClientSettings)
	engine.PUT("/mail-client-settings", handler.UpdateMailClientSettings)
	do := func(method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		decoded := map[string]any{}
		_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
		return recorder, decoded
	}

	recorder, body := do(http.MethodPost, "/mail-app-passwords", `{"name":"Phone","scopes":["imap"],"current_password":"wrong"}`)
	errorsByField, _ := body["errors"].(map[string]any)
	if recorder.Code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeValidationFailed || errorsByField["current_password"] == nil {
		t.Fatalf("wrong password = %d %v", recorder.Code, body)
	}
	recorder, body = do(http.MethodPost, "/mail-app-passwords", `{"name":"Phone","scopes":["imap","pop3"],"current_password":"right"}`)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown scope = %d %v", recorder.Code, body)
	}
	recorder, body = do(http.MethodPost, "/mail-app-passwords", `{"name":"Phone","scopes":["imap","smtp"],"current_password":"right"}`)
	if both, _ := body["data"].(map[string]any); recorder.Code != http.StatusCreated || len(both["scopes"].([]any)) != 2 {
		t.Fatalf("imap and smtp = %d %v", recorder.Code, body)
	}
	recorder, body = do(http.MethodPost, "/mail-app-passwords", `{"name":"Phone","scopes":["imap"],"current_password":"right"}`)
	data, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusCreated || !appPasswordSecretPattern.MatchString(data["secret"].(string)) ||
		recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("create = %d %v", recorder.Code, body)
	}
	id := int(data["id"].(float64))
	recorder, body = do(http.MethodGet, "/mail-app-passwords", "")
	data, _ = body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || len(data["items"].([]any)) != 2 || strings.Contains(recorder.Body.String(), "secret") ||
		!strings.Contains(recorder.Body.String(), `"smtp_host":"smtp.example.test","smtp_port":465,"smtp_security":"ssl_tls","smtp_starttls_port":587`) {
		t.Fatalf("list = %d %s", recorder.Code, recorder.Body.String())
	}
	for range 5 {
		do(http.MethodPost, "/mail-app-passwords", `{"name":"Phone","scopes":["imap"],"current_password":"wrong"}`)
	}
	recorder, body = do(http.MethodPost, "/mail-app-passwords", `{"name":"Phone","scopes":["imap"],"current_password":"right"}`)
	if recorder.Code != http.StatusTooManyRequests || body["error_code"] != "COMMON.RATE_LIMITED" || recorder.Header().Get("Retry-After") == "" {
		t.Fatalf("over the attempt budget = %d %v", recorder.Code, body)
	}
	if recorder, body = do(http.MethodGet, "/mail-app-password-directory", ""); recorder.Code != http.StatusForbidden {
		t.Fatalf("a member reading the directory = %d %v", recorder.Code, body)
	}
	if recorder, body = do(http.MethodPut, "/mail-client-settings", `{"access_enabled":false}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("a member changing the switch = %d %v", recorder.Code, body)
	}
	recorder, body = do(http.MethodDelete, "/mail-app-passwords/999999", "")
	if recorder.Code != http.StatusNotFound || body["error_code"] != domain.CodeMailAppPasswordNotFound {
		t.Fatalf("revoke unknown = %d %v", recorder.Code, body)
	}
	if recorder, _ = do(http.MethodDelete, "/mail-app-passwords/"+strconv.Itoa(id), ""); recorder.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", recorder.Code)
	}

	viewer = f.owner
	if recorder, body = do(http.MethodPut, "/mail-client-settings", `{"access_enabled":false}`); recorder.Code != http.StatusOK {
		t.Fatalf("owner switch = %d %v", recorder.Code, body)
	}
	recorder, body = do(http.MethodGet, "/mail-client-settings", "")
	data, _ = body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || data["access_enabled"] != false || data["updated_at"] == nil {
		t.Fatalf("read switch = %d %v", recorder.Code, body)
	}
	recorder, body = do(http.MethodPost, "/mail-app-passwords", `{"name":"Phone","scopes":["imap"],"current_password":"right"}`)
	if recorder.Code != http.StatusForbidden || body["error_code"] != domain.CodeMailClientAccessDisabled {
		t.Fatalf("create with the switch off = %d %v", recorder.Code, body)
	}
	recorder, body = do(http.MethodGet, "/mail-app-password-directory?page=1&per_page=10", "")
	if recorder.Code != http.StatusOK || body["meta"] == nil {
		t.Fatalf("directory = %d %v", recorder.Code, body)
	}
}
