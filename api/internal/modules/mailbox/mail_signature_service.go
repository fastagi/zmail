package mailbox

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

// signatureStore returns the signature store, or ErrServiceUnavailable without one.
func (s *service) signatureStore() (domain.MailSignatureRepository, error) {
	if s.signatures == nil {
		return nil, domain.ErrServiceUnavailable
	}
	return s.signatures, nil
}

// MailSignature reads the viewer's own signature for a mailbox they may send from.
func (s *service) MailSignature(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint) (domain.MailSignature, error) {
	store, err := s.signatureStore()
	if err != nil {
		return domain.MailSignature{}, err
	}
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, needSend)
	if err != nil {
		return domain.MailSignature{}, err
	}
	return store.FindMailSignature(ctx, mailbox.ID, viewer.UserID)
}

// SaveMailSignature replaces the viewer's own signature for a mailbox they may send from; an
// empty body removes it. Line breaks are normalized and trailing white space dropped.
func (s *service) SaveMailSignature(ctx context.Context, viewer domain.OrganizationContext, mailboxID uint, body string) (domain.MailSignature, error) {
	store, err := s.signatureStore()
	if err != nil {
		return domain.MailSignature{}, err
	}
	body = normalizeSignature(body)
	invalid := &ValidationError{}
	if utf8.RuneCountInString(body) > domain.MailSignatureMaxLength {
		invalid.add("body", "must be at most 2000 characters")
	} else if strings.IndexFunc(body, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0 {
		invalid.add("body", "must not contain control characters")
	}
	if err = invalid.orNil(); err != nil {
		return domain.MailSignature{}, err
	}
	mailbox, err := s.authorizeMailbox(ctx, viewer, mailboxID, needSend)
	if err != nil {
		return domain.MailSignature{}, err
	}
	return store.SaveMailSignature(ctx, domain.MailSignature{MailboxID: mailbox.ID, UserID: viewer.UserID, Body: body}, s.now())
}

func needSend(access domain.MailboxAccess) bool { return access.Send }

func normalizeSignature(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	return strings.TrimRightFunc(body, unicode.IsSpace)
}
