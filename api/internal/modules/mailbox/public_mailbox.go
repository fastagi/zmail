package mailbox

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

const (
	// publicMailboxNewPeriod is how long a shared-domain mailbox uses the stricter new-mailbox
	// recipient and daily send caps.
	publicMailboxNewPeriod = 7 * 24 * time.Hour

	publicMailboxBurstMax    = 2
	publicMailboxBurstWindow = time.Minute
	rulePublicMailboxBurst   = "mail.public_mailbox.burst"

	publicMailboxAvailabilityMax    = 30
	publicMailboxAvailabilityWindow = time.Minute
	rulePublicMailboxAvailability   = "mail.public_mailbox.availability"

	publicMailboxClaimMax    = 5
	publicMailboxClaimWindow = time.Hour
	rulePublicMailboxClaim   = "mail.public_mailbox.claim"

	maxPublicMailboxSuggestions = 3
)

// Public mailbox availability statuses.
const (
	PublicMailboxStatusAvailable = "available"
	PublicMailboxStatusTaken     = "taken"
	PublicMailboxStatusReserved  = "reserved"
	PublicMailboxStatusInvalid   = "invalid"
	PublicMailboxStatusTooShort  = "too_short"
)

// PublicMailboxOptions describes the self-serve public mailbox policy and the user's current claim.
type PublicMailboxOptions struct {
	Enabled               bool
	Domains               []string
	MinLocalLength        int
	LimitPerUser          int
	ClaimedCount          int
	ClaimedAddress        string
	ClaimedOrganizationID *uint
	ClaimedMailboxID      *uint
	DailySendLimitNew     int
	DailySendLimit        int
	MaxRecipientsNew      int
	MaxRecipients         int
}

// PublicMailboxAvailability is the result of checking one desired public address.
type PublicMailboxAvailability struct {
	Address     string
	LocalPart   string
	Domain      string
	Status      string
	Suggestions []string
}

// ClaimPublicMailboxInput asks to claim one free personal mailbox on a public domain.
type ClaimPublicMailboxInput struct {
	LocalPart   string
	Domain      string
	DisplayName string
}

// publicMailboxLimits bounds public mailbox probing, claiming, and outbound send bursts.
type publicMailboxLimits struct {
	burst        ratelimit.Store
	availability ratelimit.Store
	claim        ratelimit.Store
}

func newPublicMailboxLimits(stores *ratelimit.Stores) publicMailboxLimits {
	if stores == nil {
		stores = ratelimit.NewMemoryStores()
	}
	return publicMailboxLimits{
		burst: stores.New(ratelimit.Rule{
			Name: rulePublicMailboxBurst, Max: publicMailboxBurstMax, Window: publicMailboxBurstWindow,
		}),
		availability: stores.New(ratelimit.Rule{
			Name: rulePublicMailboxAvailability, Max: publicMailboxAvailabilityMax, Window: publicMailboxAvailabilityWindow,
		}),
		claim: stores.New(ratelimit.Rule{
			Name: rulePublicMailboxClaim, Max: publicMailboxClaimMax, Window: publicMailboxClaimWindow,
		}),
	}
}

// enforcePublicDomainGuardrails reports whether production shared-domain anti-abuse limits apply
// to mailbox: enabled when custom domains are active and the mailbox is a Zmail address on a
// shared/public domain. Local/dev tests without CustomDomainsEnabled stay unaffected.
func (s *service) enforcePublicDomainGuardrails(mailbox *domain.Mailbox) bool {
	return mailbox != nil && !mailbox.Connected() && s.settings.CustomDomainsEnabled &&
		s.isSharedDomain(addressDomain(mailbox.Address))
}

func (s *service) isNewPublicMailbox(mailbox *domain.Mailbox) bool {
	return mailbox.CreatedAt.IsZero() || s.now().Sub(mailbox.CreatedAt) < publicMailboxNewPeriod
}

func (s *service) effectiveMaxRecipients(mailbox *domain.Mailbox) int {
	if !s.enforcePublicDomainGuardrails(mailbox) {
		return domain.MaxMailRecipients
	}
	if s.isNewPublicMailbox(mailbox) {
		return min(domain.MaxMailRecipients, s.settings.publicMaxRecipientsNew())
	}
	return min(domain.MaxMailRecipients, s.settings.publicMaxRecipients())
}

func (s *service) effectiveSendLimit(mailbox *domain.Mailbox, laneLimit int) int {
	if !s.enforcePublicDomainGuardrails(mailbox) {
		return laneLimit
	}
	sharedLimit := s.settings.publicDailySendLimit()
	if s.isNewPublicMailbox(mailbox) {
		sharedLimit = s.settings.publicDailySendLimitNew()
	}
	return min(laneLimit, sharedLimit)
}

