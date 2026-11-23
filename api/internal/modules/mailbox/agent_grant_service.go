package mailbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/ratelimit"
)

// Access history entries of agent grants.
const (
	MailboxAccessAgentGranted domain.MailboxAccessEventType = "agent_granted"
	MailboxAccessAgentRevoked domain.MailboxAccessEventType = "agent_revoked"
)

// agentEventPruneBatch is how many feed events one prune statement deletes.
const agentEventPruneBatch = 1000

// ruleMailAgentReads names the per-key limit of the agent routes.
const ruleMailAgentReads = "mail_agent_reads"

// ErrMailAgentRateLimited means an API key made too many agent requests in a minute.
var ErrMailAgentRateLimited = errors.New("too many agent mail requests")

func newAgentReadLimit(stores *ratelimit.Stores, settings Settings) ratelimit.Store {
	if stores == nil {
		stores = ratelimit.NewMemoryStores()
	}
	return stores.New(ratelimit.Rule{
		Name: ruleMailAgentReads, Max: settings.MailAgent.Resolved().ReadsPerMinute, Window: time.Minute,
	})
}

// takeAgentRead spends one request of the key's per-minute allowance.
func (s *service) takeAgentRead(ctx context.Context, keyID uint) error {
	if s.agentReads == nil {
		return nil
	}
	allowed, _, resetAt := s.agentReads.Take(ctx, fmt.Sprintf("key:%d", keyID))
	if allowed {
		return nil
	}
	return &RetryAfterError{Err: ErrMailAgentRateLimited, RetryAfter: time.Until(resetAt)}
}

// AgentGrantService is agent grants as people manage them and as agents use them.
type AgentGrantService interface {
	// IssueAgentGrant lets an API key receive a mailbox's new mail. Only a personal or connected
	// mailbox's owner, or a shared mailbox's member with manage, may.
	IssueAgentGrant(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, input IssueAgentGrantInput) (*domain.MailAgentGrant, error)
	// ListAgentGrants pages a mailbox's grants for the people who may issue them.
	ListAgentGrants(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, page, pageSize int) ([]*domain.MailAgentGrant, int64, error)
	// RevokeAgentGrant ends a grant at once; revoking an ended grant changes nothing.
	RevokeAgentGrant(ctx context.Context, viewer domain.OrganizationContext, mailboxID, grantID uint) error
	// AgentGrantActivity reads a grant's newest audit rows.
	AgentGrantActivity(ctx context.Context, viewer domain.OrganizationContext, mailboxID, grantID uint) ([]*domain.MailAgentAuditEntry, error)
	// ListAgentDrafts pages a mailbox's agent drafts for the people who may send from it.
	ListAgentDrafts(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, page, pageSize int) ([]*domain.MailAgentDraft, int64, error)

	// AgentMailEvents reads the feed of the key's effective grants after a cursor.
	AgentMailEvents(ctx context.Context, viewer domain.OrganizationContext, keyID, after uint, limit int) (*AgentMailFeed, error)
	// AgentMailMessage reads one message an effective grant of the key received, and audits it.
	AgentMailMessage(ctx context.Context, viewer domain.OrganizationContext, keyID, messageID uint) (*AgentMailRead, error)
	// SaveAgentDraft stores a reply draft through an effective grant with the draft capability.
	SaveAgentDraft(ctx context.Context, viewer domain.OrganizationContext, keyID uint, input AgentDraftInput) (*domain.MailAgentDraft, bool, error)
}

var (
	_ AgentGrantService           = (*service)(nil)
	_ domain.MailAgentEventPruner = (*service)(nil)
)

// IssueAgentGrantInput is a grant request.
type IssueAgentGrantInput struct {
	APIKeyID      uint
	Capabilities  []string
	RevealSecrets bool
	Senders       []string
	LabelIDs      []uint
	ExpiresAt     time.Time
}

// AgentMailFeed is one page of an agent's feed.
type AgentMailFeed struct {
	Events     []*domain.MailAgentEvent
	NextCursor uint
	HasMore    bool
}

