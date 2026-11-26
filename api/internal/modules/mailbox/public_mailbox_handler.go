package mailbox

import (
	"github.com/gin-gonic/gin"

	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// PublicMailboxOptionsResponse describes self-serve public mailbox eligibility and policy.
type PublicMailboxOptionsResponse struct {
	Enabled               bool     `json:"enabled"`
	Domains               []string `json:"domains"`
	MinLocalLength        int      `json:"min_local_length"`
	LimitPerUser          int      `json:"limit_per_user"`
	ClaimedCount          int      `json:"claimed_count"`
	ClaimedAddress        string   `json:"claimed_address"`
	ClaimedOrganizationID *uint    `json:"claimed_organization_id"`
	ClaimedMailboxID      *uint    `json:"claimed_mailbox_id"`
	DailySendLimitNew     int      `json:"daily_send_limit_new"`
	DailySendLimit        int      `json:"daily_send_limit"`
	MaxRecipientsNew      int      `json:"max_recipients_new"`
	MaxRecipients         int      `json:"max_recipients"`
}

// PublicMailboxAvailabilityResponse reports whether a desired public address is available.
type PublicMailboxAvailabilityResponse struct {
	Address     string   `json:"address"`
	LocalPart   string   `json:"local_part"`
	Domain      string   `json:"domain"`
	Status      string   `json:"status"`
	Suggestions []string `json:"suggestions"`
}

// ClaimPublicMailboxRequest asks to claim a self-serve personal mailbox on a public domain.
type ClaimPublicMailboxRequest struct {
	LocalPart   string `json:"local_part" binding:"required,max=64"`
	Domain      string `json:"domain" binding:"max=253"`
	DisplayName string `json:"display_name" binding:"max=120"`
}

// GetPublicMailboxOptions returns the public mailbox policy and the authenticated user's global claim state.
func (h *Handler) GetPublicMailboxOptions(c *gin.Context) {
	userID, ok := httphandler.GetUserID(c)
	if !ok {
		return
	}
	options, err := h.publicMailbox.PublicMailboxOptions(c.Request.Context(), userID)
	if err != nil {
		response.HandleError(c, "Failed to load public mailbox options", err)
		return
	}
	domains := options.Domains
	if domains == nil {
		domains = []string{}
	}
	privateResponse(c)
	response.Success(c, PublicMailboxOptionsResponse{
		Enabled:               options.Enabled,
		Domains:               domains,
		MinLocalLength:        options.MinLocalLength,
		LimitPerUser:          options.LimitPerUser,
		ClaimedCount:          options.ClaimedCount,
		ClaimedAddress:        options.ClaimedAddress,
		ClaimedOrganizationID: options.ClaimedOrganizationID,
		ClaimedMailboxID:      options.ClaimedMailboxID,
		DailySendLimitNew:     options.DailySendLimitNew,
		DailySendLimit:        options.DailySendLimit,
		MaxRecipientsNew:      options.MaxRecipientsNew,
		MaxRecipients:         options.MaxRecipients,
	})
}

// CheckPublicMailboxAvailability checks whether a desired public address is free and suggests alternatives.
func (h *Handler) CheckPublicMailboxAvailability(c *gin.Context) {
	userID, ok := httphandler.GetUserID(c)
	if !ok {
		return
	}
	result, err := h.publicMailbox.CheckPublicMailboxAvailability(
		c.Request.Context(), userID, c.Query("local_part"), c.Query("domain"),
	)
	if err != nil {
		response.HandleError(c, "Failed to check public mailbox availability", err)
		return
	}
	suggestions := result.Suggestions
	if suggestions == nil {
		suggestions = []string{}
	}
	privateResponse(c)
	response.Success(c, PublicMailboxAvailabilityResponse{
		Address:     result.Address,
		LocalPart:   result.LocalPart,
		Domain:      result.Domain,
		Status:      result.Status,
		Suggestions: suggestions,
	})
}

// ClaimPublicMailbox claims one free personal mailbox on a configured public domain.
func (h *Handler) ClaimPublicMailbox(c *gin.Context) {
	viewer, ok := managerScope(c)
	if !ok {
		return
	}
	var req ClaimPublicMailboxRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	mailbox, err := h.publicMailbox.ClaimPublicMailbox(c.Request.Context(), viewer, ClaimPublicMailboxInput{
		LocalPart:   req.LocalPart,
		Domain:      req.Domain,
		DisplayName: req.DisplayName,
	})
	if err != nil {
		response.HandleError(c, "Failed to claim public mailbox", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailboxResponse(mailbox))
}
