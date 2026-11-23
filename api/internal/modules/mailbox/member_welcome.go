package mailbox

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// memberWelcomeTemplateKey is the template key welcome mails carry in the transactional send log.
// No mail template row backs it, so the log shows it with a null template id.
const memberWelcomeTemplateKey = "member-welcome"

const (
	// maxMemberWelcomeSubject and maxMemberWelcomeBody bound the organization's welcome copy.
	maxMemberWelcomeSubject = 200
	maxMemberWelcomeBody    = 5000
	// maxMemberWelcomeFromName is the longest sender name a transactional message stores.
	maxMemberWelcomeFromName = 100
)

// MemberWelcomeLanguage is the language of the standard welcome text and the fixed mail app
// section of every welcome mail.
type MemberWelcomeLanguage string

const (
	MemberWelcomeEnglish MemberWelcomeLanguage = "en-US"
	MemberWelcomeChinese MemberWelcomeLanguage = "zh-Hans"
)

// Valid reports whether the language is one welcome mails are written in.
func (l MemberWelcomeLanguage) Valid() bool {
	return l == MemberWelcomeEnglish || l == MemberWelcomeChinese
}

// memberWelcomeLanguages lists the languages in a stable order.
var memberWelcomeLanguages = []MemberWelcomeLanguage{MemberWelcomeChinese, MemberWelcomeEnglish}

// Welcome mail variables, the only ones the subject and body may use.
const (
	welcomeVariableMemberName       = "member_name"
	welcomeVariableMailboxAddress   = "mailbox_address"
	welcomeVariableOrganizationName = "organization_name"
	welcomeVariableSignInURL        = "sign_in_url"
)

// MemberWelcomeVariables lists the welcome variables in the order the console shows them.
var MemberWelcomeVariables = []string{
	welcomeVariableMemberName, welcomeVariableMailboxAddress, welcomeVariableOrganizationName, welcomeVariableSignInURL,
}

// MemberWelcomeTemplate is welcome copy: a subject and a plain-text body with {{variables}}.
type MemberWelcomeTemplate struct {
	Subject string
	Body    string
}

// memberWelcomeDefaults is the standard text of each language.
var memberWelcomeDefaults = map[MemberWelcomeLanguage]MemberWelcomeTemplate{
	MemberWelcomeEnglish: {
		Subject: "Welcome to {{organization_name}} on Zmail",
		Body: "Hi {{member_name}},\n\n" +
			"{{organization_name}} has set up a Zmail mailbox for you: {{mailbox_address}}\n\n" +
			"Sign in with the email address this message was sent to: {{sign_in_url}}\n" +
			"If you also received an invitation, accept it first. Your mailbox is ready as soon as you join.",
	},
	MemberWelcomeChinese: {
		Subject: "欢迎加入 {{organization_name}}",
		Body: "{{member_name}}，你好：\n\n" +
			"{{organization_name}} 已为你开通 Zmail 邮箱：{{mailbox_address}}\n\n" +
			"请用收到这封邮件的邮箱地址登录：{{sign_in_url}}\n" +
			"如果你还收到了加入邀请，请先接受邀请，加入后即可使用邮箱。",
	},
}

// MemberWelcomeDefault returns the standard text of a language.
func MemberWelcomeDefault(language MemberWelcomeLanguage) MemberWelcomeTemplate {
	if template, ok := memberWelcomeDefaults[language]; ok {
		return template
	}
	return memberWelcomeDefaults[MemberWelcomeChinese]
}

// MemberWelcomeSetting is an organization's welcome mail switch and copy. Subject and Body are
// what was saved; empty means the standard text of Language. UpdatedAt is nil until saved.
type MemberWelcomeSetting struct {
	Enabled   bool
	Language  MemberWelcomeLanguage
	Subject   string
	Body      string
	UpdatedAt *time.Time
}

// defaultMemberWelcomeSetting is the setting of an organization that never saved one.
func defaultMemberWelcomeSetting() MemberWelcomeSetting {
	return MemberWelcomeSetting{Enabled: true, Language: MemberWelcomeChinese}
}

