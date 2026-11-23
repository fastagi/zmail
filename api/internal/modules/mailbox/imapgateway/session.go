package imapgateway

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"

	"github.com/fastagi/zmail/api/internal/domain"
)

// Responses of what P1a does not do.
var (
	errFoldersReadOnly = &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeNoPerm,
		Text: "Folders are managed in Zmail on the web"}
	errNoAppend = &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeCannot,
		Text: "Adding messages from a mail app is not supported yet"}
	errNoCopy = &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeCannot,
		Text: "Moving and copying messages from a mail app is not supported yet; organize mail in Zmail on the web"}
	errNoDelete = &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeCannot,
		Text: "Deleting messages from a mail app is not supported yet; delete mail in Zmail on the web"}
	errReadOnly     = &imap.Error{Type: imap.StatusResponseTypeNo, Text: "The mailbox is open read-only"}
	errNoSuchFolder = &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeNonExistent,
		Text: "No such mailbox"}
	errNotSelectable = &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeCannot,
		Text: "This name only holds other mailboxes"}
	errSearchUnsupported = &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeCannot,
		Text: "This search criterion is not supported"}
	errSearchLimit = &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeLimit,
		Text: "The search nests too deeply"}
	errTemporary = &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeUnavailable,
		Text: "Temporary failure, try again"}
)

// byeTimeout bounds writing the goodbye to a client that does not read.
const byeTimeout = 5 * time.Second

// selectableFlags are the flags a folder lists; only \Seen and \Flagged are kept.
var (
	selectableFlags = []imap.Flag{imap.FlagAnswered, imap.FlagFlagged, imap.FlagSeen, imap.FlagDeleted, imap.FlagDraft}
	permanentFlags  = []imap.Flag{imap.FlagSeen, imap.FlagFlagged}
)

// session is one mail app connection. go-imap runs its commands one at a time; mu guards what the
// revocation sweep reads from another goroutine.
type session struct {
	server *Server
	conn   *imapserver.Conn
	ip     string
	id     uint64

	mu        sync.Mutex
	principal *domain.MailClientPrincipal
	selected  *selection
	closed    bool
	// authTimer closes the connection if it has not signed in in time.
	authTimer *time.Timer
}

// selection is the selected folder and its messages' UIDs as the client knows them, by sequence
// number.
type selection struct {
	name        string
	mailboxID   uint
	key         string
	readOnly    bool
	uidValidity uint32
	uids        []uint32
}

var (
	_ imapserver.Session          = (*session)(nil)
	_ imapserver.SessionNamespace = (*session)(nil)
	_ imapserver.SessionMove      = (*session)(nil)
)

func (s *session) current() (*domain.MailClientPrincipal, *selection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.principal, s.selected
}

// bye ends the connection with an untagged BYE. Callers outside the session run it on its own
// goroutine: the write may wait up to byeTimeout for a client that does not read.
func (s *session) bye(text string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	// A client that stops reading must not hold the goodbye up.
	if err := s.conn.NetConn().SetWriteDeadline(time.Now().Add(byeTimeout)); err != nil {
		s.server.logger.Debug("imap_gateway.bye_deadline_failed", "connection_id", s.id, "error", err)
	}
	if err := s.conn.Bye(text); err != nil {
		s.server.logger.Debug("imap_gateway.bye_failed", "connection_id", s.id, "error", err)
	}
}

// startAuthDeadline closes the connection with a BYE unless it signs in within timeout.
func (s *session) startAuthDeadline(timeout time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authTimer = time.AfterFunc(timeout, func() {
		if principal, _ := s.current(); principal != nil {
			return
		}
		s.bye("Sign-in timed out")
	})
}

// stopAuthDeadline stops the sign-in deadline.
func (s *session) stopAuthDeadline() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authTimer != nil {
		s.authTimer.Stop()
	}
}

func (s *session) Close() error {
	s.stopAuthDeadline()
	principal, _ := s.current()
	var membershipID uint
	if principal != nil {
		membershipID = principal.Viewer.MembershipID
	}
	s.server.registry.close(s, membershipID)
	recordConnections(s.server.registry.count())
	if principal != nil {
		s.server.logger.Info("imap_gateway.session_closed", "connection_id", s.id, "password_id", principal.PasswordID,
			"organization_id", principal.Viewer.OrganizationID, "membership_id", principal.Viewer.MembershipID)
	}
	return nil
}

