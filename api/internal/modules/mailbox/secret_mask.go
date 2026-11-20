package mailbox

import (
	"bytes"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/fastagi/zmail/api/internal/domain"
)

// Inbound mail often carries one-time codes and account links. Every response masks them; only
// the reveal endpoint returns the original. See docs/plans/verification-mask.zh-CN.md.
//
// Anything that hands inbound subjects or bodies to an AI model must take the masked output of
// these functions, never the stored original.

const (
	// secretContextBefore is how many characters before a code a keyword may end.
	secretContextBefore = 40
	// secretContextAfter is how many characters after a code a keyword may start.
	secretContextAfter = 20
)

// secretKeywords say a nearby number is a one-time code. ASCII keywords match whole words only.
var secretKeywords = []string{
	"\u9a8c\u8bc1\u7801",       // Chinese "verification code"
	"\u6821\u9a8c\u7801",       // Chinese "check code"
	"\u52a8\u6001\u7801",       // Chinese "dynamic code"
	"\u786e\u8ba4\u7801",       // Chinese "confirmation code"
	"\u767b\u5f55\u7801",       // Chinese "login code"
	"\u5b89\u5168\u7801",       // Chinese "security code"
	"\u6fc0\u6d3b\u7801",       // Chinese "activation code"
	"\u53e3\u4ee4",             // Chinese "passphrase"
	"\u52a8\u6001\u5bc6\u7801", // Chinese "dynamic password"
	"code", "otp", "passcode", "pin", "verification", "verify", "one-time", "2fa", "security code",
	"login code",
}

// sensitiveLinkParts mark a URL whose path or query can sign someone in or take over an account.
var sensitiveLinkParts = []string{
	"reset", "recover", "verify", "verification", "confirm", "magic", "token", "login", "signin",
	"sign-in", "auth", "otp", "password", "activate",
}

var textURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

// maskSecretsInText masks codes and sensitive links in plain text and reports whether it masked
// anything.
func maskSecretsInText(text string) (string, bool) {
	return maskSecretsWithContext("", text)
}

// maskSnippetSource masks the start of a text body that a snippet is cut from. When the source
// was cut short, the trailing letters or digits may be part of a longer code, so they are dropped.
func maskSnippetSource(source string, truncated bool) string {
	if truncated {
		runes := []rune(source)
		end := len(runes)
		for end > 0 && isASCIIAlnum(runes[end-1]) {
			end--
		}
		source = string(runes[:end])
	}
	masked, _ := maskSecretsInText(source)
	return masked
}

// maskSecretsInHTML masks codes in the text of an HTML body and sensitive links in its href
// attributes. Tokens it does not change are written back byte for byte.
func maskSecretsInHTML(body string) (string, bool) {
	if body == "" {
		return body, false
	}
	tokenizer := html.NewTokenizer(strings.NewReader(body))
	var out bytes.Buffer
	out.Grow(len(body))
	masked := false
	rawTag := ""
	context := ""
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			if tokenizer.Err() != io.EOF {
				out.Write(tokenizer.Raw())
			}
			break
		}
		raw := append([]byte(nil), tokenizer.Raw()...)
		switch kind {
		case html.TextToken:
			if rawTag != "" {
				out.Write(raw)
				continue
			}
			text := string(tokenizer.Text())
			replaced, changed := maskSecretsWithContext(context, text)
			context = lastRunes(context+text, secretContextBefore)
			if !changed {
				out.Write(raw)
				continue
			}
			masked = true
			out.WriteString(html.EscapeString(replaced))
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			if kind == html.StartTagToken {
				switch token.Data {
				case "style", "script", "textarea", "title":
					rawTag = token.Data
				}
			}
			changed := false
			for index, attribute := range token.Attr {
				if attribute.Namespace == "" && strings.EqualFold(attribute.Key, "href") {
					if replacement, ok := maskSensitiveLink(attribute.Val, true); ok {
						token.Attr[index].Val = replacement
						changed = true
					}
				}
			}
			if !changed {
				out.Write(raw)
				continue
			}
			masked = true
			out.WriteString(token.String())
		case html.EndTagToken:
			if rawTag != "" {
				name, _ := tokenizer.TagName()
				if string(name) == rawTag {
					rawTag = ""
				}
			}
			out.Write(raw)
		default:
			out.Write(raw)
		}
	}
	if !masked {
		return body, false
	}
	return out.String(), true
}