// Effective is the copy a welcome mail is written with.
func (s MemberWelcomeSetting) Effective() MemberWelcomeTemplate {
	standard := MemberWelcomeDefault(s.Language)
	effective := MemberWelcomeTemplate{Subject: s.Subject, Body: s.Body}
	if strings.TrimSpace(effective.Subject) == "" {
		effective.Subject = standard.Subject
	}
	if strings.TrimSpace(effective.Body) == "" {
		effective.Body = standard.Body
	}
	return effective
}

// Customized reports whether the organization replaced any of the standard text.
func (s MemberWelcomeSetting) Customized() bool {
	return strings.TrimSpace(s.Subject) != "" || strings.TrimSpace(s.Body) != ""
}

// MemberWelcomeInput replaces an organization's welcome mail setting.
type MemberWelcomeInput struct {
	Enabled  bool
	Language MemberWelcomeLanguage
	Subject  string
	Body     string
}

// MemberWelcomePreviewInput is copy to preview; empty subject or body previews the standard text.
type MemberWelcomePreviewInput struct {
	Language MemberWelcomeLanguage
	Subject  string
	Body     string
}

// MemberWelcomePreview is a welcome mail rendered with sample values.
type MemberWelcomePreview struct {
	Subject string
	Text    string
	HTML    string
}

// validateMemberWelcomeCopy checks the language and that subject and body are well-formed
// templates within their limits that use only welcome variables.
func validateMemberWelcomeCopy(language MemberWelcomeLanguage, subject, body string) error {
	fields := &ValidationError{}
	if !language.Valid() {
		fields.add("language", "must be en-US or zh-Hans")
	}
	for _, field := range []struct {
		name, value string
		limit       int
	}{
		{"subject", subject, maxMemberWelcomeSubject},
		{"body", body, maxMemberWelcomeBody},
	} {
		if utf8.RuneCountInString(field.value) > field.limit {
			fields.add(field.name, "is longer than "+strconv.Itoa(field.limit)+" characters")
			continue
		}
		if field.name == "subject" && strings.ContainsAny(field.value, "\r\n") {
			fields.add(field.name, "must be one line")
			continue
		}
		parts, ok := parseTemplate(field.value)
		if !ok {
			fields.add(field.name, "has a malformed {{variable}}")
			continue
		}
		for _, part := range parts {
			if part.variable != "" && !isMemberWelcomeVariable(part.variable) {
				fields.add(field.name, "uses the unknown variable "+part.variable)
			}
		}
	}
	return fields.orNil()
}

func isMemberWelcomeVariable(name string) bool {
	for _, known := range MemberWelcomeVariables {
		if name == known {
			return true
		}
	}
	return false
}

// memberWelcomeValues are the values one welcome mail is written with.
type memberWelcomeValues struct {
	MemberName       string
	MailboxAddress   string
	OrganizationName string
	SignInURL        string
}

func (v memberWelcomeValues) variables() map[string]string {
	return map[string]string{
		welcomeVariableMemberName:       v.MemberName,
		welcomeVariableMailboxAddress:   v.MailboxAddress,
		welcomeVariableOrganizationName: v.OrganizationName,
		welcomeVariableSignInURL:        v.SignInURL,
	}
}

// memberWelcomeClientSetup is the public mail app server and the console page where a member
// creates a mail app password. It never holds a password or a token.
type memberWelcomeClientSetup struct {
	SettingsURL    string
	IMAPHost       string
	IMAPPort       int
	SMTPHost       string
	SMTPPort       int
	SubmissionPort int
}

