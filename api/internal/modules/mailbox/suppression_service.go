package mailbox

import (
	"context"
	"fmt"
	"strconv"

	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// ListSuppressions lists one page of the organization's suppression list, newest first, and the
// cursor of the next page (nil on the last). Only owners and admins may.
func (s *service) ListSuppressions(
	ctx context.Context,
	viewer domain.OrganizationContext,
	filter domain.MailSuppressionFilter,
) ([]domain.MailSuppression, *uint, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, nil, domain.ErrPermissionDenied
	}
	if s.transactional == nil {
		return nil, nil, domain.ErrServiceUnavailable
	}
	filter.Email = normalizeEmail(filter.Email)
	switch {
	case len(filter.Email) > transactionalLogMaxRecipient:
		return nil, nil, fmt.Errorf("%w: email is too long", domain.ErrInvalidInput)
	case filter.Reason != "" && filter.Reason != domain.MailSuppressionReasonBounced &&
		filter.Reason != domain.MailSuppressionReasonComplained:
		return nil, nil, fmt.Errorf("%w: unknown reason", domain.ErrInvalidInput)
	case filter.PerPage < 0 || filter.PerPage > suppressionMaxPerPage:
		return nil, nil, fmt.Errorf("%w: per_page must be 1 to %d", domain.ErrInvalidInput, suppressionMaxPerPage)
	}
	if filter.PerPage == 0 {
		filter.PerPage = suppressionDefaultPerPage
	}
	suppressions, err := s.transactional.ListSuppressions(ctx, viewer.OrganizationID, filter)
	if err != nil {
		return nil, nil, err
	}
	if len(suppressions) <= filter.PerPage {
		return suppressions, nil, nil
	}
	suppressions = suppressions[:filter.PerPage]
	next := suppressions[len(suppressions)-1].ID
	return suppressions, &next, nil
}

// RemoveSuppression takes an entry off the organization's suppression list. It succeeds whether
// or not the organization had the entry, so it discloses nothing, and audits only a removal that
// deleted one. A contact's status is never changed.
func (s *service) RemoveSuppression(ctx context.Context, viewer domain.OrganizationContext, id uint) error {
	if !viewer.Role.CanManageOrganization() {
		return domain.ErrPermissionDenied
	}
	if s.transactional == nil {
		return domain.ErrServiceUnavailable
	}
	removed, err := s.transactional.DeleteSuppression(ctx, viewer.OrganizationID, id)
	if err != nil || removed == nil {
		return err
	}
	if s.recordAudit != nil {
		// The address stays out of the audit entry; the id and reason identify the removal.
		s.recordAudit(ctx, auditstarter.Change{
			Action:     "mail_suppression.removed",
			Resource:   "mail_suppressions",
			TargetType: "mail_suppression",
			TargetID:   strconv.FormatUint(uint64(removed.ID), 10),
			Result:     domain.AuditResultSuccess,
			Changes:    map[string]domain.AuditValueChange{"reason": {Before: removed.Reason}},
		})
	}
	return nil
}
