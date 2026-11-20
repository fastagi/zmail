package mailbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

// maxPlatformAccountBodyBytes caps a platform account or handover body: 2000 characters of notes
// and a 2048-byte URL with JSON escaping.
const maxPlatformAccountBodyBytes = 8 << 10

// PlatformAccountRequest is the whole of a platform account a create or replace sets. Unknown
// fields are ignored; the service trims and checks every field.
type PlatformAccountRequest struct {
	Platform    string `json:"platform"`
	Handle      string `json:"handle"`
	ProfileURL  string `json:"profile_url"`
	RoleID      *uint  `json:"role_id"`
	MailboxID   *uint  `json:"mailbox_id"`
	OwnerUserID *uint  `json:"owner_user_id"`
	Status      string `json:"status"`
	Notes       string `json:"notes"`
}

func (r PlatformAccountRequest) input() PlatformAccountInput {
	return PlatformAccountInput{
		Platform: r.Platform, Handle: r.Handle, ProfileURL: r.ProfileURL, RoleID: r.RoleID, MailboxID: r.MailboxID,
		OwnerUserID: r.OwnerUserID, Status: domain.PlatformAccountStatus(r.Status), Notes: r.Notes,
	}
}

// PlatformAccountHandoverRequest moves every account of one owner to another.
type PlatformAccountHandoverRequest struct {
	FromUserID uint `json:"from_user_id"`
	ToUserID   uint `json:"to_user_id"`
}