// renderMemberWelcome writes a welcome mail: the organization's copy, then the fixed mail app
// section in the setting's language, which an admin cannot remove or get wrong. The body is plain
// text; the HTML part is the same text escaped, with the known links made clickable.
func renderMemberWelcome(
	template MemberWelcomeTemplate,
	language MemberWelcomeLanguage,
	values memberWelcomeValues,
	setup memberWelcomeClientSetup,
) (renderedTemplate, error) {
	subjectParts, ok := parseTemplate(template.Subject)
	if !ok {
		return renderedTemplate{}, fmt.Errorf("%w: welcome subject", ErrMailTemplateInvalid)
	}
	bodyParts, ok := parseTemplate(template.Body)
	if !ok {
		return renderedTemplate{}, fmt.Errorf("%w: welcome body", ErrMailTemplateInvalid)
	}
	variables := values.variables()
	// Every value is present, so nothing renders as missing; an empty name falls back here.
	if strings.TrimSpace(variables[welcomeVariableMemberName]) == "" {
		variables[welcomeVariableMemberName] = values.MailboxAddress
	}
	missing := map[string]struct{}{}
	subject := strings.TrimSpace(renderTemplate(subjectParts, variables, escapeLine, missing))
	body := strings.TrimSpace(renderTemplate(bodyParts, variables, escapeNone, missing))
	text := body + "\n\n" + memberWelcomeSetupText(language, values.MailboxAddress, setup)
	links := []string{values.SignInURL, setup.SettingsURL}
	return renderedTemplate{Subject: subject, Text: text, HTML: plainTextHTML(text, links)}, nil
}

// memberWelcomeSetupText is the fixed section that tells a member how to set up a mail app.
func memberWelcomeSetupText(language MemberWelcomeLanguage, address string, setup memberWelcomeClientSetup) string {
	imapPort, smtpPort, submissionPort := strconv.Itoa(setup.IMAPPort), strconv.Itoa(setup.SMTPPort), strconv.Itoa(setup.SubmissionPort)
	if language == MemberWelcomeEnglish {
		return "Set up a mail app\n\n" +
			"You can also read and send this mail in Apple Mail, Outlook, Thunderbird, or your phone's mail app.\n\n" +
			"1. Sign in to Zmail and open Settings > Mail apps: " + setup.SettingsURL + "\n" +
			"2. Create a mail app password for the device. It is only for the mail app, not your Zmail sign-in password.\n" +
			"3. Add an account in the mail app with these settings:\n" +
			"   User name: " + address + "\n" +
			"   Incoming mail (IMAP): " + setup.IMAPHost + ", port " + imapPort + ", SSL/TLS\n" +
			"   Outgoing mail (SMTP): " + setup.SMTPHost + ", port " + smtpPort + " (SSL/TLS) or " + submissionPort + " (STARTTLS)"
	}
	return "设置邮件客户端\n\n" +
		"你也可以在 Apple 邮件、Outlook、Thunderbird 或手机自带的邮件 App 中收发这个邮箱的邮件。\n\n" +
		"1. 登录 Zmail，打开“设置 > 邮件客户端”：" + setup.SettingsURL + "\n" +
		"2. 为这台设备创建一个客户端专用密码。它只用于邮件 App，不是你的 Zmail 登录密码。\n" +
		"3. 在邮件 App 中添加账户，填写：\n" +
		"   用户名：" + address + "\n" +
		"   收信服务器（IMAP）：" + setup.IMAPHost + "，端口 " + imapPort + "，SSL/TLS\n" +
		"   发信服务器（SMTP）：" + setup.SMTPHost + "，端口 " + smtpPort + "（SSL/TLS）或 " + submissionPort + "（STARTTLS）"
}

// plainTextHTML escapes text into paragraphs and line breaks and links each of links where it
// appears. Only URLs Zmail itself put into the mail become links.
func plainTextHTML(text string, links []string) string {
	escaped := html.EscapeString(text)
	for _, link := range links {
		if link == "" {
			continue
		}
		target := html.EscapeString(link)
		escaped = strings.ReplaceAll(escaped, target, `<a href="`+target+`">`+target+`</a>`)
	}
	paragraphs := strings.Split(escaped, "\n\n")
	var out strings.Builder
	for _, paragraph := range paragraphs {
		if strings.TrimSpace(paragraph) == "" {
			continue
		}
		out.WriteString("<p>")
		out.WriteString(strings.ReplaceAll(paragraph, "\n", "<br>\n"))
		out.WriteString("</p>\n")
	}
	return out.String()
}