func (s *session) Login(username, password string) error {
	defer observeCommand("LOGIN", time.Now())
	ctx, cancel := s.server.commandContext()
	defer cancel()
	limiter := s.server.limiter
	if !limiter.Allow(ctx, username, s.ip) {
		recordAuth("locked")
		s.failureDelay()
		return imapserver.ErrAuthFailed
	}
	principal, err := s.server.store.AuthenticateClient(ctx, username, password, domain.MailClientProtocolIMAP, s.ip)
	if errors.Is(err, domain.ErrMailClientAuthFailed) {
		recordAuth("failure")
		limiter.Failed(ctx, username, s.ip)
		s.failureDelay()
		return imapserver.ErrAuthFailed
	}
	if err != nil {
		recordAuth("error")
		s.server.logger.Error("imap_gateway.login_failed", "connection_id", s.id, "error", err)
		return errTemporary
	}
	if err := s.server.registry.signIn(principal.Viewer.MembershipID); err != nil {
		recordAuth("limited")
		return &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeLimit, Text: err.Error()}
	}
	limiter.Succeeded(ctx, username, s.ip)
	recordAuth("success")
	s.mu.Lock()
	s.principal = principal
	s.mu.Unlock()
	s.stopAuthDeadline()
	s.server.logger.Info("imap_gateway.session_opened", "connection_id", s.id, "password_id", principal.PasswordID,
		"organization_id", principal.Viewer.OrganizationID, "membership_id", principal.Viewer.MembershipID)
	return nil
}

func (s *session) failureDelay() {
	if delay := s.server.cfg.FailureDelay; delay > 0 {
		select {
		case <-time.After(delay):
		case <-s.server.ctx.Done():
		}
	}
}

// storeError turns a store failure into a response; unexpected ones are logged with ids only.
func (s *session) storeError(command string, err error) error {
	switch {
	case errors.Is(err, domain.ErrMailClientFolderNotFound):
		return errNoSuchFolder
	case errors.Is(err, domain.ErrMailClientSearchUnsupported):
		return errSearchUnsupported
	case errors.Is(err, errSearchTooDeep):
		return errSearchLimit
	}
	s.server.logger.Error("imap_gateway.command_failed", "connection_id", s.id, "command", command, "error", err)
	return errTemporary
}

// tree lists the member's folders as the gateway names them.
func (s *session) tree(ctx context.Context, principal *domain.MailClientPrincipal) (*folderTree, error) {
	folders, err := s.server.store.ClientFolders(ctx, principal)
	if err != nil {
		return nil, err
	}
	return newFolderTree(folders), nil
}

func (s *session) Namespace() (*imap.NamespaceData, error) {
	return &imap.NamespaceData{
		Personal: []imap.NamespaceDescriptor{{Prefix: "", Delim: delimiter}},
		Shared:   []imap.NamespaceDescriptor{{Prefix: sharedPrefix + string(delimiter), Delim: delimiter}},
	}, nil
}

