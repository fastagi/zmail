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

	auditstarter.RecordChange(ctx, auditstarter.Change{
		Action:     "invite",
		Resource:   "organization_invitations",
		TargetType: "organization_invitation",
		TargetID:   strconv.FormatUint(uint64(invitation.ID), 10),
		Result:     domain.AuditResultSuccess,
		Metadata: map[string]any{
			"organization_id":   organizationID,
			"role":              invitation.Role,
			"email_send_status": emailStatus,
		},
	})
	return &OrganizationInvitationResult{
		Invitation:      invitation,
		EmailSendStatus: emailStatus,
	}, nil
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
	auditstarter.RecordChange(ctx, auditstarter.Change{
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

	auditstarter.RecordChange(ctx, auditstarter.Change{
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

// ErrInvitationResendRateLimited refuses a resend that exceeds the per-invitation quota.
var ErrInvitationResendRateLimited = errors.New("organization invitation resend is rate limited")

// InvitationRetryAfterError carries when the refused resend may be tried again.
type InvitationRetryAfterError struct {
	Err        error
	RetryAfter time.Duration
}

func (e *InvitationRetryAfterError) Error() string { return e.Err.Error() }

func (e *InvitationRetryAfterError) Unwrap() error { return e.Err }

// sendInvitationEmail makes one synchronous attempt for the current token and records its outcome
// on the invitation. A recording failure is logged; the attempt itself already happened.
func (s *service) sendInvitationEmail(
	ctx context.Context,
	invitation *domain.OrganizationInvitation,
	organizationName string,
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
	recorded, err := s.invitationRepo.RecordInvitationEmailAttempt(
		context.WithoutCancel(ctx), invitation.ID, invitation.TokenHash, string(status), attemptedAt,
	)
	switch {
	case err != nil:
		slog.ErrorContext(ctx, "organization.invitation_email_status_record_failed",
			"organization_id", invitation.OrganizationID,
			"invitation_id", invitation.ID,
			"error_type", fmt.Sprintf("%T", err),
		)
	case recorded:
		invitation.EmailSendStatus = string(status)
		invitation.EmailAttemptedAt = &attemptedAt
	}
	return status
}

// takeResendQuota consumes one unit of both per-invitation quotas, or refuses with the longer wait.
func (s *service) takeResendQuota(ctx context.Context, invitationID uint) error {
	key := strconv.FormatUint(uint64(invitationID), 10)
	for _, store := range []ratelimit.Store{s.invitationPolicy.ResendCooldown, s.invitationPolicy.ResendDaily} {
		allowed, _, resetAt := store.Take(ctx, key)
		if !allowed {
			return &InvitationRetryAfterError{Err: ErrInvitationResendRateLimited, RetryAfter: time.Until(resetAt)}
		}
	}
	return nil
}

// ResendInvitation emails a pending invitation again. Only token hashes are stored, so a resend
// through a configured mailer issues a new token and the earlier one stops working. Without a
// mailer the token is kept and the attempt is recorded as not_configured. The expiry is unchanged.
func (s *service) ResendInvitation(
	ctx context.Context,
	userID,
	organizationID,
	invitationID uint,
) (*OrganizationInvitationResult, error) {
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
	// Validate before spending quota so a stale row does not lock the manager out.
	if _, err = s.invitationRepo.PrepareInvitationResend(ctx, organizationID, invitationID, "", now); err != nil {
		return nil, err
	}
	if err = s.takeResendQuota(ctx, invitationID); err != nil {
		return nil, err
	}

	token := ""
	tokenHash := ""
	if s.invitationMailer != nil && s.invitationMailer.IsConfigured() {
		if token, err = generateOrganizationInvitationToken(); err != nil {
			return nil, err
		}
		tokenHash = crypto.SHA256Hex(token)
	}
	invitation, err := s.invitationRepo.PrepareInvitationResend(ctx, organizationID, invitationID, tokenHash, now)
	if err != nil {
		return nil, err
	}
	emailStatus := s.sendInvitationEmail(ctx, invitation, manager.Organization.Name, token)

	auditstarter.RecordChange(ctx, auditstarter.Change{
		Action:     "resend",
		Resource:   "organization_invitations",
		TargetType: "organization_invitation",
		TargetID:   strconv.FormatUint(uint64(invitation.ID), 10),
		Result:     domain.AuditResultSuccess,
		Metadata: map[string]any{
			"organization_id":   organizationID,
			"role":              invitation.Role,
			"email_send_status": emailStatus,
			"token_rotated":     tokenHash != "",
		},
	})
	return &OrganizationInvitationResult{Invitation: invitation, EmailSendStatus: emailStatus}, nil
}
