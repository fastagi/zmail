package mailbox

import (
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

// appPasswordTouchInterval spaces the writes of a password's last use.
const appPasswordTouchInterval = 5 * time.Minute

var _ domain.MailClientStore = (*service)(nil)

func (s *service) clientRepository() (mailClientRepository, error) {
	store, ok := s.repo.(mailClientRepository)
	if !ok {
		return nil, domain.ErrServiceUnavailable
	}
	return store, nil
}

// AuthenticateClient signs a mail app in. The secret is hashed before anything is looked up, so an
// unknown identity costs the same work, and every refusal is the one ErrMailClientAuthFailed.
// Failures of the database are returned as they are, so a gateway does not count an outage as a
// wrong password.
func (s *service) AuthenticateClient(ctx context.Context, username, password string, protocol domain.MailClientProtocol, ip string) (*domain.MailClientPrincipal, error) {
	hash := hashAppPassword([]byte(s.settings.AppPasswordPepper), normalizeAppPassword(password))
	store, err := s.clientRepository()
	if err != nil {
		return nil, err
	}
	if s.appPasswords == nil {
		return nil, domain.ErrServiceUnavailable
	}
	if s.settings.AppPasswordPepper == "" || !protocol.IsValid() {
		return nil, domain.ErrMailClientAuthFailed
	}
	found, err := store.FindClientPassword(ctx, hash)
	if errors.Is(err, ErrMailAppPasswordNotFound) {
		return nil, domain.ErrMailClientAuthFailed
	}
	if err != nil {
		return nil, err
	}
	if (protocol == domain.MailClientProtocolIMAP && !found.ScopeIMAP) || (protocol == domain.MailClientProtocolSMTP && !found.ScopeSMTP) {
		return nil, domain.ErrMailClientAuthFailed
	}
	settings, err := s.appPasswords.FindMailClientSettings(ctx, found.OrganizationID)
	if err != nil {
		return nil, err
	}
	if !settings.AccessEnabled {
		return nil, domain.ErrMailClientAuthFailed
	}
	resolved, err := store.FindClientViewer(ctx, found.OrganizationID, found.MembershipID, found.UserID)
	if errors.Is(err, domain.ErrOrganizationNotFound) {
		return nil, domain.ErrMailClientAuthFailed
	}
	if err != nil {
		return nil, err
	}
	if !resolved.Viewer.IsValid() || !resolved.Viewer.CanUse(domain.MemberFeatureMailClients) {
		return nil, domain.ErrMailClientAuthFailed
	}
	root, err := s.clientRoot(ctx, resolved, strings.ToLower(strings.TrimSpace(username)))
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	if err := store.TouchAppPassword(ctx, found.ID, coarseIP(ip), protocol, now, now.Add(-appPasswordTouchInterval)); err != nil {
		return nil, err
	}
	return &domain.MailClientPrincipal{PasswordID: found.ID, Viewer: resolved.Viewer, RootMailboxID: root}, nil
}

// clientRoot resolves the username to the mailbox shown at the root: a Zmail mailbox the member
// may view by its address, or the member's own personal mailbox by the account email.
func (s *service) clientRoot(ctx context.Context, resolved *clientViewer, username string) (uint, error) {
	if username == "" {
		return 0, domain.ErrMailClientAuthFailed
	}
	mailbox, err := s.repo.FindMailboxByAddress(ctx, username)
	switch {
	case err == nil:
		if mailbox.OrganizationID != resolved.Viewer.OrganizationID {
			return 0, domain.ErrMailClientAuthFailed
		}
		if accessErr := s.checkAccess(ctx, resolved.Viewer, mailbox, canView, ErrMailboxNotFound); accessErr != nil {
			if errors.Is(accessErr, ErrMailboxNotFound) || errors.Is(accessErr, domain.ErrPermissionDenied) {
				return 0, domain.ErrMailClientAuthFailed
			}
			return 0, accessErr
		}
		return mailbox.ID, nil
	case !errors.Is(err, ErrMailboxNotFound):
		return 0, err
	}
	if !strings.EqualFold(strings.TrimSpace(resolved.AccountEmail), username) {
		return 0, domain.ErrMailClientAuthFailed
	}
	personal, err := s.clientPersonalMailbox(ctx, resolved.Viewer)
	if err != nil {
		return 0, err
	}
	if personal == nil {
		return 0, nil
	}
	return personal.ID, nil
}

// clientPersonalMailbox is the member's own Zmail personal mailbox with the lowest id, or nil.
func (s *service) clientPersonalMailbox(ctx context.Context, viewer domain.OrganizationContext) (*domain.Mailbox, error) {
	mailboxes, err := s.visibleMailboxes(ctx, viewer)
	if err != nil {
		return nil, err
	}
	var personal *domain.Mailbox
	for _, mailbox := range mailboxes {
		if mailbox.Type == domain.MailboxTypePersonal && !mailbox.Connected() && mailbox.Access.View &&
			mailbox.OwnerUserID != nil && *mailbox.OwnerUserID == viewer.UserID && (personal == nil || mailbox.ID < personal.ID) {
			personal = mailbox
		}
	}
	return personal, nil
}

// coarseIP keeps the /24 of an IPv4 address and the /48 of an IPv6 one; anything else is empty.
func coarseIP(ip string) string {
	host := ip
	if splitHost, _, err := net.SplitHostPort(ip); err == nil {
		host = splitHost
	}
	parsed := net.ParseIP(host)
	switch {
	case parsed == nil:
		return ""
	case parsed.To4() != nil:
		return parsed.Mask(net.CIDRMask(24, 32)).String()
	default:
		return parsed.Mask(net.CIDRMask(48, 128)).String()
	}
}

// RevalidateClients reports which principals may still sign in, by password id.
func (s *service) RevalidateClients(ctx context.Context, principals []*domain.MailClientPrincipal) (map[uint]bool, error) {
	store, err := s.clientRepository()
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(principals))
	for _, principal := range principals {
		if principal != nil && !slices.Contains(ids, principal.PasswordID) {
			ids = append(ids, principal.PasswordID)
		}
	}
	return store.ValidClientPasswords(ctx, ids)
}