func (s *service) checkPublicSendBurst(ctx context.Context, mailbox *domain.Mailbox) error {
	if !s.enforcePublicDomainGuardrails(mailbox) || s.publicLimits.burst == nil {
		return nil
	}
	bucket := "mailbox:" + strconv.FormatUint(uint64(mailbox.ID), 10)
	if allowed, _, resetAt := s.publicLimits.burst.Take(ctx, bucket); !allowed {
		return &RetryAfterError{Err: ErrMailSendLimitReached, RetryAfter: time.Until(resetAt)}
	}
	return nil
}

// PublicMailboxOptions returns the public mailbox policy and the authenticated user's global claim state.
func (s *service) PublicMailboxOptions(ctx context.Context, userID uint) (*PublicMailboxOptions, error) {
	domains := s.settings.publicDomains()
	limit := s.settings.publicLimitPerUser()
	options := &PublicMailboxOptions{
		Enabled:           len(domains) > 0 && limit > 0,
		Domains:           domains,
		MinLocalLength:    s.settings.publicMinLocalLen(),
		LimitPerUser:      limit,
		DailySendLimitNew: s.settings.publicDailySendLimitNew(),
		DailySendLimit:    s.settings.publicDailySendLimit(),
		MaxRecipientsNew:  s.settings.publicMaxRecipientsNew(),
		MaxRecipients:     s.settings.publicMaxRecipients(),
	}
	if s.publicMailboxes == nil || userID == 0 || len(domains) == 0 {
		return options, nil
	}
	claims, err := s.publicMailboxes.ListUserPublicMailboxes(ctx, userID, s.sharedDomains())
	if err != nil {
		return nil, err
	}
	options.ClaimedCount = len(claims)
	for _, claim := range claims {
		if claim.Deleted {
			continue
		}
		options.ClaimedAddress = claim.Address
		if claim.OrganizationID != 0 {
			orgID := claim.OrganizationID
			options.ClaimedOrganizationID = &orgID
		}
		mailboxID := claim.ID
		options.ClaimedMailboxID = &mailboxID
		break
	}
	return options, nil
}

// CheckPublicMailboxAvailability checks whether a public address can be claimed and suggests up to
// 3 free alternatives when it is taken, reserved, or too short. Never reveals owner metadata.
func (s *service) CheckPublicMailboxAvailability(ctx context.Context, userID uint, rawLocalPart, rawDomain string) (*PublicMailboxAvailability, error) {
	if s.publicLimits.availability != nil && userID != 0 {
		bucket := "user:" + strconv.FormatUint(uint64(userID), 10)
		if allowed, _, resetAt := s.publicLimits.availability.Take(ctx, bucket); !allowed {
			return nil, &RetryAfterError{Err: ErrPublicMailboxRateLimited, RetryAfter: time.Until(resetAt)}
		}
	}
	domainName, err := s.resolvePublicDomain(rawDomain)
	if err != nil {
		return nil, err
	}
	localPart := strings.ToLower(strings.TrimSpace(rawLocalPart))
	address := ""
	if localPart != "" {
		address = localPart + "@" + domainName
	}
	result := &PublicMailboxAvailability{
		Address:     address,
		LocalPart:   localPart,
		Domain:      domainName,
		Suggestions: []string{},
	}
	if localPart == "" || !localPartPattern.MatchString(localPart) || strings.Contains(localPart, "..") {
		result.Status = PublicMailboxStatusInvalid
		return result, nil
	}
	minLen := s.settings.publicMinLocalLen()
	switch {
	case len(localPart) < minLen:
		result.Status = PublicMailboxStatusTooShort
	case isPublicReservedLocalPart(localPart):
		result.Status = PublicMailboxStatusReserved
	default:
		err := s.ensureAddressFree(ctx, address)
		switch {
		case err == nil:
			result.Status = PublicMailboxStatusAvailable
			return result, nil
		case errors.Is(err, ErrMailboxAddressTaken):
			result.Status = PublicMailboxStatusTaken
		default:
			return nil, err
		}
	}
	suggestions, err := s.suggestPublicAddresses(ctx, localPart, domainName, minLen)
	if err != nil {
		return nil, err
	}
	result.Suggestions = suggestions
	return result, nil
}

func (s *service) resolvePublicDomain(rawDomain string) (string, error) {
	domains := s.settings.publicDomains()
	if len(domains) == 0 {
		return "", ErrMailDomainsDisabled
	}
	name := normalizeSettingDomain(rawDomain)
	if name == "" {
		return domains[0], nil
	}
	if !slices.Contains(domains, name) {
		return "", ErrMailDomainNotFound
	}
	return name, nil
}