// AgentMailRead is a message as an agent reads it.
type AgentMailRead struct {
	Message *domain.MailMessage
	// Revealed is true when the grant allows unmasked codes and links.
	Revealed bool
}

// AgentDraftInput is an agent's reply draft.
type AgentDraftInput struct {
	MessageID uint
	Text      string
	HTML      string
}

func (s *service) agentsAvailable() error {
	if s.agents == nil {
		return domain.ErrServiceUnavailable
	}
	return nil
}

// agentIssuerMailbox loads a mailbox for someone who issues, lists, or revokes its grants: hidden
// without any access, forbidden unless the viewer owns the personal or connected mailbox or holds
// manage as a member of the shared one. An organization role alone never qualifies.
func (s *service) agentIssuerMailbox(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) (*domain.Mailbox, error) {
	if err := s.agentsAvailable(); err != nil {
		return nil, err
	}
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, nil)
	if err != nil {
		return nil, err
	}
	if mailbox.Type == domain.MailboxTypeShared {
		member, err := s.repo.FindMailboxMember(ctx, mailbox.ID, viewer.MembershipID)
		if err != nil {
			return nil, err
		}
		if member == nil || member.UserID != viewer.UserID || !member.Permission.Includes(domain.MailboxPermissionManage) {
			return nil, domain.ErrPermissionDenied
		}
		return mailbox, nil
	}
	if mailbox.OwnerUserID == nil || *mailbox.OwnerUserID != viewer.UserID {
		return nil, domain.ErrPermissionDenied
	}
	return mailbox, nil
}

func agentGrantInvalid(reason string) error {
	return fmt.Errorf("%w: %s", ErrMailAgentGrantInvalid, reason)
}

// normalizeAgentSenders lowercases, checks, and deduplicates sender filters: an address with @ or
// a domain; a leading @ marks a domain too.
func normalizeAgentSenders(values []string) ([]string, error) {
	if len(values) > domain.MaxMailAgentGrantFilters {
		return nil, agentGrantInvalid(fmt.Sprintf("at most %d sender filters", domain.MaxMailAgentGrantFilters))
	}
	seen := map[string]bool{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "@")
		local, host, isAddress := strings.Cut(value, "@")
		if isAddress && local == "" {
			return nil, agentGrantInvalid("a sender filter is an address or a domain")
		}
		if !isAddress {
			host = value
		}
		if len(value) > 320 || !validAgentFilterDomain(host) || strings.ContainsAny(local, " \t\r\n<>,;\"") {
			return nil, agentGrantInvalid("a sender filter is an address or a domain")
		}
		if !seen[value] {
			seen[value] = true
			normalized = append(normalized, value)
		}
	}
	return normalized, nil
}

func validAgentFilterDomain(host string) bool {
	if host == "" || len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return false
	}
	for _, char := range host {
		isLetter, isDigit := char >= 'a' && char <= 'z', char >= '0' && char <= '9'
		if !isLetter && !isDigit && char != '-' && char != '.' {
			return false
		}
	}
	return true
}

