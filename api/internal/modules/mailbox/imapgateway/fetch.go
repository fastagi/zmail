package imapgateway

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"sync"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-message/textproto"

	"github.com/fastagi/zmail/api/internal/domain"
)

// needsSource reports whether a FETCH reads the message itself rather than its flags.
func needsSource(options *imap.FetchOptions) bool {
	return options.Envelope || options.BodyStructure != nil || options.RFC822Size ||
		len(options.BodySection) > 0 || len(options.BinarySection) > 0 || len(options.BinarySectionSize) > 0
}

// marksSeen reports whether a FETCH reads a body without PEEK, which sets \Seen (RFC 3501 §6.4.5).
func marksSeen(options *imap.FetchOptions) bool {
	for _, section := range options.BodySection {
		if !section.Peek {
			return true
		}
	}
	return len(options.BinarySection) > 0
}

func messageFlags(message domain.MailClientMessage) []imap.Flag {
	flags := make([]imap.Flag, 0, 3)
	if message.Seen {
		flags = append(flags, imap.FlagSeen)
	}
	if message.Flagged {
		flags = append(flags, imap.FlagFlagged)
	}
	if message.Answered {
		flags = append(flags, imap.FlagAnswered)
	}
	return flags
}

// fetch writes the requested items of targets, FetchBatch messages per store read. Every batch
// asks the store first, which checks the member may still read the folder.
func (s *session) fetch(ctx context.Context, w *imapserver.FetchWriter, principal *domain.MailClientPrincipal,
	selected *selection, targets []target, options *imap.FetchOptions, command string) error {
	markSeen := marksSeen(options) && !selected.readOnly
	batch := s.server.cfg.FetchBatch
	for start := 0; start < len(targets); start += batch {
		chunk := targets[start:min(start+batch, len(targets))]
		uids := make([]uint32, 0, len(chunk))
		for _, t := range chunk {
			uids = append(uids, t.uid)
		}
		messages, err := s.server.store.ClientMessages(ctx, principal, selected.mailboxID, selected.key, uids)
		if err != nil {
			return s.storeError(command, err)
		}
		byUID := make(map[uint32]domain.MailClientMessage, len(messages))
		var unseen []uint32
		for _, message := range messages {
			byUID[message.UID] = message
			if markSeen && !message.Seen {
				unseen = append(unseen, message.UID)
			}
		}
		if len(unseen) > 0 {
			if err := s.server.store.SetClientFlag(ctx, principal, selected.mailboxID, selected.key, unseen,
				domain.MailClientFlagSeen, true); err != nil {
				return s.storeError(command, err)
			}
			for _, uid := range unseen {
				message := byUID[uid]
				message.Seen = true
				byUID[uid] = message
			}
		}
		for _, t := range chunk {
			message, ok := byUID[t.uid]
			if !ok {
				// Removed since the client last looked; the next poll reports the expunge.
				continue
			}
			if err := s.writeMessage(ctx, w, principal, selected, t, message, options, markSeen, command); err != nil {
				return err
			}
		}
	}
	return nil
}

// source returns a message's source and the done func that frees its slot, which the caller
// calls once it no longer holds the source. At most Config.MaxSourceFetches sources are held at
// once; a source larger than domain.MailClientMaxSourceBytes is refused and never cached.
func (s *session) source(ctx context.Context, principal *domain.MailClientPrincipal, selected *selection, message domain.MailClientMessage) ([]byte, func(), error) {
	select {
	case s.server.sourceSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, func() {}, ctx.Err()
	}
	var once sync.Once
	done := func() { once.Do(func() { <-s.server.sourceSlots }) }
	if raw, ok := s.server.cache.Get(message.MessageID); ok {
		return raw, done, nil
	}
	raw, err := s.server.store.ClientMessageSource(ctx, principal, selected.mailboxID, selected.key, message.UID)
	if err == nil && len(raw) > domain.MailClientMaxSourceBytes {
		err = domain.ErrMailClientMessageTooLarge
	}
	if err != nil {
		done()
		return nil, func() {}, err
	}
	s.server.cache.Add(message.MessageID, raw)
	return raw, done, nil
}

func (s *session) writeMessage(ctx context.Context, w *imapserver.FetchWriter, principal *domain.MailClientPrincipal,
	selected *selection, t target, message domain.MailClientMessage, options *imap.FetchOptions, markSeen bool, command string) error {
	var raw []byte
	if needsSource(options) {
		var err error
		var done func()
		raw, done, err = s.source(ctx, principal, selected, message)
		defer done()
		if errors.Is(err, domain.ErrMailClientMessageNotFound) {
			return nil
		}
		if errors.Is(err, domain.ErrMailClientMessageTooLarge) {
			// Skipped rather than failing the rest of the FETCH; the id lets an operator find it.
			s.server.logger.Warn("imap_gateway.source_too_large", "connection_id", s.id, "message_id", message.MessageID)
			return nil
		}
		if err != nil {
			return s.storeError(command, err)
		}
	}
	out := w.CreateMessage(t.seq)
	if options.UID {
		out.WriteUID(imap.UID(t.uid))
	}
	if options.Flags || markSeen {
		out.WriteFlags(messageFlags(message))
	}
	if options.InternalDate {
		out.WriteInternalDate(message.InternalDate)
	}
	if options.RFC822Size {
		out.WriteRFC822Size(int64(len(raw)))
	}
	if options.Envelope {
		header, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
		if err != nil {
			header = textproto.Header{}
		}
		out.WriteEnvelope(imapserver.ExtractEnvelope(header))
	}
	if options.BodyStructure != nil {
		out.WriteBodyStructure(imapserver.ExtractBodyStructure(bytes.NewReader(raw)))
	}
	for _, section := range options.BodySection {
		data := imapserver.ExtractBodySection(bytes.NewReader(raw), section)
		writer := out.WriteBodySection(section, int64(len(data)))
		if _, err := writer.Write(data); err != nil {
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
	}
	for _, section := range options.BinarySection {
		data := imapserver.ExtractBinarySection(bytes.NewReader(raw), section)
		writer := out.WriteBinarySection(section, int64(len(data)))
		if _, err := writer.Write(data); err != nil {
			return err
		}
		if err := writer.Close(); err != nil {
			return err
		}
	}
	for _, section := range options.BinarySectionSize {
		out.WriteBinarySectionSize(section, imapserver.ExtractBinarySectionSize(bytes.NewReader(raw), section))
	}
	return out.Close()
}