// ClientMailboxVisible reports whether the principal may still read the mailbox from a mail app.
func (s *service) ClientMailboxVisible(ctx context.Context, p *domain.MailClientPrincipal, mailboxID uint) (bool, error) {
	err := s.clientMailbox(ctx, p, mailboxID)
	if errors.Is(err, domain.ErrMailClientFolderNotFound) {
		return false, nil
	}
	return err == nil, err
}

// clientMailbox authorizes view of a mailbox a mail app may show: the root, or a shared Zmail
// mailbox. Anything else is ErrMailClientFolderNotFound.
func (s *service) clientMailbox(ctx context.Context, p *domain.MailClientPrincipal, mailboxID uint) error {
	if p == nil {
		return domain.ErrMailClientFolderNotFound
	}
	mailbox, err := s.authorizeMailbox(ctx, p.Viewer, mailboxID, canView)
	if errors.Is(err, ErrMailboxNotFound) || errors.Is(err, domain.ErrPermissionDenied) {
		return domain.ErrMailClientFolderNotFound
	}
	if err != nil {
		return err
	}
	if mailbox.Connected() || (mailbox.ID != p.RootMailboxID && mailbox.Type != domain.MailboxTypeShared) {
		return domain.ErrMailClientFolderNotFound
	}
	return nil
}

// clientFolder authorizes the mailbox and checks that key names one of its folders.
func (s *service) clientFolder(ctx context.Context, p *domain.MailClientPrincipal, mailboxID uint, key string) (mailClientRepository, error) {
	store, err := s.clientRepository()
	if err != nil {
		return nil, err
	}
	if err = s.clientMailbox(ctx, p, mailboxID); err != nil {
		return nil, err
	}
	switch key {
	case domain.MailClientFolderInbox, domain.MailClientFolderSent, domain.MailClientFolderArchive, domain.MailClientFolderTrash:
		return store, nil
	}
	folderID, err := strconv.ParseUint(strings.TrimPrefix(key, "folder:"), 10, 64)
	if !strings.HasPrefix(key, "folder:") || err != nil || folderID == 0 {
		return nil, domain.ErrMailClientFolderNotFound
	}
	exists, err := store.FolderExists(ctx, mailboxID, uint(folderID))
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, domain.ErrMailClientFolderNotFound
	}
	return store, nil
}

// ClientFolders lists the folders a mail app shows: the root mailbox's standard folders and user
// folders, then those of every shared Zmail mailbox the member may view, by address.
func (s *service) ClientFolders(ctx context.Context, p *domain.MailClientPrincipal) ([]domain.MailClientFolder, error) {
	if p == nil {
		return nil, domain.ErrMailClientFolderNotFound
	}
	mailboxes, err := s.visibleMailboxes(ctx, p.Viewer)
	if err != nil {
		return nil, err
	}
	var root *domain.Mailbox
	shared := make([]*domain.Mailbox, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		if !mailbox.Access.View || mailbox.Connected() {
			continue
		}
		switch {
		case mailbox.ID == p.RootMailboxID:
			root = mailbox
		case mailbox.Type == domain.MailboxTypeShared:
			shared = append(shared, mailbox)
		}
	}
	slices.SortFunc(shared, func(a, b *domain.Mailbox) int { return strings.Compare(a.Address, b.Address) })
	var folders []domain.MailClientFolder
	add := func(mailbox *domain.Mailbox, isShared bool) error {
		for _, key := range []string{domain.MailClientFolderInbox, domain.MailClientFolderSent, domain.MailClientFolderArchive, domain.MailClientFolderTrash} {
			folders = append(folders, domain.MailClientFolder{MailboxID: mailbox.ID, Key: key, Shared: isShared, MailboxAddress: mailbox.Address})
		}
		if s.organize == nil {
			return nil
		}
		userFolders, err := s.organize.ListFolders(ctx, mailbox.ID)
		if err != nil {
			return err
		}
		for _, folder := range userFolders {
			folders = append(folders, domain.MailClientFolder{
				MailboxID: mailbox.ID, Key: "folder:" + strconv.FormatUint(uint64(folder.ID), 10), Name: folder.Name,
				Shared: isShared, MailboxAddress: mailbox.Address,
			})
		}
		return nil
	}
	if root != nil {
		if err := add(root, false); err != nil {
			return nil, err
		}
	}
	for _, mailbox := range shared {
		if err := add(mailbox, true); err != nil {
			return nil, err
		}
	}
	return folders, nil
}