// maskSecretsWithContext masks text, reading keywords from the preceding context as well; only
// text itself is changed.
func maskSecretsWithContext(context, text string) (string, bool) {
	if text == "" {
		return text, false
	}
	linked, linksMasked, urlSpans := maskTextLinks(text)
	prefix := []rune(context)
	body := []rune(linked)
	all := append(append(make([]rune, 0, len(prefix)+len(body)), prefix...), body...)
	keywords := findSecretKeywords(all)
	if len(keywords) == 0 {
		return linked, linksMasked
	}
	offset := len(prefix)
	var out strings.Builder
	out.Grow(len(linked))
	codesMasked := false
	last := 0
	for _, token := range findCodeTokens(body, urlSpans) {
		if !nearKeyword(keywords, token.start+offset, token.end+offset) {
			continue
		}
		out.WriteString(string(body[last:token.start]))
		out.WriteString(maskedSecret)
		last = token.end
		codesMasked = true
	}
	if !codesMasked {
		return linked, linksMasked
	}
	out.WriteString(string(body[last:]))
	return out.String(), true
}

// runeSpan is a half-open range of rune offsets.
type runeSpan struct{ start, end int }

// maskTextLinks replaces sensitive URLs in text with their host and returns the rune spans of the
// URLs it kept, where codes are not looked for.
func maskTextLinks(text string) (string, bool, []runeSpan) {
	matches := textURLPattern.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return text, false, nil
	}
	var out strings.Builder
	masked := false
	var kept []runeSpan
	last := 0
	runeCount := 0
	for _, match := range matches {
		start, end := match[0], trimURLEnd(text, match[0], match[1])
		out.WriteString(text[last:start])
		runeCount += len([]rune(text[last:start]))
		link := text[start:end]
		if replacement, ok := maskSensitiveLink(link, false); ok {
			out.WriteString(replacement)
			runeCount += len([]rune(replacement))
			masked = true
		} else {
			out.WriteString(link)
			length := len([]rune(link))
			kept = append(kept, runeSpan{runeCount, runeCount + length})
			runeCount += length
		}
		last = end
	}
	out.WriteString(text[last:])
	return out.String(), masked, kept
}

// trimURLEnd drops trailing punctuation that ends a sentence rather than the URL.
func trimURLEnd(text string, start, end int) int {
	for end > start && strings.ContainsRune(".,;:!?)]}", rune(text[end-1])) {
		end--
	}
	return end
}

// maskSensitiveLink returns the masked form of an http or https URL whose path or query names a
// sign-in or recovery action. keepScheme keeps "https://" so an href stays a URL.
func maskSensitiveLink(link string, keepScheme bool) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	if (scheme != "http" && scheme != "https") || host == "" {
		return "", false
	}
	rest := strings.ToLower(parsed.EscapedPath() + "?" + parsed.RawQuery + "#" + parsed.Fragment)
	sensitive := false
	for _, part := range sensitiveLinkParts {
		if strings.Contains(rest, part) {
			sensitive = true
			break
		}
	}
	if !sensitive {
		return "", false
	}
	if keepScheme {
		return scheme + "://" + host + maskedURLPath, true
	}
	return host + maskedURLPath, true
}

// findSecretKeywords returns where each keyword occurs in text.
func findSecretKeywords(text []rune) []runeSpan {
	lower := make([]rune, len(text))
	for index, r := range text {
		if r < 0x80 && 'A' <= r && r <= 'Z' {
			r += 'a' - 'A'
		}
		lower[index] = r
	}
	var spans []runeSpan
	for _, keyword := range secretKeywords {
		needle := []rune(keyword)
		ascii := needle[0] < 0x80
		for start := 0; start+len(needle) <= len(lower); start++ {
			if !runesEqual(lower[start:start+len(needle)], needle) {
				continue
			}
			end := start + len(needle)
			if ascii && ((start > 0 && isASCIIAlnum(lower[start-1])) || (end < len(lower) && isASCIIAlnum(lower[end]))) {
				continue
			}
			spans = append(spans, runeSpan{start, end})
		}
	}
	return spans
}

func nearKeyword(keywords []runeSpan, start, end int) bool {
	for _, keyword := range keywords {
		if keyword.end <= start && start-keyword.end <= secretContextBefore {
			return true
		}
		if keyword.start >= end && keyword.start-end <= secretContextAfter {
			return true
		}
	}
	return false
}

