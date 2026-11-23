package organization

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
	"github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

const (
	maxSuspensionReasonLength = 500
	// MaxOrganizationDailySendLimit bounds an operator's daily send limit for one organization.
	MaxOrganizationDailySendLimit = 1_000_000
)

// operatorChange is one operator edit of an organization's controls; nil fields stay as they are.
type operatorChange struct {
	suspend        *bool
	reason         string
	dailySendLimit **int
}

type suspendOrganizationRequest struct {
	Reason string `json:"reason" binding:"required"`
}

// Suspend serves POST /v1/operator/organizations/:id/suspend. Members can no longer act in the
// organization, its API keys stop working, and nothing more is sent for it; mail still arrives.
func (h *OperatorHandler) Suspend(c *gin.Context) {
	organizationID, ok := handler.ParseID(c, "id")
	if !ok {
		return
	}
	var request suspendOrganizationRequest
	if !handler.BindJSON(c, &request) {
		return
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" || len([]rune(reason)) > maxSuspensionReasonLength || strings.IndexFunc(reason, unicode.IsControl) >= 0 {
		response.AbortWithCode(c, http.StatusUnprocessableEntity, response.ErrorCodeValidationFailed,
			"reason must be 1 to 500 characters without control characters")
		return
	}
	suspend := true
	h.update(c, organizationID, "suspend", operatorChange{suspend: &suspend, reason: reason})
}

// Restore serves POST /v1/operator/organizations/:id/restore.
func (h *OperatorHandler) Restore(c *gin.Context) {
	organizationID, ok := handler.ParseID(c, "id")
	if !ok {
		return
	}
	suspend := false
	h.update(c, organizationID, "restore", operatorChange{suspend: &suspend})
}

// SetSendLimit serves PUT /v1/operator/organizations/:id/send-limit.
func (h *OperatorHandler) SetSendLimit(c *gin.Context) {
	organizationID, ok := handler.ParseID(c, "id")
	if !ok {
		return
	}
	var body sendLimitBody
	if !handler.BindJSON(c, &body) {
		return
	}
	value := body.DailySendLimit
	var limit *int
	if value == nil {
		response.AbortWithCode(c, http.StatusUnprocessableEntity, response.ErrorCodeValidationFailed,
			"daily_send_limit is required; null clears it")
		return
	}
	if string(value) != "null" {
		var parsed int
		if err := json.Unmarshal(value, &parsed); err != nil || parsed < 1 || parsed > MaxOrganizationDailySendLimit {
			response.AbortWithCode(c, http.StatusUnprocessableEntity, response.ErrorCodeValidationFailed,
				"daily_send_limit must be null or an integer from 1 to 1000000")
			return
		}
		limit = &parsed
	}
	h.update(c, organizationID, "set_send_limit", operatorChange{dailySendLimit: &limit})
}

func (h *OperatorHandler) update(c *gin.Context, organizationID uint, action string, change operatorChange) {
	before, after, err := h.store.operatorUpdate(c.Request.Context(), organizationID, change)
	if err != nil {
		response.HandleError(c, "Failed to update organization", err)
		return
	}
	changes := map[string]domain.AuditValueChange{}
	if change.suspend != nil {
		changes["suspended_at"] = domain.AuditValueChange{Before: before.SuspendedAt, After: after.SuspendedAt}
		changes["suspension_reason"] = domain.AuditValueChange{Before: before.SuspensionReason, After: after.SuspensionReason}
	}
	if change.dailySendLimit != nil {
		changes["daily_send_limit"] = domain.AuditValueChange{Before: before.DailySendLimit, After: after.DailySendLimit}
	}
	auditstarter.RecordChange(c.Request.Context(), auditstarter.Change{
		Action:     action,
		Resource:   "organizations",
		TargetType: "organization",
		TargetID:   strconv.FormatUint(uint64(organizationID), 10),
		Result:     domain.AuditResultSuccess,
		Changes:    changes,
		Metadata:   map[string]any{"organization_id": organizationID},
	})
	response.Success(c, after)
}

// operatorUpdate applies one change under a row lock and returns the organization before and
// after it. Suspending a suspended organization keeps its first suspension time and replaces the
// reason; restoring an active one changes nothing.
func (r *repository) operatorUpdate(
	ctx context.Context,
	organizationID uint,
	change operatorChange,
) (*OperatorOrganizationResponse, *OperatorOrganizationResponse, error) {
	db, err := r.withContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	if organizationID == 0 {
		return nil, nil, domain.ErrInvalidInput
	}
	var before, after *OperatorOrganizationResponse
	err = db.Transaction(func(tx *gorm.DB) error {
		var row OrganizationPO
		if findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, organizationID).Error; findErr != nil {
			if errors.Is(findErr, gorm.ErrRecordNotFound) {
				return domain.ErrOrganizationNotFound
			}
			return findErr
		}
		counts, countErr := operatorMemberCounts(tx, []uint{row.ID})
		if countErr != nil {
			return countErr
		}
		previous := newOperatorOrganizationResponse(&row, counts[row.ID])
		before = &previous
		updates := map[string]any{}
		if change.suspend != nil {
			if *change.suspend {
				if row.SuspendedAt == nil {
					now := time.Now().UTC()
					row.SuspendedAt = &now
				}
				row.SuspensionReason = change.reason
			} else {
				row.SuspendedAt = nil
				row.SuspensionReason = ""
			}
			updates["suspended_at"] = row.SuspendedAt
			updates["suspension_reason"] = row.SuspensionReason
		}
		if change.dailySendLimit != nil {
			row.DailySendLimit = *change.dailySendLimit
			updates["daily_send_limit"] = row.DailySendLimit
		}
		if len(updates) > 0 {
			if updateErr := tx.Model(&OrganizationPO{}).Where("id = ?", row.ID).Updates(updates).Error; updateErr != nil {
				return updateErr
			}
			if reloadErr := tx.First(&row, row.ID).Error; reloadErr != nil {
				return reloadErr
			}
		}
		current := newOperatorOrganizationResponse(&row, counts[row.ID])
		after = &current
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return before, after, nil
}

// sendLimitBody keeps daily_send_limit raw so an absent field differs from an explicit null.
type sendLimitBody struct {
	DailySendLimit json.RawMessage `json:"daily_send_limit"`
}

func (b *sendLimitBody) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return errors.New("body must be a JSON object")
	}
	type plain sendLimitBody
	return json.Unmarshal(data, (*plain)(b))
}