func (s *session) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	defer observeCommand("LIST", time.Now())
	if len(patterns) == 0 {
		return w.WriteList(&imap.ListData{Attrs: []imap.MailboxAttr{imap.MailboxAttrNoSelect}, Delim: delimiter})
	}
	principal, _ := s.current()
	ctx, cancel := s.server.commandContext()
	defer cancel()
	tree, err := s.tree(ctx, principal)
	if err != nil {
		return s.storeError("LIST", err)
	}
	for _, entry := range tree.all() {
		if !slices.ContainsFunc(patterns, func(pattern string) bool {
			return imapserver.MatchList(entry.name, delimiter, ref, pattern)
		}) {
			continue
		}
		data := imap.ListData{Mailbox: entry.name, Delim: delimiter}
		if !entry.selectable() {
			if options.SelectSubscribed || options.SelectSpecialUse {
				continue
			}
			data.Attrs = []imap.MailboxAttr{imap.MailboxAttrNoSelect, imap.MailboxAttrHasChildren}
			if err := w.WriteList(&data); err != nil {
				return err
			}
			continue
		}
		if options.SelectSpecialUse && entry.specialUse == "" {
			continue
		}
		data.Attrs = []imap.MailboxAttr{imap.MailboxAttrHasNoChildren}
		if entry.specialUse != "" {
			data.Attrs = append(data.Attrs, entry.specialUse)
		}
		// Every folder counts as subscribed: there is nothing to subscribe to separately.
		if options.SelectSubscribed || options.ReturnSubscribed {
			data.Attrs = append(data.Attrs, imap.MailboxAttrSubscribed)
		}
		if options.ReturnStatus != nil {
			status, err := s.status(ctx, principal, entry, options.ReturnStatus)
			if err != nil {
				return s.storeError("LIST", err)
			}
			data.Status = status
		}
		if err := w.WriteList(&data); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) status(ctx context.Context, principal *domain.MailClientPrincipal, entry folderEntry, options *imap.StatusOptions) (*imap.StatusData, error) {
	state, err := s.server.store.ClientFolderState(ctx, principal, entry.folder.MailboxID, entry.folder.Key)
	if err != nil {
		return nil, err
	}
	data := &imap.StatusData{Mailbox: entry.name}
	if options.NumMessages {
		data.NumMessages = new(uint32(len(state.UIDs)))
	}
	if options.UIDNext {
		data.UIDNext = imap.UID(state.UIDNext)
	}
	if options.UIDValidity {
		data.UIDValidity = state.UIDValidity
	}
	if options.NumUnseen {
		data.NumUnseen = new(uint32(state.Unseen))
	}
	if options.NumRecent {
		data.NumRecent = new(uint32(0))
	}
	if options.NumDeleted {
		data.NumDeleted = new(uint32(0))
	}
	return data, nil
}

func (s *session) Status(name string, options *imap.StatusOptions) (*imap.StatusData, error) {
	defer observeCommand("STATUS", time.Now())
	principal, _ := s.current()
	ctx, cancel := s.server.commandContext()
	defer cancel()
	tree, err := s.tree(ctx, principal)
	if err != nil {
		return nil, s.storeError("STATUS", err)
	}
	entry, ok := tree.lookup(name)
	if !ok {
		return nil, errNoSuchFolder
	}
	if !entry.selectable() {
		return nil, errNotSelectable
	}
	data, err := s.status(ctx, principal, entry, options)
	if err != nil {
		return nil, s.storeError("STATUS", err)
	}
	return data, nil
}

func (s *session) Select(name string, options *imap.SelectOptions) (*imap.SelectData, error) {
	defer observeCommand("SELECT", time.Now())
	principal, _ := s.current()
	ctx, cancel := s.server.commandContext()
	defer cancel()
	tree, err := s.tree(ctx, principal)
	if err != nil {
		return nil, s.storeError("SELECT", err)
	}
	entry, ok := tree.lookup(name)
	if !ok {
		return nil, errNoSuchFolder
	}
	if !entry.selectable() {
		return nil, errNotSelectable
	}
	state, err := s.server.store.ClientFolderState(ctx, principal, entry.folder.MailboxID, entry.folder.Key)
	if err != nil {
		return nil, s.storeError("SELECT", err)
	}
	selected := &selection{
		name: entry.name, mailboxID: entry.folder.MailboxID, key: entry.folder.Key,
		readOnly: options != nil && options.ReadOnly, uidValidity: state.UIDValidity, uids: state.UIDs,
	}
	s.mu.Lock()
	s.selected = selected
	s.mu.Unlock()
	data := &imap.SelectData{
		Flags: selectableFlags, PermanentFlags: permanentFlags,
		NumMessages: uint32(len(state.UIDs)), UIDNext: imap.UID(state.UIDNext), UIDValidity: state.UIDValidity,
	}
	if selected.readOnly {
		data.PermanentFlags = nil
	}
	return data, nil
}