// ClientFolderState reads a folder's UIDVALIDITY, UIDNEXT, UIDs, and the member's unseen count.
func (s *service) ClientFolderState(ctx context.Context, p *domain.MailClientPrincipal, mailboxID uint, key string) (*domain.MailClientFolderState, error) {
	store, err := s.clientFolder(ctx, p, mailboxID, key)
	if err != nil {
		return nil, err
	}
	return store.ClientFolderState(ctx, mailboxID, key, p.Viewer.MembershipID)
}

// ClientMessages lists a folder's messages with the member's flags.
func (s *service) ClientMessages(ctx context.Context, p *domain.MailClientPrincipal, mailboxID uint, key string, uids []uint32) ([]domain.MailClientMessage, error) {
	store, err := s.clientFolder(ctx, p, mailboxID, key)
	if err != nil {
		return nil, err
	}
	return store.ClientMessages(ctx, mailboxID, key, p.Viewer.MembershipID, uids)
}

// ClientMessageSource returns what a mail app downloads for one message: its stored source, or a
// message rebuilt from its fields. It is never masked (see the gateway spec on verification codes).
func (s *service) ClientMessageSource(ctx context.Context, p *domain.MailClientPrincipal, mailboxID uint, key string, uid uint32) ([]byte, error) {
	store, err := s.clientFolder(ctx, p, mailboxID, key)
	if err != nil {
		return nil, err
	}
	ids, err := store.ClientMessageIDs(ctx, mailboxID, key, []uint32{uid})
	if err != nil {
		return nil, err
	}
	messageID, ok := ids[uid]
	if !ok {
		return nil, domain.ErrMailClientMessageNotFound
	}
	message, err := s.repo.FindMessage(ctx, mailboxID, messageID)
	if errors.Is(err, ErrMailMessageNotFound) {
		return nil, domain.ErrMailClientMessageNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.messageSource(ctx, message)
}

// SetClientFlag changes the member's read state or star through the same path as the web.
func (s *service) SetClientFlag(ctx context.Context, p *domain.MailClientPrincipal, mailboxID uint, key string, uids []uint32, flag domain.MailClientFlag, value bool) error {
	store, err := s.clientFolder(ctx, p, mailboxID, key)
	if err != nil {
		return err
	}
	var update MessageUpdate
	switch flag {
	case domain.MailClientFlagSeen:
		update.Read = &value
	case domain.MailClientFlagFlagged:
		update.Starred = &value
	default:
		return domain.ErrInvalidInput
	}
	byUID, err := store.ClientMessageIDs(ctx, mailboxID, key, uids)
	if err != nil {
		return err
	}
	ids := make([]uint, 0, len(byUID))
	for _, id := range byUID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for chunk := range slices.Chunk(ids, MaxMessageIDs) {
		if _, err := s.UpdateMessages(ctx, p.Viewer, mailboxID, chunk, update); err != nil {
			if errors.Is(err, ErrMailboxNotFound) || errors.Is(err, domain.ErrPermissionDenied) {
				return domain.ErrMailClientFolderNotFound
			}
			// A message moved away between the lookup and the change; the next poll tells the app.
			if errors.Is(err, ErrMailMessageNotFound) {
				continue
			}
			return err
		}
	}
	return nil
}

// SearchClient searches one folder; the result is at most 10000 UIDs, ascending.
func (s *service) SearchClient(ctx context.Context, p *domain.MailClientPrincipal, mailboxID uint, key string, criteria domain.MailClientSearch) ([]uint32, error) {
	store, err := s.clientFolder(ctx, p, mailboxID, key)
	if err != nil {
		return nil, err
	}
	return store.SearchClient(ctx, mailboxID, key, p.Viewer.MembershipID, criteria)
}
