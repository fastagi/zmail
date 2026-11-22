package organization

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/capabilities/idgen"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
)

// audit records one invitation change on the request's audit entry. The metadata never carries
// a token or an invitation link; link_issued says only whether one was returned.
func (s *service) audit(ctx context.Context, change auditstarter.Change) {
	if s.recordAudit != nil {
		s.recordAudit(ctx, change)
		return
	}
	auditstarter.RecordChange(ctx, change)
}

func (s *service) invitationManager(ctx context.Context, userID, organizationID uint) (*domain.OrganizationMembership, error) {
	if s == nil || s.repo == nil {
		return nil, domain.ErrServiceUnavailable
	}
	membership, err := s.repo.FindForUser(ctx, organizationID, userID)
	if err != nil {
		return nil, err
	}
	if membership == nil || membership.Organization == nil {
		return nil, domain.ErrOrganizationNotFound
	}
	if !membership.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	return membership, nil
}

func generateOrganizationInvitationToken() (string, error) {
	secret, err := crypto.GenerateKeyHex(24)
	if err != nil {
		return "", fmt.Errorf("generate organization invitation token: %w", err)
	}
	return "oinv_" + strings.ToLower(idgen.ShortID()) + "." + secret, nil
}

func (s *service) Invite(
	ctx context.Context,
	userID,
	organizationID uint,
	req *CreateOrganizationInvitationRequest,
) (*OrganizationInvitationResult, error) {
	if userID == 0 || organizationID == 0 || req == nil || len(req.validationErrors()) > 0 {
		return nil, domain.ErrInvalidInput
	}
	if s.invitationRepo == nil {
		return nil, domain.ErrServiceUnavailable
	}
	manager, err := s.invitationManager(ctx, userID, organizationID)
	if err != nil {
		return nil, err
	}

	token, err := generateOrganizationInvitationToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	invitation := &domain.OrganizationInvitation{
		OrganizationID: organizationID,
		InvitedBy:      userID,
		Email:          normalizeInvitationEmail(req.Email),
		Role:           req.Role,
		TokenHash:      crypto.SHA256Hex(token),
		ExpiresAt:      now.Add(s.invitationPolicy.TTL),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.invitationRepo.CreateInvitation(ctx, invitation, now); err != nil {
		return nil, fmt.Errorf("create organization invitation: %w", err)
	}

	emailStatus := s.sendInvitationEmail(ctx, invitation, manager.Organization.Name, token)
	link := invitationAcceptURL(s.invitationPolicy.PublicURL, token)

	s.audit(ctx, auditstarter.Change{
		Action:     "invite",
		Resource:   "organization_invitations",
		TargetType: "organization_invitation",
		TargetID:   strconv.FormatUint(uint64(invitation.ID), 10),
		Result:     domain.AuditResultSuccess,
		Metadata: map[string]any{
			"organization_id":   organizationID,
			"role":              invitation.Role,
			"email_send_status": emailStatus,
			"link_issued":       link != "",
		},
	})
	return &OrganizationInvitationResult{
		Invitation:      invitation,
		EmailSendStatus: emailStatus,
		InvitationURL:   link,
	}, nil
}

// ResendInvitation gives a pending invitation a fresh token and expiry and emails the new link.
// Earlier links stop working, so only the latest email can be accepted. The new link is also
// returned once to the calling manager. Without a configured mailer the token and expiry are
// kept, the attempt is recorded as not_configured, and no link is returned because none was
// minted. Resends are limited per invitation and per organization.
func (s *service) ResendInvitation(ctx context.Context, userID, organizationID, invitationID uint) (*OrganizationInvitationResult, error) {
	if userID == 0 || organizationID == 0 || invitationID == 0 {
		return nil, domain.ErrInvalidInput
	}
	if s.invitationRepo == nil {
		return nil, domain.ErrServiceUnavailable
	}
	manager, err := s.invitationManager(ctx, userID, organizationID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	// Check the invitation before spending quota, so a stale row cannot use up the budget.
	invitation, err := s.invitationRepo.FindPendingInvitation(ctx, organizationID, invitationID, now)
	if err != nil {
		return nil, err
	}
	if err = s.takeResendQuota(ctx, organizationID, invitationID); err != nil {
		return nil, err
	}

	rotated := s.invitationMailer != nil && s.invitationMailer.IsConfigured()
	if !rotated {
		emailStatus := s.sendInvitationEmail(ctx, invitation, manager.Organization.Name, "")
		s.recordResend(ctx, invitation, emailStatus, false, false)
		return &OrganizationInvitationResult{Invitation: invitation, EmailSendStatus: emailStatus}, nil
	}

	token, err := generateOrganizationInvitationToken()
	if err != nil {
		return nil, err
	}
	invitation, err = s.invitationRepo.ReissueInvitation(
		ctx,
		organizationID,
		invitationID,
		crypto.SHA256Hex(token),
		now.Add(s.invitationPolicy.TTL),
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("reissue organization invitation: %w", err)
	}
	emailStatus := s.sendInvitationEmail(ctx, invitation, manager.Organization.Name, token)
	link := invitationAcceptURL(s.invitationPolicy.PublicURL, token)
	s.recordResend(ctx, invitation, emailStatus, true, link != "")
	return &OrganizationInvitationResult{
		Invitation:      invitation,
		EmailSendStatus: emailStatus,
		InvitationURL:   link,
	}, nil
}

func (s *service) recordResend(
	ctx context.Context,
	invitation *domain.OrganizationInvitation,
	emailStatus InvitationEmailSendStatus,
	tokenRotated bool,
	linkIssued bool,
) {
	s.audit(ctx, auditstarter.Change{
		Action:     "resend",
		Resource:   "organization_invitations",
		TargetType: "organization_invitation",
		TargetID:   strconv.FormatUint(uint64(invitation.ID), 10),
		Result:     domain.AuditResultSuccess,
		Metadata: map[string]any{
			"organization_id":   invitation.OrganizationID,
			"role":              invitation.Role,
			"email_send_status": emailStatus,
			"token_rotated":     tokenRotated,
			"link_issued":       linkIssued,
		},
	})
}

// ErrInvitationResendRateLimited refuses a resend over the per-invitation or per-organization quota.
var ErrInvitationResendRateLimited = errors.New("organization invitation resend is rate limited")

// InvitationRetryAfterError carries when a refused resend may be tried again.
type InvitationRetryAfterError struct {
	Err        error
	RetryAfter time.Duration
}

func (e *InvitationRetryAfterError) Error() string { return e.Err.Error() }

func (e *InvitationRetryAfterError) Unwrap() error { return e.Err }

// takeResendQuota consumes one unit of each resend quota, refusing with the wait of the first
// exhausted one.
func (s *service) takeResendQuota(ctx context.Context, organizationID, invitationID uint) error {
	invitationKey := strconv.FormatUint(uint64(invitationID), 10)
	checks := []struct {
		store ratelimit.Store
		key   string
	}{
		{s.invitationPolicy.ResendCooldown, invitationKey},
		{s.invitationPolicy.ResendDaily, invitationKey},
		{s.invitationPolicy.ResendOrganization, strconv.FormatUint(uint64(organizationID), 10)},
	}
	for _, check := range checks {
		allowed, _, resetAt := check.store.Take(ctx, check.key)
		if !allowed {
			return &InvitationRetryAfterError{Err: ErrInvitationResendRateLimited, RetryAfter: time.Until(resetAt)}
		}
	}
	return nil
}

// sendInvitationEmail makes one synchronous send attempt and records its outcome on the
// invitation. A failure to record is logged; the attempt outcome is still returned.
func (s *service) sendInvitationEmail(
	ctx context.Context,
	invitation *domain.OrganizationInvitation,
	organizationName,
	token string,
) InvitationEmailSendStatus {
	status := InvitationEmailSendStatusNotConfigured
	if s.invitationMailer != nil && s.invitationMailer.IsConfigured() {
		status = InvitationEmailSendStatusAcceptedByProvider
		if sendErr := s.invitationMailer.SendInvitation(
			ctx,
			invitation.Email,
			organizationName,
			invitation.Role,
			token,
			invitation.ExpiresAt,
		); sendErr != nil {
			status = InvitationEmailSendStatusFailed
			slog.WarnContext(ctx, "organization.invitation_email_send_failed",
				"organization_id", invitation.OrganizationID,
				"invitation_id", invitation.ID,
				"error_type", fmt.Sprintf("%T", sendErr),
			)
		}
	}

	attemptedAt := s.now()
	if err := s.invitationRepo.RecordInvitationEmailAttempt(
		ctx, invitation.OrganizationID, invitation.ID, invitation.TokenHash, string(status), attemptedAt,
	); err != nil {
		slog.WarnContext(ctx, "organization.invitation_email_status_not_recorded",
			"organization_id", invitation.OrganizationID,
			"invitation_id", invitation.ID,
			"error_type", fmt.Sprintf("%T", err),
		)
		return status
	}
	invitation.EmailSendStatus = string(status)
	invitation.EmailAttemptedAt = &attemptedAt
	return status
}

func (s *service) ListInvitations(
	ctx context.Context,
	userID,
	organizationID uint,
	page,
	pageSize int,
) ([]*domain.OrganizationInvitation, int64, error) {
	if userID == 0 || organizationID == 0 || page < 1 || pageSize < 1 {
		return nil, 0, domain.ErrInvalidInput
	}
	if s.invitationRepo == nil {
		return nil, 0, domain.ErrServiceUnavailable
	}
	if _, err := s.invitationManager(ctx, userID, organizationID); err != nil {
		return nil, 0, err
	}
	return s.invitationRepo.ListInvitations(ctx, organizationID, page, pageSize)
}

func (s *service) RevokeInvitation(ctx context.Context, userID, organizationID, invitationID uint) error {
	if userID == 0 || organizationID == 0 || invitationID == 0 {
		return domain.ErrInvalidInput
	}
	if s.invitationRepo == nil {
		return domain.ErrServiceUnavailable
	}
	if _, err := s.invitationManager(ctx, userID, organizationID); err != nil {
		return err
	}
	invitation, err := s.invitationRepo.RevokeInvitation(ctx, organizationID, invitationID, userID, s.now())
	if err != nil {
		return err
	}
	s.audit(ctx, auditstarter.Change{
		Action:     "revoke",
		Resource:   "organization_invitations",
		TargetType: "organization_invitation",
		TargetID:   strconv.FormatUint(uint64(invitation.ID), 10),
		Result:     domain.AuditResultSuccess,
		Metadata: map[string]any{
			"organization_id": organizationID,
			"role":            invitation.Role,
		},
	})
	return nil
}

func (s *service) AcceptInvitation(
	ctx context.Context,
	userID uint,
	req *AcceptOrganizationInvitationRequest,
) (*domain.OrganizationMembership, error) {
	if userID == 0 || req == nil || len(req.validationErrors()) > 0 || len(req.Token) > 256 {
		return nil, domain.ErrInvalidInput
	}
	if s.invitationRepo == nil {
		return nil, domain.ErrServiceUnavailable
	}
	token := strings.TrimSpace(req.Token)
	membership, invitation, err := s.invitationRepo.AcceptInvitation(
		ctx,
		crypto.SHA256Hex(token),
		userID,
		s.now(),
	)
	if err != nil {
		return nil, err
	}

	s.audit(ctx, auditstarter.Change{
		Action:     "accept",
		Resource:   "organization_invitations",
		TargetType: "organization_invitation",
		TargetID:   strconv.FormatUint(uint64(invitation.ID), 10),
		Result:     domain.AuditResultSuccess,
		Metadata: map[string]any{
			"organization_id": invitation.OrganizationID,
			"user_id":         userID,
			"role":            membership.Role,
		},
	})
	s.publish(ctx, domain.NewOrganizationMemberJoinedEvent(invitation.OrganizationID, userID, membership.ID, s.now()))
	return membership, nil
}
