package mailbox

import "fmt"

// authenticateAndIdentify runs LOGIN followed by a best-effort RFC 2971 IMAP ID command so
// providers like NetEase 163/126/yeah.net allow EXAMINE INBOX instead of refusing with
// "Unsafe Login". Servers without RFC 2971 reply BAD or NO, which is ignored.
func (s *imapSession) authenticateAndIdentify(username, password string) error {
	if err := s.login(username, password); err != nil {
		return err
	}
	if err := s.send(`ID ("name" "Zmail" "version" "1.0" "vendor" "Zmail")`); err != nil {
		return fmt.Errorf("%w: %v", ErrExternalMailServerUnreachable, err)
	}
	if _, err := s.finish(imapHandlers{}); err != nil {
		return fmt.Errorf("%w: %v", ErrExternalMailServerUnreachable, err)
	}
	return nil
}