func (s *service) IssueAgentGrant(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, input IssueAgentGrantInput) (*domain.MailAgentGrant, error) {
	mailbox, err := s.agentIssuerMailbox(ctx, viewer, mailboxID)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	canDraft, hasRead := false, false
	for _, capability := range input.Capabilities {
		switch capability {
		case "read":
			hasRead = true
		case "draft":
			canDraft = true
		default:
			return nil, agentGrantInvalid("capabilities are read and draft")
		}
	}
	if !hasRead {
		return nil, agentGrantInvalid("capabilities must include read")
	}
	if canDraft && mailbox.Connected() {
		return nil, agentGrantInvalid("a connected mailbox is read-only: nobody sends from it, so draft is not available")
	}
	expiresAt := input.ExpiresAt.UTC()
	if !expiresAt.After(now) || expiresAt.After(now.Add(domain.MaxMailAgentGrantLifetime)) {
		return nil, agentGrantInvalid("expires_at must be in the future and at most 90 days ahead")
	}
	senders, err := normalizeAgentSenders(input.Senders)
	if err != nil {
		return nil, err
	}
	labelIDs, err := s.agentLabelFilters(ctx, mailbox.ID, input.LabelIDs)
	if err != nil {
		return nil, err
	}
	key, err := s.checkAgentKey(ctx, viewer, mailbox, input.APIKeyID, canDraft, now)
	if err != nil {
		return nil, err
	}
	keyUserID := key.UserID
	grant := &domain.MailAgentGrant{
		OrganizationID: viewer.OrganizationID, MailboxID: mailbox.ID, APIKeyID: input.APIKeyID, KeyUserID: keyUserID,
		GrantedBy: viewer.UserID, CanDraft: canDraft, RevealSecrets: input.RevealSecrets,
		SenderFilters: senders, LabelIDs: labelIDs, ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now,
		APIKeyName: key.Name, APIKeyPrefix: key.KeyPrefix,
	}
	err = s.repo.InTransaction(ctx, func(tx context.Context) error {
		if createErr := s.agents.CreateAgentGrant(tx, grant, domain.MaxLiveMailAgentGrantsPerMailbox); createErr != nil {
			return createErr
		}
		return s.recordAccess(tx, mailbox, MailboxAccessAgentGranted, viewer.UserID, keyUserID, map[string]any{
			"grant_id":       grant.ID,
			"api_key_id":     grant.APIKeyID,
			"capabilities":   grant.Capabilities(),
			"reveal_secrets": grant.RevealSecrets,
			"expires_at":     grant.ExpiresAt.Format(time.RFC3339),
			"sender_filters": len(grant.SenderFilters),
			"label_filters":  len(grant.LabelIDs),
		})
	})
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "mail.agent_granted",
		slog.Uint64("organization_id", uint64(grant.OrganizationID)), slog.Uint64("mailbox_id", uint64(grant.MailboxID)),
		slog.Uint64("grant_id", uint64(grant.ID)), slog.Uint64("api_key_id", uint64(grant.APIKeyID)),
		slog.Bool("draft", grant.CanDraft), slog.Bool("reveal_secrets", grant.RevealSecrets))
	return grant, nil
}

// agentLabelFilters checks that every label filter names a label of the mailbox.
func (s *service) agentLabelFilters(ctx context.Context, mailboxID uint, ids []uint) ([]uint, error) {
	if len(ids) == 0 {
		return []uint{}, nil
	}
	if len(ids) > domain.MaxMailAgentGrantFilters {
		return nil, agentGrantInvalid(fmt.Sprintf("at most %d label filters", domain.MaxMailAgentGrantFilters))
	}
	if s.organize == nil {
		return nil, agentGrantInvalid("label filters are not available")
	}
	labels, err := s.organize.ListLabels(ctx, mailboxID)
	if err != nil {
		return nil, err
	}
	known := make(map[uint]bool, len(labels))
	for _, label := range labels {
		known[label.ID] = true
	}
	seen := map[uint]bool{}
	normalized := make([]uint, 0, len(ids))
	for _, id := range ids {
		if !known[id] {
			return nil, agentGrantInvalid("label_ids must be labels of the mailbox")
		}
		if !seen[id] {
			seen[id] = true
			normalized = append(normalized, id)
		}
	}
	return normalized, nil
}

// errForeignAgentKey is the one answer about a key of someone other than the issuer, whatever is
// wrong with it: unknown, another organization's, inactive, a test key, without the scope, or its
// owner unable to read the mailbox. Only the issuer's own keys get a reason.
var errForeignAgentKey = agentGrantInvalid("api_key_id must name an active mail:agent key of a member who can read this mailbox")