func (s *session) Unselect() error {
	s.mu.Lock()
	s.selected = nil
	s.mu.Unlock()
	return nil
}

// Poll tells the client about mail added to or removed from the selected folder since it last
// looked, by comparing the folder's UIDs with the client's. Removals wait for a command that may
// report them.
func (s *session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	principal, selected := s.current()
	if selected == nil {
		return nil
	}
	ctx, cancel := s.server.commandContext()
	defer cancel()
	state, err := s.server.store.ClientFolderState(ctx, principal, selected.mailboxID, selected.key)
	if errors.Is(err, domain.ErrMailClientFolderNotFound) {
		s.bye("The selected mailbox is no longer available")
		return err
	}
	if err != nil {
		// A failed refresh changes nothing; the next command tries again.
		s.server.logger.Warn("imap_gateway.poll_failed", "connection_id", s.id, "error", err)
		return nil
	}
	if state.UIDValidity != selected.uidValidity {
		s.bye("The selected mailbox was renumbered; open it again")
		return errors.New("imapgateway: uidvalidity changed")
	}
	current := make(map[uint32]bool, len(state.UIDs))
	for _, uid := range state.UIDs {
		current[uid] = true
	}
	known := make(map[uint32]bool, len(selected.uids))
	for _, uid := range selected.uids {
		known[uid] = true
	}
	if allowExpunge {
		for seq := len(selected.uids); seq >= 1; seq-- {
			if current[selected.uids[seq-1]] {
				continue
			}
			if err := w.WriteExpunge(uint32(seq)); err != nil {
				return err
			}
			selected.uids = slices.Delete(selected.uids, seq-1, seq)
		}
	}
	added := false
	for _, uid := range state.UIDs {
		if !known[uid] {
			selected.uids = append(selected.uids, uid)
			added = true
		}
	}
	if added {
		return w.WriteNumMessages(uint32(len(selected.uids)))
	}
	return nil
}

// Idle polls the selected folder until the client ends the IDLE. beta.9 always offers IDLE, so it
// is served by polling every Config.IdlePoll; P1c replaces this with notifications.
func (s *session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	ticker := time.NewTicker(s.server.cfg.IdlePoll)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-s.server.ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.Poll(w, true); err != nil {
				return err
			}
		}
	}
}

// target is one message a command names: its sequence number and UID.
type target struct {
	seq uint32
	uid uint32
}

// targets resolves a sequence or UID set against the selection.
func (sel *selection) targets(numSet imap.NumSet) []target {
	count := uint32(len(sel.uids))
	var targets []target
	switch set := numSet.(type) {
	case imap.SeqSet:
		for _, seqRange := range set {
			start, stop := staticRange(seqRange.Start, seqRange.Stop, count)
			for seq := max(start, 1); seq <= min(stop, count); seq++ {
				targets = append(targets, target{seq: seq, uid: sel.uids[seq-1]})
			}
		}
	case imap.UIDSet:
		largest := uint32(0)
		if count > 0 {
			largest = sel.uids[count-1]
		}
		for _, uidRange := range set {
			start, stop := staticRange(uint32(uidRange.Start), uint32(uidRange.Stop), largest)
			from, _ := slices.BinarySearch(sel.uids, start)
			for i := from; i < len(sel.uids) && sel.uids[i] <= stop; i++ {
				targets = append(targets, target{seq: uint32(i + 1), uid: sel.uids[i]})
			}
		}
	}
	slices.SortFunc(targets, func(a, b target) int { return int(a.seq) - int(b.seq) })
	return slices.CompactFunc(targets, func(a, b target) bool { return a.seq == b.seq })
}