// PlatformAccountResponse is one ledger entry. MailboxAddress is null without a registration
// mailbox, when it was deleted, or when the caller cannot see it.
type PlatformAccountResponse struct {
	ID             uint      `json:"id"`
	Platform       string    `json:"platform"`
	Handle         string    `json:"handle"`
	ProfileURL     *string   `json:"profile_url"`
	RoleID         *uint     `json:"role_id"`
	MailboxID      *uint     `json:"mailbox_id"`
	MailboxAddress *string   `json:"mailbox_address"`
	OwnerUserID    *uint     `json:"owner_user_id"`
	Status         string    `json:"status"`
	Notes          string    `json:"notes"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func toPlatformAccountResponse(account *domain.PlatformAccount) PlatformAccountResponse {
	return PlatformAccountResponse{
		ID: account.ID, Platform: account.Platform, Handle: account.Handle, ProfileURL: optionalText(account.ProfileURL),
		RoleID: account.RoleID, MailboxID: account.MailboxID, MailboxAddress: optionalText(account.MailboxAddress),
		OwnerUserID: account.OwnerUserID, Status: string(account.Status), Notes: account.Notes,
		CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt,
	}
}

func toPlatformAccountResponses(accounts []*domain.PlatformAccount) []PlatformAccountResponse {
	responses := make([]PlatformAccountResponse, 0, len(accounts))
	for _, account := range accounts {
		responses = append(responses, toPlatformAccountResponse(account))
	}
	return responses
}

// PlatformAccountEventResponse is one entry of an account's history. From and To are a status or
// a user or mailbox id as text; null for none.
type PlatformAccountEventResponse struct {
	ID          uint      `json:"id"`
	AccountID   uint      `json:"account_id"`
	Kind        string    `json:"kind"`
	ActorUserID *uint     `json:"actor_user_id"`
	From        *string   `json:"from"`
	To          *string   `json:"to"`
	CreatedAt   time.Time `json:"created_at"`
}

func toPlatformAccountEventResponses(events []*domain.PlatformAccountEvent) []PlatformAccountEventResponse {
	responses := make([]PlatformAccountEventResponse, 0, len(events))
	for _, event := range events {
		responses = append(responses, PlatformAccountEventResponse{
			ID: event.ID, AccountID: event.AccountID, Kind: string(event.Kind), ActorUserID: event.ActorUserID,
			From: optionalText(event.From), To: optionalText(event.To), CreatedAt: event.CreatedAt,
		})
	}
	return responses
}

// PlatformAccountHandoverResponse names the accounts a handover moved, ascending.
type PlatformAccountHandoverResponse struct {
	FromUserID uint   `json:"from_user_id"`
	ToUserID   uint   `json:"to_user_id"`
	AccountIDs []uint `json:"account_ids"`
}

// ListPlatformAccounts lists one page of the ledger entries the caller may see.
func (h *Handler) ListPlatformAccounts(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	filter, err := platformAccountFilter(c)
	if err != nil {
		response.HandleError(c, "Invalid platform account filter", err)
		return
	}
	page := pagination.FromContext(c)
	filter.Page, filter.PerPage = page.GetPage(), page.GetPerPage()
	accounts, total, err := h.service.ListPlatformAccounts(c.Request.Context(), viewer, filter)
	if err != nil {
		response.HandleError(c, "Failed to list platform accounts", err)
		return
	}
	paginator := pagination.NewPaginator(toPlatformAccountResponses(accounts), total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}

// platformAccountFilter reads q, status, and the positive id filters; anything malformed is
// invalid input.
func platformAccountFilter(c *gin.Context) (domain.PlatformAccountFilter, error) {
	filter := domain.PlatformAccountFilter{Query: c.Query("q"), Status: domain.PlatformAccountStatus(c.Query("status"))}
	for name, target := range map[string]*uint{
		"role_id": &filter.RoleID, "mailbox_id": &filter.MailboxID, "owner_user_id": &filter.OwnerUserID,
	} {
		raw, present := c.GetQuery(name)
		if !present {
			continue
		}
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 || id > maxPublicID {
			return filter, fmt.Errorf("%w: %s must be a positive id", domain.ErrInvalidInput, name)
		}
		*target = uint(id)
	}
	return filter, nil
}

// CreatePlatformAccount adds an entry to the ledger. Only owners and admins may.
func (h *Handler) CreatePlatformAccount(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req PlatformAccountRequest
	if !bindLedgerBody(c, &req) {
		return
	}
	account, err := h.service.CreatePlatformAccount(c.Request.Context(), viewer, req.input())
	if err != nil {
		respondOrganizeError(c, "Failed to create platform account", err)
		return
	}
	privateResponse(c)
	response.Created(c, toPlatformAccountResponse(account))
}

// UpdatePlatformAccount replaces every field of a ledger entry. Only owners and admins may.
func (h *Handler) UpdatePlatformAccount(c *gin.Context) {
	viewer, accountID, ok := viewerScope(c, "account_id")
	if !ok {
		return
	}
	var req PlatformAccountRequest
	if !bindLedgerBody(c, &req) {
		return
	}
	account, err := h.service.UpdatePlatformAccount(c.Request.Context(), viewer, accountID, req.input())
	if err != nil {
		respondOrganizeError(c, "Failed to update platform account", err)
		return
	}
	privateResponse(c)
	response.Success(c, toPlatformAccountResponse(account))
}

// DeletePlatformAccount removes a ledger entry after recording its deletion. Only owners and
// admins may.
func (h *Handler) DeletePlatformAccount(c *gin.Context) {
	viewer, accountID, ok := viewerScope(c, "account_id")
	if !ok {
		return
	}
	if err := h.service.DeletePlatformAccount(c.Request.Context(), viewer, accountID); err != nil {
		response.HandleError(c, "Failed to delete platform account", err)
		return
	}
	response.NoContent(c)
}

// ListPlatformAccountEvents lists one page of an entry's handover and status history.
func (h *Handler) ListPlatformAccountEvents(c *gin.Context) {
	viewer, accountID, ok := viewerScope(c, "account_id")
	if !ok {
		return
	}
	page := pagination.FromContext(c)
	events, total, err := h.service.ListPlatformAccountEvents(c.Request.Context(), viewer, accountID, page.GetPage(), page.GetPerPage())
	if err != nil {
		response.HandleError(c, "Failed to list platform account events", err)
		return
	}
	paginator := pagination.NewPaginator(toPlatformAccountEventResponses(events), total, page.GetPage(), page.GetPerPage())
	paginator.SetPath(c.Request.URL.Path)
	privateResponse(c)
	response.Success(c, paginator)
}

// HandOverPlatformAccounts moves every entry owned by one user to another. Only owners and admins
// may.
func (h *Handler) HandOverPlatformAccounts(c *gin.Context) {
	viewer, ok := organizationScope(c)
	if !ok {
		return
	}
	var req PlatformAccountHandoverRequest
	if !bindLedgerBody(c, &req) {
		return
	}
	result, err := h.service.HandOverPlatformAccounts(c.Request.Context(), viewer, req.FromUserID, req.ToUserID)
	if err != nil {
		respondOrganizeError(c, "Failed to hand over platform accounts", err)
		return
	}
	accountIDs := result.AccountIDs
	if accountIDs == nil {
		accountIDs = []uint{}
	}
	privateResponse(c)
	response.Created(c, PlatformAccountHandoverResponse{FromUserID: result.FromUserID, ToUserID: result.ToUserID, AccountIDs: accountIDs})
}

// bindLedgerBody decodes a body of at most maxPlatformAccountBodyBytes that is exactly one JSON
// object into target.
// Anything else answers 400 COMMON.INVALID_INPUT.
func bindLedgerBody(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPlatformAccountBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	trimmed := bytes.TrimSpace(body)
	if err != nil || len(trimmed) == 0 || trimmed[0] != '{' || json.Unmarshal(trimmed, target) != nil {
		// Neither the body nor the decoder's message reaches the response.
		response.HandleError(c, "Invalid request body",
			fmt.Errorf("%w: the body must be one JSON object of at most %d bytes", domain.ErrInvalidInput, maxPlatformAccountBodyBytes))
		return false
	}
	return true
}