var publicSuggestionSuffixes = []string{
	"88", ".mail", "2026", ".im", "99", "_me", "01", ".inbox", "77", "2027", "hq", "now",
}

func (s *service) suggestPublicAddresses(ctx context.Context, localPart, domainName string, minLen int) ([]string, error) {
	base := strings.Trim(localPart, "._-")
	if base == "" {
		return []string{}, nil
	}
	suggestions := make([]string, 0, maxPublicMailboxSuggestions)
	seen := map[string]struct{}{localPart: {}}
	for _, suffix := range publicSuggestionSuffixes {
		if len(suggestions) >= maxPublicMailboxSuggestions {
			break
		}
		candidate := base + suffix
		if len(candidate) > maxLocalPartLength {
			trimTo := maxLocalPartLength - len(suffix)
			if trimTo <= 0 {
				continue
			}
			candidate = strings.TrimRight(base[:trimTo], "._-") + suffix
		}
		if len(candidate) < minLen || !localPartPattern.MatchString(candidate) || strings.Contains(candidate, "..") {
			continue
		}
		if isPublicReservedLocalPart(candidate) {
			continue
		}
		if _, dup := seen[candidate]; dup {
			continue
		}
		seen[candidate] = struct{}{}
		address := candidate + "@" + domainName
		if err := s.ensureAddressFree(ctx, address); err == nil {
			suggestions = append(suggestions, address)
		} else if !errors.Is(err, ErrMailboxAddressTaken) {
			return nil, err
		}
	}
	return suggestions, nil
}

// ClaimPublicMailbox claims a free personal mailbox on a configured public domain for the viewer,
// enforcing reserved handles, minimum length, rate limiting, and the global per-user claim cap
// across all organizations (including deleted tombstones), and stores a local Welcome email.
func (s *service) ClaimPublicMailbox(ctx context.Context, viewer domain.OrganizationContext, input ClaimPublicMailboxInput) (*domain.Mailbox, error) {
	if !viewer.Role.CanManageOrganization() {
		return nil, domain.ErrPermissionDenied
	}
	domains := s.settings.publicDomains()
	limit := s.settings.publicLimitPerUser()
	if len(domains) == 0 || limit <= 0 {
		return nil, ErrMailDomainsDisabled
	}
	if s.publicLimits.claim != nil {
		bucket := "user:" + strconv.FormatUint(uint64(viewer.UserID), 10)
		if allowed, _, resetAt := s.publicLimits.claim.Take(ctx, bucket); !allowed {
			return nil, &RetryAfterError{Err: ErrPublicMailboxRateLimited, RetryAfter: time.Until(resetAt)}
		}
	}
	domainName, err := s.resolvePublicDomain(input.Domain)
	if err != nil {
		return nil, err
	}
	localPart, err := normalizeLocalPart(input.LocalPart)
	if err != nil {
		return nil, err
	}
	minLen := s.settings.publicMinLocalLen()
	if len(localPart) < minLen {
		return nil, fmt.Errorf("%w: must be at least %d characters", ErrMailboxAddressInvalid, minLen)
	}
	if isPublicReservedLocalPart(localPart) {
		return nil, ErrMailboxAddressReserved
	}
	displayName, err := normalizeDisplayName(input.DisplayName)
	if err != nil {
		return nil, err
	}
	owner, err := s.currentMembership(ctx, viewer.OrganizationID, viewer.UserID)
	if err != nil {
		return nil, err
	}
	if err := s.checkPublicClaimCap(ctx, viewer.UserID, limit); err != nil {
		return nil, err
	}
	addressDomain, connected, err := s.mailboxDomainFor(ctx, viewer.OrganizationID, domainName)
	if err != nil {
		return nil, err
	}
	ownerID := owner.UserID
	mailbox := &domain.Mailbox{
		OrganizationID: viewer.OrganizationID,
		CreatedBy:      viewer.UserID,
		Address:        localPart + "@" + addressDomain,
		DisplayName:    displayName,
		Type:           domain.MailboxTypePersonal,
		OwnerUserID:    &ownerID,
	}
	if err := s.ensureAddressFree(ctx, mailbox.Address); err != nil {
		return nil, err
	}
	if connected != nil && receives(connected) {
		provider, worker, clientErr := s.domainClient(ctx, connected)
		if clientErr != nil {
			return nil, clientErr
		}
		ruleID, ruleErr := provider.CreateRoutingRule(ctx, connected.ZoneID, mailbox.Address, worker)
		if ruleErr != nil {
			return nil, providerError(ruleErr)
		}
		mailbox.RoutingRuleID = ruleID
	}
	err = s.repo.InTransaction(ctx, func(tx context.Context) error {
		if s.publicMailboxes != nil {
			if lockErr := s.publicMailboxes.LockPublicMailboxClaim(tx, viewer.UserID); lockErr != nil {
				return lockErr
			}
			if capErr := s.checkPublicClaimCap(tx, viewer.UserID, limit); capErr != nil {
				return capErr
			}
		}
		if createErr := s.repo.CreateMailbox(tx, mailbox, s.MaxMailboxes()); createErr != nil {
			return createErr
		}
		return s.recordAccess(tx, mailbox, domain.MailboxAccessCreated, viewer.UserID, ownerID, map[string]any{
			"type":         string(mailbox.Type),
			"public_claim": true,
		})
	})
	if err != nil {
		if connected != nil {
			s.removeRoutingRule(ctx, mailbox)
		}
		return nil, err
	}
	if mailbox.Access, err = s.accessFor(ctx, viewer, mailbox); err != nil {
		return nil, err
	}
	s.deliverPublicWelcomeMessage(ctx, mailbox, addressDomain)
	return mailbox, nil
}