func (s *session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, _ *imap.StoreOptions) error {
	defer observeCommand("STORE", time.Now())
	principal, selected := s.current()
	if selected == nil {
		return errNoSuchFolder
	}
	if selected.readOnly {
		return errReadOnly
	}
	targets := selected.targets(numSet)
	has := func(flag imap.Flag) bool {
		return slices.ContainsFunc(flags.Flags, func(f imap.Flag) bool { return canonicalFlag(f) == flag })
	}
	// Marking a message \Deleted is refused like EXPUNGE, so the app never hides mail that stays.
	if flags.Op != imap.StoreFlagsDel && has(imap.FlagDeleted) {
		return errNoDelete
	}
	changes := map[domain.MailClientFlag]bool{}
	for flag, imapFlag := range map[domain.MailClientFlag]imap.Flag{domain.MailClientFlagSeen: imap.FlagSeen, domain.MailClientFlagFlagged: imap.FlagFlagged} {
		switch {
		case flags.Op == imap.StoreFlagsSet:
			changes[flag] = has(imapFlag)
		case has(imapFlag):
			changes[flag] = flags.Op == imap.StoreFlagsAdd
		}
	}
	// \Draft, \Answered, and keywords are not kept in P1a and are ignored.
	ctx, cancel := s.server.commandContext()
	defer cancel()
	uids := make([]uint32, 0, len(targets))
	for _, t := range targets {
		uids = append(uids, t.uid)
	}
	for flag, value := range changes {
		if len(uids) == 0 {
			break
		}
		if err := s.server.store.SetClientFlag(ctx, principal, selected.mailboxID, selected.key, uids, flag, value); err != nil {
			return s.storeError("STORE", err)
		}
	}
	if flags.Silent {
		return nil
	}
	return s.fetch(ctx, w, principal, selected, targets, &imap.FetchOptions{Flags: true, UID: true}, "STORE")
}

func (s *session) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, _ *imap.SearchOptions) (*imap.SearchData, error) {
	defer observeCommand("SEARCH", time.Now())
	principal, selected := s.current()
	if selected == nil {
		return nil, errNoSuchFolder
	}
	translated, err := translateSearch(criteria, selected.uids)
	if err != nil {
		return nil, s.storeError("SEARCH", err)
	}
	ctx, cancel := s.server.commandContext()
	defer cancel()
	found, err := s.server.store.SearchClient(ctx, principal, selected.mailboxID, selected.key, translated)
	if err != nil {
		return nil, s.storeError("SEARCH", err)
	}
	data := &imap.SearchData{}
	var seqSet imap.SeqSet
	var uidSet imap.UIDSet
	for _, uid := range found {
		index, ok := slices.BinarySearch(selected.uids, uid)
		if !ok {
			// Mail the client has not been told about yet stays out until the next poll.
			continue
		}
		num := uid
		if kind == imapserver.NumKindSeq {
			num = uint32(index + 1)
			seqSet.AddNum(num)
		} else {
			uidSet.AddNum(imap.UID(uid))
		}
		if data.Min == 0 || num < data.Min {
			data.Min = num
		}
		data.Max = max(data.Max, num)
		data.Count++
	}
	if kind == imapserver.NumKindSeq {
		data.All = seqSet
	} else {
		data.All = uidSet
	}
	return data, nil
}

func (s *session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	defer observeCommand("FETCH", time.Now())
	principal, selected := s.current()
	if selected == nil {
		return errNoSuchFolder
	}
	ctx, cancel := s.server.commandContext()
	defer cancel()
	return s.fetch(ctx, w, principal, selected, selected.targets(numSet), options, "FETCH")
}

// Folder changes and new messages are not offered in P1a.

func (s *session) Create(string, *imap.CreateOptions) error               { return errFoldersReadOnly }
func (s *session) Delete(string) error                                    { return errFoldersReadOnly }
func (s *session) Rename(string, string, *imap.RenameOptions) error       { return errFoldersReadOnly }
func (s *session) Subscribe(string) error                                 { return nil }
func (s *session) Unsubscribe(string) error                               { return nil }
func (s *session) Copy(imap.NumSet, string) (*imap.CopyData, error)       { return nil, errNoCopy }
func (s *session) Move(*imapserver.MoveWriter, imap.NumSet, string) error { return errNoCopy }

func (s *session) Append(string, imap.LiteralReader, *imap.AppendOptions) (*imap.AppendData, error) {
	return nil, errNoAppend
}

// Expunge (and UID EXPUNGE) is refused: P1a removes nothing from a mail app.
func (s *session) Expunge(*imapserver.ExpungeWriter, *imap.UIDSet) error { return errNoDelete }
