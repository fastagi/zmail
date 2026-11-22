package mailbox

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// DefaultConnectionDirectoryPageSize and MaxConnectionDirectoryPageSize bound one page of the
// connection directory.
const (
	DefaultConnectionDirectoryPageSize = 15
	MaxConnectionDirectoryPageSize     = 100
)

// ExternalMailDirectoryService lets owners and admins see that members connected outside accounts,
// and disconnect them, without ever seeing the mail or the full address.
type ExternalMailDirectoryService interface {
	// ExternalMailConnectionDirectory lists one page of the organization's connections, newest
	// first, for owners and admins.
	ExternalMailConnectionDirectory(ctx context.Context, viewer domain.OrganizationContext, beforeID uint, limit int) (*ExternalMailConnectionDirectoryPage, error)
	// ForceDisconnectExternalMailConnection revokes any connection of the organization, as its
	// owner's unbind would, for owners and admins.
	ForceDisconnectExternalMailConnection(ctx context.Context, viewer domain.OrganizationContext, id uint) error
}

// ExternalMailConnectionDirectoryEntry is one connection as a manager sees it.
type ExternalMailConnectionDirectoryEntry struct {
	Connection *domain.ExternalMailConnection
	// OwnerDisplayName is the owner's name while they are a member; empty otherwise.
	OwnerDisplayName string
}

// ExternalMailConnectionDirectoryPage is one directory page.
type ExternalMailConnectionDirectoryPage struct {
	Entries []ExternalMailConnectionDirectoryEntry
	// NextBeforeID is the id the next, older page starts below; 0 on the last page.
	NextBeforeID uint
}

var _ ExternalMailDirectoryService = (*service)(nil)

