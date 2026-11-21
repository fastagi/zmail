package organization

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/infra/email"
)

// InvitationMailer is the organization-owned seam for invitation delivery.
type InvitationMailer interface {
	IsConfigured() bool
	SendInvitation(ctx context.Context, to, organizationName string, role domain.OrganizationRole, token string, expiresAt time.Time) error
}

type invitationEmailSender interface {
	IsConfigured() bool
	SendEmail(ctx context.Context, to []string, subject, htmlContent string) error
}

type invitationMailer struct {
	sender invitationEmailSender
	// publicURL is the Web console origin; when set the mail links straight to accepting.
	publicURL string
}

// NewInvitationMailer adapts the shared email capability to organization-owned copy. The console
// origin comes from MAILBOX_PUBLIC_URL, the same origin unsubscribe links use.
func NewInvitationMailer(service *email.Service, cfg *config.Config) InvitationMailer {
	publicURL := ""
	if cfg != nil {
		publicURL = cfg.Mailbox.PublicURL
	}
	return newInvitationMailer(service, publicURL)
}

func newInvitationMailer(sender invitationEmailSender, publicURL string) *invitationMailer {
	return &invitationMailer{sender: sender, publicURL: publicURL}
}

// acceptURL is the page that hands the token to the console accept dialog; empty
// without a known console origin.
func (m *invitationMailer) acceptURL(token string) string {
	if m.publicURL == "" {
		return ""
	}
	// The token rides in the fragment, which browsers never send to a server or a referrer.
	return m.publicURL + "/invite#" + url.QueryEscape(token)
}

func (m *invitationMailer) IsConfigured() bool {
	return m != nil && m.sender != nil && m.sender.IsConfigured()
}

func (m *invitationMailer) SendInvitation(
	ctx context.Context,
	to string,
	organizationName string,
	role domain.OrganizationRole,
	token string,
	expiresAt time.Time,
) error {
	if !m.IsConfigured() {
		return email.ErrNotConfigured
	}
	button := ""
	if link := m.acceptURL(token); link != "" {
		button = fmt.Sprintf(
			`<p><a href="%s">Join %s on Zmail</a></p>
		<p>New to Zmail? The link lets you sign up with this email address first.</p>
		<p>If the link does not open, sign in with this email address and enter this token:</p>`,
			html.EscapeString(link),
			html.EscapeString(organizationName),
		)
	} else {
		button = `<p>Sign in with this email address and enter the one-time token below:</p>`
	}
	htmlContent := fmt.Sprintf(`
		<h2>Organization invitation</h2>
		<p>You have been invited to join <strong>%s</strong> as <strong>%s</strong>.</p>
		%s
		<p><code>%s</code></p>
		<p>This invitation expires at %s.</p>
	`,
		html.EscapeString(organizationName),
		html.EscapeString(string(role)),
		button,
		html.EscapeString(token),
		html.EscapeString(expiresAt.UTC().Format(time.RFC3339)),
	)
	return m.sender.SendEmail(ctx, []string{to}, "Organization invitation", htmlContent)
}