// findCodeTokens returns the spans of text that look like one-time codes, outside the given URL
// spans.
func findCodeTokens(text []rune, skip []runeSpan) []runeSpan {
	var tokens []runeSpan
	for index := 0; index < len(text); {
		if !isASCIIAlnum(text[index]) {
			index++
			continue
		}
		start := index
		for index < len(text) && isASCIIAlnum(text[index]) {
			index++
		}
		end := index
		if insideSpans(skip, start) {
			continue
		}
		// "482 913" and "482-913" are one code.
		if end-start == 3 && allDigits(text[start:end]) && end+4 <= len(text) &&
			(text[end] == ' ' || text[end] == '-') && allDigits(text[end+1:end+4]) &&
			(end+4 == len(text) || !isASCIIAlnum(text[end+4])) && standsAlone(text, start, end+4) {
			tokens = append(tokens, runeSpan{start, end + 4})
			index = end + 4
			continue
		}
		if codeShaped(text[start:end]) && standsAlone(text, start, end) {
			tokens = append(tokens, runeSpan{start, end})
		}
	}
	return tokens
}

// codeShaped is 4–8 digits, or 6–8 letters and digits with at least one of each.
func codeShaped(token []rune) bool {
	digits, letters := 0, 0
	for _, r := range token {
		if '0' <= r && r <= '9' {
			digits++
		} else {
			letters++
		}
	}
	if letters == 0 {
		return digits >= 4 && digits <= 8
	}
	return digits > 0 && len(token) >= 6 && len(token) <= 8
}

// standsAlone rejects a token that is part of a color, date, time, version, decimal, or amount.
func standsAlone(text []rune, start, end int) bool {
	if start > 0 {
		before := text[start-1]
		if strings.ContainsRune("#$¥￥€£", before) {
			return false
		}
		if strings.ContainsRune("-/.:", before) && start > 1 && isASCIIDigit(text[start-2]) {
			return false
		}
	}
	if end < len(text)-1 {
		after := text[end]
		if strings.ContainsRune("-/.:", after) && isASCIIDigit(text[end+1]) {
			return false
		}
	}
	return true
}

func insideSpans(spans []runeSpan, position int) bool {
	for _, span := range spans {
		if position >= span.start && position < span.end {
			return true
		}
	}
	return false
}

func allDigits(runes []rune) bool {
	for _, r := range runes {
		if !isASCIIDigit(r) {
			return false
		}
	}
	return true
}

func runesEqual(a, b []rune) bool {
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func isASCIIDigit(r rune) bool { return '0' <= r && r <= '9' }

func isASCIIAlnum(r rune) bool {
	return isASCIIDigit(r) || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')
}

func lastRunes(text string, count int) string {
	runes := []rune(text)
	if len(runes) <= count {
		return text
	}
	return string(runes[len(runes)-count:])
}

// maskedContent is the subject and bodies of a message as responses show them.
type maskedContent struct {
	Subject, TextBody, HTMLBody string
	// Masked is true when anything was masked.
	Masked bool
}

// maskInboundSecrets masks the subject and bodies of an inbound message; any other message is
// returned as stored. The message itself is not changed.
func maskInboundSecrets(message *domain.MailMessage) maskedContent {
	content := maskedContent{Subject: message.Subject, TextBody: message.TextBody, HTMLBody: message.HTMLBody}
	if message.Direction != domain.MailDirectionInbound {
		return content
	}
	var subjectMasked, textMasked, htmlMasked bool
	content.Subject, subjectMasked = maskSecretsInText(message.Subject)
	content.TextBody, textMasked = maskSecretsInText(message.TextBody)
	content.HTMLBody, htmlMasked = maskSecretsInHTML(message.HTMLBody)
	content.Masked = subjectMasked || textMasked || htmlMasked
	return content
}

// maskedSubject is the subject a listing shows.
func maskedSubject(message *domain.MailMessage) string {
	if message.Direction != domain.MailDirectionInbound {
		return message.Subject
	}
	subject, _ := maskSecretsInText(message.Subject)
	return subject
}

// inboxItemSnippet is the snippet of a unified inbox row, cut from the start of the text body;
// an inbound source is masked first.
func inboxItemSnippet(message *domain.MailMessage, source string) string {
	if message.Direction == domain.MailDirectionInbound {
		source = maskSnippetSource(source, utf8.RuneCountInString(source) >= inboxSnippetSource)
	}
	return inboxSnippet(source)
}