func (s *service) checkPublicClaimCap(ctx context.Context, userID uint, limit int) error {
	if s.publicMailboxes == nil {
		return nil
	}
	claims, err := s.publicMailboxes.ListUserPublicMailboxes(ctx, userID, s.sharedDomains())
	if err != nil {
		return err
	}
	if len(claims) >= limit {
		return ErrPublicMailboxLimitReached
	}
	return nil
}

// deliverPublicWelcomeMessage inserts a local inbound welcome message directly into the claimed
// mailbox without external SMTP. Best-effort: failures are logged and never roll back the claim.
func (s *service) deliverPublicWelcomeMessage(ctx context.Context, mailbox *domain.Mailbox, domainName string) {
	fromAddress := "noreply@" + domainName
	if domainName == "zmail.im" {
		fromAddress = "notify@zmail.im"
	}
	greeting := "Welcome to Zmail"
	if mailbox.DisplayName != "" {
		greeting = "Welcome to Zmail, " + mailbox.DisplayName
	}
	subject := fmt.Sprintf("Welcome to your new Zmail address (%s)", mailbox.Address)
	textBody := fmt.Sprintf(
		"%s!\n\n"+
			"Your new address %s is active and ready to send and receive email.\n\n"+
			"Tips to get started:\n"+
			"- Send yourself a test email or share %s with friends and services.\n"+
			"- Connect your own custom domain anytime for higher sending limits, team mailboxes, and API access.\n\n"+
			"— The Zmail Team\n",
		greeting, mailbox.Address, mailbox.Address,
	)
	escapedAddress := html.EscapeString(mailbox.Address)
	escapedGreeting := html.EscapeString(greeting)
	htmlBody := fmt.Sprintf(
		`<div style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;max-width:560px;margin:0 auto;padding:24px;color:#18181b;line-height:1.6;">`+
			`<h2 style="margin:0 0 12px;font-size:20px;">%s!</h2>`+
			`<p style="margin:0 0 16px;">Your new address <strong>%s</strong> is active and ready to send and receive email.</p>`+
			`<ul style="margin:0 0 16px;padding-left:20px;">`+
			`<li>Send a test email or start using <strong>%s</strong> right away.</li>`+
			`<li>Connect your own custom domain anytime for higher sending limits, team mailboxes, and API access.</li>`+
			`</ul>`+
			`<p style="margin:0;color:#71717a;font-size:13px;">— The Zmail Team</p>`+
			`</div>`,
		escapedGreeting, escapedAddress, escapedAddress,
	)
	rfcID := newRFCMessageID(domainName)
	welcome := &domain.MailMessage{
		MailboxID:    mailbox.ID,
		Direction:    domain.MailDirectionInbound,
		Status:       domain.MailStatusReceived,
		FromAddress:  fromAddress,
		FromName:     "Zmail",
		To:           []string{mailbox.Address},
		Subject:      subject,
		TextBody:     textBody,
		HTMLBody:     htmlBody,
		MessageID:    rfcID,
		RFCMessageID: rfcID,
		DedupeKey:    inboundDedupeKey("public-welcome:"+strconv.FormatUint(uint64(mailbox.ID), 10), nil),
		Placement:    domain.MailPlacementDefault,
		Source:       domain.MailSourceZmail,
	}
	if created, err := s.repo.CreateInboundMessage(ctx, welcome); err != nil {
		slog.WarnContext(ctx, "public mailbox welcome message was not stored",
			slog.Uint64("mailbox_id", uint64(mailbox.ID)), slog.String("error", err.Error()))
	} else if created {
		mailbox.UnreadCount = 1
	}
}