func (s *service) ExternalMailConnectionDirectory(ctx context.Context, viewer domain.OrganizationContext, beforeID uint, limit int) (*ExternalMailConnectionDirectoryPage, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	page := &ExternalMailConnectionDirectoryPage{Entries: []ExternalMailConnectionDirectoryEntry{}}
	if !s.externalMailAvailable() {
		return page, nil
	}
	if limit <= 0 {
		limit = DefaultConnectionDirectoryPageSize
	}
	limit = min(limit, MaxConnectionDirectoryPageSize)
	connections, err := s.externalMail.ListExternalMailConnectionDirectory(ctx, viewer.OrganizationID, beforeID, limit+1)
	if err != nil {
		return nil, err
	}
	if len(connections) > limit {
		connections = connections[:limit]
		page.NextBeforeID = connections[limit-1].ID
	}
	names, err := s.memberDisplayNames(ctx, viewer.OrganizationID, connections)
	if err != nil {
		return nil, err
	}
	for _, connection := range connections {
		entry := ExternalMailConnectionDirectoryEntry{Connection: connection}
		if connection.OwnerUserID != nil {
			entry.OwnerDisplayName = names[*connection.OwnerUserID]
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, nil
}

// memberDisplayNames maps the owners of connections who are still members to their nickname, or
// their username without one.
func (s *service) memberDisplayNames(ctx context.Context, organizationID uint, connections []*domain.ExternalMailConnection) (map[uint]string, error) {
	names := map[uint]string{}
	userIDs := make([]uint, 0, len(connections))
	for _, connection := range connections {
		if connection.OwnerUserID != nil {
			userIDs = append(userIDs, *connection.OwnerUserID)
		}
	}
	if len(userIDs) == 0 || s.memberships == nil {
		return names, nil
	}
	memberships, err := s.memberships.ListMembershipsByUserIDs(ctx, organizationID, userIDs)
	if err != nil {
		return nil, err
	}
	for _, membership := range memberships {
		if membership.User == nil {
			continue
		}
		name := strings.TrimSpace(membership.User.Nickname)
		if name == "" {
			name = membership.User.Username
		}
		names[membership.UserID] = name
	}
	return names, nil
}

func (s *service) ForceDisconnectExternalMailConnection(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	if !viewer.Role.CanManageOrganization() {
		return domain.ErrPermissionDenied
	}
	if !s.externalMailAvailable() {
		return ErrExternalMailConnectionNotFound
	}
	connection, err := s.externalMail.FindExternalMailConnection(ctx, viewer.OrganizationID, id)
	if err != nil {
		return err
	}
	if connection.TenantID != nil {
		return s.unassignTenantConnection(ctx, viewer, connection)
	}
	if connection.Shared {
		return s.disconnectShared(ctx, viewer, connection)
	}
	return s.forceDisconnect(ctx, viewer, connection)
}

// maskAccountAddress keeps the first two characters of the local part (one when it is that short)
// and the domain: st***@gmail.com. A manager learns which provider and roughly whose account it is,
// never the address itself.
func maskAccountAddress(address string) string {
	at := strings.LastIndexByte(address, '@')
	if at < 0 {
		return "***"
	}
	local, host := address[:at], address[at+1:]
	keep := 2
	if utf8.RuneCountInString(local) <= 2 {
		keep = 1
	}
	runes := []rune(local)
	if len(runes) < keep {
		keep = len(runes)
	}
	return string(runes[:keep]) + "***@" + host
}

// ExternalMailConnectionDirectoryRequest holds the query of GET /v1/external-mail-connection-directory.
type ExternalMailConnectionDirectoryRequest struct {
	Cursor  string `form:"cursor"`
	PerPage int    `form:"per_page" binding:"omitempty,min=1,max=100"`
}

// ExternalMailConnectionDirectoryEntryResponse is one connection as a manager sees it: never the
// mail or the token, and for a member's own account never the full address or the mailbox. A
// shared connection is the organization's own team account, so it carries both.
type ExternalMailConnectionDirectoryEntryResponse struct {
	ID                   uint       `json:"id"`
	Provider             string     `json:"provider"`
	Status               string     `json:"status"`
	Shared               bool       `json:"shared"`
	TenantID             *uint      `json:"tenant_id"`
	AccountAddress       *string    `json:"account_address"`
	MailboxID            *uint      `json:"mailbox_id"`
	MaskedAccountAddress string     `json:"masked_account_address"`
	OwnerUserID          *uint      `json:"owner_user_id"`
	OwnerDisplayName     *string    `json:"owner_display_name"`
	LastSyncedAt         *time.Time `json:"last_synced_at"`
	CreatedAt            time.Time  `json:"created_at"`
}

func toExternalMailConnectionDirectoryEntryResponse(entry ExternalMailConnectionDirectoryEntry) ExternalMailConnectionDirectoryEntryResponse {
	connection := entry.Connection
	item := ExternalMailConnectionDirectoryEntryResponse{
		ID: connection.ID, Provider: string(connection.Provider), Status: string(connection.Status), Shared: connection.Shared,
		MaskedAccountAddress: maskAccountAddress(connection.AccountAddress), OwnerUserID: connection.OwnerUserID,
		OwnerDisplayName: optionalText(entry.OwnerDisplayName), CreatedAt: connection.CreatedAt.UTC(),
		TenantID: connection.TenantID,
	}
	// A shared connection and a tenant assignment are at the organization's own addresses.
	if connection.Shared || connection.TenantID != nil {
		item.AccountAddress, item.MailboxID = new(connection.AccountAddress), new(connection.MailboxID)
	}
	if connection.LastSyncedAt != nil {
		item.LastSyncedAt = new(connection.LastSyncedAt.UTC())
	}
	return item
}

// ListExternalMailConnectionDirectory returns one keyset page of the organization's connections for
// owners and admins.
func (h *Handler) ListExternalMailConnectionDirectory(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	if _, present := c.GetQueryArray("page"); present {
		response.HandleError(c, "Invalid connection directory query", fmt.Errorf("%w: page is not supported; use cursor", domain.ErrInvalidInput))
		return
	}
	var request ExternalMailConnectionDirectoryRequest
	if !bindValidatedQuery(c, &request) {
		return
	}
	perPage := request.PerPage
	if perPage == 0 {
		perPage = DefaultConnectionDirectoryPageSize
	}
	var before uint
	if request.Cursor != "" {
		var err error
		if before, err = decodeDirectoryCursor(request.Cursor); err != nil {
			response.HandleError(c, "Invalid connection directory cursor", err)
			return
		}
	}
	page, err := h.service.ExternalMailConnectionDirectory(c.Request.Context(), viewer, before, perPage)
	if err != nil {
		response.HandleError(c, "Failed to list the connection directory", err)
		return
	}
	next := ""
	if page.NextBeforeID != 0 {
		next = encodeDirectoryCursor(page.NextBeforeID)
	}
	items := make([]ExternalMailConnectionDirectoryEntryResponse, 0, len(page.Entries))
	for _, entry := range page.Entries {
		items = append(items, toExternalMailConnectionDirectoryEntryResponse(entry))
	}
	privateResponse(c)
	response.SuccessCursorPage(c, items, perPage, next)
}

// ForceDisconnectExternalMailConnection revokes a member's connection for an owner or admin.
func (h *Handler) ForceDisconnectExternalMailConnection(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	_, connectionID, ok := viewerScope(c, "connection_id")
	if !ok {
		return
	}
	if err := h.service.ForceDisconnectExternalMailConnection(c.Request.Context(), viewer, connectionID); err != nil {
		response.HandleError(c, "Failed to disconnect external mail connection", err)
		return
	}
	response.NoContent(c)
}