// checkAgentKey accepts a live, unexpired, unrevoked key with the mail:agent scope whose owner is
// a current member who can read the mailbox, and send from it when the grant drafts. The key is
// considered only once its owner is known to be a member of the issuer's organization, and a key
// of anyone else than the issuer fails with one undifferentiated error. It returns the key.
func (s *service) checkAgentKey(ctx context.Context, issuer domain.OrganizationContext, mailbox *domain.Mailbox, keyID uint, canDraft bool, now time.Time) (*domain.APIKey, error) {
	if keyID == 0 {
		return nil, agentGrantInvalid("api_key_id is required")
	}
	key, err := s.agents.FindAgentKey(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if key == nil {
		return nil, errForeignAgentKey
	}
	own := key.UserID == issuer.UserID
	refuse := func(reason string) error {
		if own {
			return agentGrantInvalid(reason)
		}
		return errForeignAgentKey
	}
	owner, err := s.agentKeyOwner(ctx, issuer.OrganizationID, key.UserID)
	if err != nil {
		return nil, err
	}
	if owner == nil {
		return nil, errForeignAgentKey
	}
	if !key.IsActive(now) {
		return nil, refuse("api_key_id must name an active API key")
	}
	if key.Mode == domain.APIKeyModeTest {
		return nil, refuse("a test API key cannot hold an agent grant")
	}
	if !key.HasScope(ScopeMailAgent) {
		return nil, refuse("the API key needs the mail:agent scope")
	}
	access, err := s.accessFor(ctx, *owner, mailbox)
	if err != nil {
		return nil, err
	}
	if !access.View {
		return nil, refuse("the API key's owner cannot read this mailbox")
	}
	if canDraft && !access.Send {
		return nil, refuse("the draft capability needs an API key whose owner can send from this mailbox")
	}
	return key, nil
}

// agentKeyOwner is the key owner's current membership as a viewer; nil when they are not a member.
func (s *service) agentKeyOwner(ctx context.Context, organizationID, userID uint) (*domain.OrganizationContext, error) {
	membership, err := s.memberships.FindForUser(ctx, organizationID, userID)
	if errors.Is(err, domain.ErrOrganizationNotFound) || (err == nil && membership == nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &domain.OrganizationContext{
		OrganizationID: organizationID, MembershipID: membership.ID, UserID: userID, Role: membership.Role,
	}, nil
}

func (s *service) ListAgentGrants(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, page, pageSize int) ([]*domain.MailAgentGrant, int64, error) {
	mailbox, err := s.agentIssuerMailbox(ctx, viewer, mailboxID)
	if err != nil {
		return nil, 0, err
	}
	return s.agents.ListAgentGrants(ctx, mailbox.ID, mailbox.SealedBefore, page, pageSize)
}

func (s *service) RevokeAgentGrant(ctx context.Context, viewer domain.OrganizationContext, mailboxID, grantID uint) error {
	mailbox, err := s.agentIssuerMailbox(ctx, viewer, mailboxID)
	if err != nil {
		return err
	}
	if _, err := s.agents.FindAgentGrant(ctx, mailbox.ID, grantID, mailbox.SealedBefore); err != nil {
		return err
	}
	return s.repo.InTransaction(ctx, func(tx context.Context) error {
		return s.revokeAgentGrants(tx, domain.MailAgentGrantRevocation{
			OrganizationID: mailbox.OrganizationID, MailboxID: mailbox.ID, GrantID: grantID,
			Reason: domain.MailAgentRevokedByPerson, RevokedBy: viewer.UserID,
		})
	})
}

// revokeAgentGrants revokes the selected live grants and records each in its mailbox's access
// history. It is a no-op without the agent store, so callers in other flows need no check.
func (s *service) revokeAgentGrants(ctx context.Context, revocation domain.MailAgentGrantRevocation) error {
	if s.agents == nil {
		return nil
	}
	if revocation.At.IsZero() {
		revocation.At = s.now().UTC()
	}
	revoked, err := s.agents.RevokeAgentGrants(ctx, revocation)
	if err != nil {
		return err
	}
	for _, grant := range revoked {
		mailbox := &domain.Mailbox{ID: grant.MailboxID, OrganizationID: grant.OrganizationID}
		if err := s.recordAccess(ctx, mailbox, MailboxAccessAgentRevoked, revocation.RevokedBy, grant.KeyUserID, map[string]any{
			"grant_id":   grant.ID,
			"api_key_id": grant.APIKeyID,
			"reason":     string(revocation.Reason),
		}); err != nil {
			return err
		}
		slog.InfoContext(ctx, "mail.agent_revoked",
			slog.Uint64("organization_id", uint64(grant.OrganizationID)), slog.Uint64("mailbox_id", uint64(grant.MailboxID)),
			slog.Uint64("grant_id", uint64(grant.ID)), slog.String("reason", string(revocation.Reason)))
	}
	return nil
}

func (s *service) AgentGrantActivity(ctx context.Context, viewer domain.OrganizationContext, mailboxID, grantID uint) ([]*domain.MailAgentAuditEntry, error) {
	mailbox, err := s.agentIssuerMailbox(ctx, viewer, mailboxID)
	if err != nil {
		return nil, err
	}
	if _, err := s.agents.FindAgentGrant(ctx, mailbox.ID, grantID, mailbox.SealedBefore); err != nil {
		return nil, err
	}
	return s.agents.ListAgentAudit(ctx, grantID, mailbox.SealedBefore, domain.MaxMailAgentActivity)
}

func (s *service) ListAgentDrafts(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, page, pageSize int) ([]*domain.MailAgentDraft, int64, error) {
	if err := s.agentsAvailable(); err != nil {
		return nil, 0, err
	}
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, canSend)
	if err != nil {
		return nil, 0, err
	}
	return s.agents.ListAgentDrafts(ctx, mailbox.ID, page, pageSize)
}

// effectiveGrant is a grant that works right now, with its mailbox and the key owner's access.
type effectiveGrant struct {
	grant   *domain.MailAgentGrant
	mailbox *domain.Mailbox
}

// effectiveAgentGrants is the one check every agent request passes: the key's live grants in the
// viewer's organization whose mailbox still exists and which the key's owner, the viewer, can
// still read. Membership and key validity were already checked by the middleware.
func (s *service) effectiveAgentGrants(ctx context.Context, viewer domain.OrganizationContext, keyID uint) ([]effectiveGrant, error) {
	if err := s.agentsAvailable(); err != nil {
		return nil, err
	}
	grants, err := s.agents.ListLiveAgentGrantsOfKey(ctx, viewer.OrganizationID, keyID, s.now().UTC())
	if err != nil {
		return nil, err
	}
	mailboxes := map[uint]*domain.Mailbox{}
	effective := make([]effectiveGrant, 0, len(grants))
	for _, grant := range grants {
		if grant.KeyUserID != viewer.UserID {
			continue
		}
		mailbox, seen := mailboxes[grant.MailboxID]
		if !seen {
			mailbox, err = s.repo.FindMailbox(ctx, viewer.OrganizationID, grant.MailboxID)
			if errors.Is(err, ErrMailboxNotFound) {
				mailbox, err = nil, nil
			}
			if err != nil {
				return nil, err
			}
			if mailbox != nil {
				if mailbox.Access, err = s.accessFor(ctx, viewer, mailbox); err != nil {
					return nil, err
				}
			}
			mailboxes[grant.MailboxID] = mailbox
		}
		if mailbox != nil && mailbox.Access.View {
			effective = append(effective, effectiveGrant{grant: grant, mailbox: mailbox})
		}
	}
	return effective, nil
}

func grantIDs(grants []effectiveGrant) []uint {
	ids := make([]uint, 0, len(grants))
	for _, grant := range grants {
		ids = append(ids, grant.grant.ID)
	}
	return ids
}

func (s *service) AgentMailEvents(ctx context.Context, viewer domain.OrganizationContext, keyID, after uint, limit int) (*AgentMailFeed, error) {
	if limit < 1 || limit > domain.MaxMailAgentFeedPage {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", domain.ErrInvalidInput, domain.MaxMailAgentFeedPage)
	}
	if err := s.takeAgentRead(ctx, keyID); err != nil {
		return nil, err
	}
	grants, err := s.effectiveAgentGrants(ctx, viewer, keyID)
	if err != nil {
		return nil, err
	}
	events, err := s.agents.ListAgentEvents(ctx, grantIDs(grants), after, limit+1)
	if err != nil {
		return nil, err
	}
	feed := &AgentMailFeed{Events: events, NextCursor: after}
	if len(events) > limit {
		feed.Events, feed.HasMore = events[:limit], true
	}
	if len(feed.Events) > 0 {
		feed.NextCursor = feed.Events[len(feed.Events)-1].ID
	}
	return feed, nil
}

// agentMessageGrants are the effective grants that received a message; none hides it.
func (s *service) agentMessageGrants(ctx context.Context, viewer domain.OrganizationContext, keyID, messageID uint) ([]effectiveGrant, error) {
	grants, err := s.effectiveAgentGrants(ctx, viewer, keyID)
	if err != nil {
		return nil, err
	}
	events, err := s.agents.ListAgentMessageEvents(ctx, grantIDs(grants), messageID)
	if err != nil {
		return nil, err
	}
	received := map[uint]bool{}
	for _, event := range events {
		received[event.GrantID] = true
	}
	matched := make([]effectiveGrant, 0, len(events))
	for _, grant := range grants {
		if received[grant.grant.ID] {
			matched = append(matched, grant)
		}
	}
	if len(matched) == 0 {
		return nil, ErrMailMessageNotFound
	}
	return matched, nil
}

func (s *service) AgentMailMessage(ctx context.Context, viewer domain.OrganizationContext, keyID, messageID uint) (*AgentMailRead, error) {
	if err := s.takeAgentRead(ctx, keyID); err != nil {
		return nil, err
	}
	grants, err := s.agentMessageGrants(ctx, viewer, keyID, messageID)
	if err != nil {
		return nil, err
	}
	chosen := grants[0]
	for _, grant := range grants {
		if grant.grant.RevealSecrets {
			chosen = grant
			break
		}
	}
	message, err := s.repo.FindMessage(ctx, chosen.mailbox.ID, messageID)
	if err != nil {
		return nil, err
	}
	// An agent reads for no member: blind copies stay with the member who sent them.
	hideBlindCopies(0, message)
	if message, err = s.withEvents(ctx, message); err != nil {
		return nil, err
	}
	action := domain.MailAgentActionRead
	if chosen.grant.RevealSecrets {
		action = domain.MailAgentActionReadRevealed
	}
	// A read that cannot be audited is not served.
	if err := s.agents.AddAgentAudit(ctx, &domain.MailAgentAuditEntry{
		OrganizationID: viewer.OrganizationID, MailboxID: chosen.mailbox.ID, GrantID: chosen.grant.ID,
		APIKeyID: keyID, MessageID: messageID, Action: action, CreatedAt: s.now().UTC(),
	}); err != nil {
		return nil, err
	}
	if chosen.grant.RevealSecrets {
		slog.InfoContext(ctx, "mail.agent_secret_revealed",
			slog.Uint64("organization_id", uint64(viewer.OrganizationID)), slog.Uint64("mailbox_id", uint64(chosen.mailbox.ID)),
			slog.Uint64("grant_id", uint64(chosen.grant.ID)), slog.Uint64("message_id", uint64(messageID)))
	}
	return &AgentMailRead{Message: message, Revealed: chosen.grant.RevealSecrets}, nil
}

func (s *service) SaveAgentDraft(ctx context.Context, viewer domain.OrganizationContext, keyID uint, input AgentDraftInput) (*domain.MailAgentDraft, bool, error) {
	if strings.TrimSpace(input.Text) == "" {
		return nil, false, fmt.Errorf("%w: text is required", ErrMailMessageInvalid)
	}
	if len(input.Text) > domain.MaxMailAgentDraftBytes || len(input.HTML) > domain.MaxMailAgentDraftBytes {
		return nil, false, fmt.Errorf("%w: text and html are at most %d bytes each", ErrMailMessageInvalid, domain.MaxMailAgentDraftBytes)
	}
	grants, err := s.agentMessageGrants(ctx, viewer, keyID, input.MessageID)
	if err != nil {
		return nil, false, err
	}
	var chosen *effectiveGrant
	for i := range grants {
		if grants[i].grant.CanDraft && grants[i].mailbox.Access.Send {
			chosen = &grants[i]
			break
		}
	}
	if chosen == nil {
		return nil, false, fmt.Errorf("%w: the grant does not allow drafts", domain.ErrPermissionDenied)
	}
	if _, err = s.repo.FindMessage(ctx, chosen.mailbox.ID, input.MessageID); err != nil {
		return nil, false, err
	}
	now := s.now().UTC()
	draft := &domain.MailAgentDraft{
		OrganizationID: viewer.OrganizationID, MailboxID: chosen.mailbox.ID, GrantID: chosen.grant.ID,
		MessageID: input.MessageID, APIKeyID: keyID, TextBody: input.Text, HTMLBody: input.HTML, CreatedAt: now, UpdatedAt: now,
	}
	var created bool
	err = s.repo.InTransaction(ctx, func(tx context.Context) error {
		var saveErr error
		if created, saveErr = s.agents.SaveAgentDraft(tx, draft); saveErr != nil {
			return saveErr
		}
		return s.agents.AddAgentAudit(tx, &domain.MailAgentAuditEntry{
			OrganizationID: viewer.OrganizationID, MailboxID: chosen.mailbox.ID, GrantID: chosen.grant.ID,
			APIKeyID: keyID, MessageID: input.MessageID, Action: domain.MailAgentActionDraft, CreatedAt: now,
		})
	})
	if err != nil {
		return nil, false, err
	}
	return draft, created, nil
}

// appendAgentEvents writes one feed event for every live grant of the mailbox whose filters the
// message passes. It runs inside afterInboundStored, in the storing transaction. Mail content only
// ever selects among existing grants; it never changes one.
func (s *service) appendAgentEvents(tx context.Context, mailbox *domain.Mailbox, message *domain.MailMessage) error {
	if s.agents == nil || message.Direction != domain.MailDirectionInbound {
		return nil
	}
	now := s.now().UTC()
	grants, err := s.agents.ListLiveAgentGrantsOfMailbox(tx, mailbox.ID, now)
	if err != nil || len(grants) == 0 {
		return err
	}
	var labels []uint
	for _, grant := range grants {
		if len(grant.LabelIDs) > 0 {
			byMessage, err := s.repo.MessageLabelIDs(tx, []uint{message.ID})
			if err != nil {
				return err
			}
			labels = byMessage[message.ID]
			break
		}
	}
	events := make([]*domain.MailAgentEvent, 0, len(grants))
	for _, grant := range grants {
		if !grant.MatchesSender(message.FromAddress) || !grant.MatchesLabels(labels) {
			continue
		}
		events = append(events, &domain.MailAgentEvent{
			GrantID: grant.ID, MailboxID: mailbox.ID, MessageID: message.ID,
			Type: domain.MailAgentEventMessageReceived, CreatedAt: now,
		})
	}
	return s.agents.AppendAgentEvents(tx, mailbox.OrganizationID, events)
}

// endAgentMailbox revokes a mailbox's grants and deletes its drafts, which quote mail that is
// about to be sealed or that nobody may read any more. It runs in the caller's transaction.
func (s *service) endAgentMailbox(ctx context.Context, revocation domain.MailAgentGrantRevocation) error {
	if s.agents == nil {
		return nil
	}
	if err := s.revokeAgentGrants(ctx, revocation); err != nil {
		return err
	}
	return s.agents.DeleteAgentDrafts(ctx, revocation.MailboxID)
}

// PruneExpiredAgentMail deletes up to limit feed events and audit rows past their retention.
func (s *service) PruneExpiredAgentMail(ctx context.Context, now time.Time, limit int) (int, error) {
	if s.agents == nil || limit < 1 {
		return 0, nil
	}
	retention := s.settings.MailAgent.Resolved()
	events, err := s.agents.PruneAgentEvents(ctx, now.Add(-retention.EventRetention), limit)
	if err != nil || int(events) >= limit {
		return int(events), err
	}
	audit, err := s.agents.PruneAgentAudit(ctx, now.Add(-retention.AuditRetention), limit-int(events))
	return int(events + audit), err
}

// PruneAgentMailEvents deletes feed events created before a time, in batches.
func (s *service) PruneAgentMailEvents(ctx context.Context, before time.Time) (int64, error) {
	if err := s.agentsAvailable(); err != nil {
		return 0, err
	}
	var total int64
	for {
		deleted, err := s.agents.PruneAgentEvents(ctx, before, agentEventPruneBatch)
		total += deleted
		if err != nil || deleted < agentEventPruneBatch {
			return total, err
		}
	}
}
