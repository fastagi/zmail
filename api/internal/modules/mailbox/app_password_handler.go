package mailbox

import (
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

// maxAppPasswordBodyBytes bounds a mail client password creation request.
const maxAppPasswordBodyBytes = 2048

// createAppPasswordRequest creates a mail client password. The credentials are never logged or
// echoed back; the service validates every field.
type createAppPasswordRequest struct {
	Name            string   `json:"name"`
	Scopes          []string `json:"scopes"`
	CurrentPassword string   `json:"current_password"`
	TOTPCode        string   `json:"totp_code"`
}

// updateMailClientSettingsRequest turns mail app sign-in on or off.
type updateMailClientSettingsRequest struct {
	AccessEnabled *bool `json:"access_enabled" binding:"required"`
}

// MailAppPasswordResponse is a mail client password without its secret.
type MailAppPasswordResponse struct {
	ID               uint       `json:"id"`
	Name             string     `json:"name"`
	Scopes           []string   `json:"scopes"`
	Hint             string     `json:"hint"`
	CreatedAt        time.Time  `json:"created_at"`
	LastUsedAt       *time.Time `json:"last_used_at"`
	LastUsedProtocol *string    `json:"last_used_protocol"`
}

// MailAppPasswordCreatedResponse adds the secret, carried by the creation response only.
type MailAppPasswordCreatedResponse struct {
	MailAppPasswordResponse
	Secret string `json:"secret"`
}

// MailClientAccessResponse says whether the caller's mail apps can sign in.
type MailClientAccessResponse struct {
	Enabled bool    `json:"enabled"`
	Reason  *string `json:"reason"`
}

// MailClientServerResponse is the public IMAP and SMTP servers.
type MailClientServerResponse struct {
	IMAPHost         string `json:"imap_host"`
	IMAPPort         int    `json:"imap_port"`
	IMAPSecurity     string `json:"imap_security"`
	SMTPHost         string `json:"smtp_host"`
	SMTPPort         int    `json:"smtp_port"`
	SMTPSecurity     string `json:"smtp_security"`
	SMTPStartTLSPort int    `json:"smtp_starttls_port"`
}

// MailAppPasswordListResponse is the caller's own passwords, the cap, access, and the server.
type MailAppPasswordListResponse struct {
	Items  []MailAppPasswordResponse `json:"items"`
	Limit  int                       `json:"limit"`
	Access MailClientAccessResponse  `json:"access"`
	Server MailClientServerResponse  `json:"server"`
}

// MailAppPasswordDirectoryEntryResponse is one password in the organization directory.
type MailAppPasswordDirectoryEntryResponse struct {
	MailAppPasswordResponse
	MembershipID uint       `json:"membership_id"`
	UserID       uint       `json:"user_id"`
	UserEmail    string     `json:"user_email"`
	UserName     string     `json:"user_name"`
	RevokedAt    *time.Time `json:"revoked_at"`
	RevokeReason *string    `json:"revoke_reason"`
}

// MailClientSettingsResponse is the organization switch.
type MailClientSettingsResponse struct {
	AccessEnabled bool       `json:"access_enabled"`
	UpdatedAt     *time.Time `json:"updated_at"`
}

func toMailAppPasswordResponse(password *domain.MailAppPassword) MailAppPasswordResponse {
	scopes := make([]string, 0, 2)
	for _, scope := range password.Scopes() {
		scopes = append(scopes, string(scope))
	}
	item := MailAppPasswordResponse{
		ID: password.ID, Name: password.Name, Scopes: scopes, Hint: password.Hint,
		CreatedAt: password.CreatedAt, LastUsedAt: password.LastUsedAt,
	}
	if password.LastUsedProtocol != "" {
		item.LastUsedProtocol = new(string(password.LastUsedProtocol))
	}
	return item
}

func toMailAppPasswordCreatedResponse(password *domain.MailAppPassword, secret string) MailAppPasswordCreatedResponse {
	return MailAppPasswordCreatedResponse{MailAppPasswordResponse: toMailAppPasswordResponse(password), Secret: secret}
}

func toMailAppPasswordListResponse(list *AppPasswordList) MailAppPasswordListResponse {
	items := make([]MailAppPasswordResponse, 0, len(list.Items))
	for _, password := range list.Items {
		items = append(items, toMailAppPasswordResponse(password))
	}
	access := MailClientAccessResponse{Enabled: list.Access.Enabled}
	if list.Access.Reason != "" {
		access.Reason = new(list.Access.Reason)
	}
	return MailAppPasswordListResponse{
		Items: items, Limit: list.Limit, Access: access,
		Server: MailClientServerResponse{
			IMAPHost: list.Server.IMAPHost, IMAPPort: list.Server.IMAPPort, IMAPSecurity: list.Server.IMAPSecurity,
			SMTPHost: list.Server.SMTPHost, SMTPPort: list.Server.SMTPPort, SMTPSecurity: list.Server.SMTPSecurity,
			SMTPStartTLSPort: list.Server.SMTPStartTLSPort,
		},
	}
}

func toMailAppPasswordDirectoryResponses(passwords []*domain.MailAppPassword) []MailAppPasswordDirectoryEntryResponse {
	items := make([]MailAppPasswordDirectoryEntryResponse, 0, len(passwords))
	for _, password := range passwords {
		item := MailAppPasswordDirectoryEntryResponse{
			MailAppPasswordResponse: toMailAppPasswordResponse(password),
			MembershipID:            password.MembershipID, UserID: password.UserID,
			UserEmail: password.UserEmail, UserName: password.UserName, RevokedAt: password.RevokedAt,
		}
		if password.RevokeReason != "" {
			item.RevokeReason = new(string(password.RevokeReason))
		}
		items = append(items, item)
	}
	return items
}

func toMailClientSettingsResponse(settings MailClientSettings) MailClientSettingsResponse {
	return MailClientSettingsResponse(settings)
}

// ListAppPasswords lists the caller's own mail client passwords.
// fastbuild:bounded-list max=20 reason=a member holds at most MaxAppPasswordsPerMember passwords
func (h *Handler) ListAppPasswords(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	list, err := h.clientAccess.ListAppPasswords(c.Request.Context(), viewer)
	if err != nil {
		response.HandleError(c, "Failed to list mail client passwords", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailAppPasswordListResponse(list))
}

// CreateAppPassword creates a mail client password and answers its secret, this once.
func (h *Handler) CreateAppPassword(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req createAppPasswordRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	scopes := make([]domain.MailClientProtocol, 0, len(req.Scopes))
	for _, scope := range req.Scopes {
		scopes = append(scopes, domain.MailClientProtocol(scope))
	}
	password, secret, err := h.clientAccess.CreateAppPassword(c.Request.Context(), viewer, CreateAppPasswordInput{
		Name: req.Name, Scopes: scopes, CurrentPassword: req.CurrentPassword, TOTPCode: req.TOTPCode,
	})
	if err != nil {
		var retry *RetryAfterError
		if errors.As(err, &retry) {
			c.Header("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.RetryAfter.Seconds())))))
		}
		respondOrganizeError(c, "Failed to create mail client password", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailAppPasswordCreatedResponse(password, secret))
}

// RevokeAppPassword revokes one of the caller's own mail client passwords.
func (h *Handler) RevokeAppPassword(c *gin.Context) {
	viewer, id, ok := viewerScope(c, "password_id")
	if !ok {
		return
	}
	if err := h.clientAccess.RevokeAppPassword(c.Request.Context(), viewer, id); err != nil {
		response.HandleError(c, "Failed to revoke mail client password", err)
		return
	}
	response.NoContent(c)
}

// ListAppPasswordDirectory pages every mail client password of the organization.
func (h *Handler) ListAppPasswordDirectory(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	page := pagination.FromContext(c)
	passwords, total, err := h.clientAccess.AppPasswordDirectory(c.Request.Context(), viewer, page.GetPage(), page.GetPerPage())
	if err != nil {
		response.HandleError(c, "Failed to list the mail client password directory", err)
		return
	}
	paginator := pagination.NewPaginator(toMailAppPasswordDirectoryResponses(passwords), total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}

// ForceRevokeAppPassword revokes a member's mail client password.
func (h *Handler) ForceRevokeAppPassword(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	_, id, ok := viewerScope(c, "password_id")
	if !ok {
		return
	}
	if err := h.clientAccess.ForceRevokeAppPassword(c.Request.Context(), viewer, id); err != nil {
		response.HandleError(c, "Failed to revoke mail client password", err)
		return
	}
	response.NoContent(c)
}

// GetMailClientSettings reads the organization's mail app sign-in switch.
func (h *Handler) GetMailClientSettings(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	settings, err := h.clientAccess.MailClientSettings(c.Request.Context(), viewer)
	if err != nil {
		response.HandleError(c, "Failed to read mail client settings", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailClientSettingsResponse(settings))
}

// UpdateMailClientSettings turns the organization's mail app sign-in on or off.
func (h *Handler) UpdateMailClientSettings(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	var req updateMailClientSettingsRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	settings, err := h.clientAccess.UpdateMailClientSettings(c.Request.Context(), viewer, *req.AccessEnabled)
	if err != nil {
		response.HandleError(c, "Failed to update mail client settings", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailClientSettingsResponse(settings))
}
