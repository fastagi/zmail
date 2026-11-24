package mailbox

import (
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestDecodeMIMEHeader(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "utf8 base64 8341Token工厂",
			raw:  "=?UTF-8?B?ODM0MVRva2Vu5bel5Y6C?=",
			want: "8341Token工厂",
		},
		{
			name: "utf8 base64 利弗莫尔证券",
			raw:  "=?UTF-8?B?5Yip5byX6I6r5bCU6K+B5Yi4?=",
			want: "利弗莫尔证券",
		},
		{
			name: "iso-8859-1 quoted-printable",
			raw:  "=?ISO-8859-1?Q?Ren=E9_Descartes?=",
			want: "René Descartes",
		},
		{
			name: "adjacent utf8 encoded words",
			raw:  "=?UTF-8?B?ODM0MVRva2Vu?= =?UTF-8?B?5bel5Y6C?=",
			want: "8341Token工厂",
		},
		{
			name: "unknown charset falls back gracefully",
			raw:  "=?x-unknown-charset?B?SGVsbG8=?=",
			want: "=?x-unknown-charset?B?SGVsbG8=?=",
		},
		{
			name: "mixed valid utf8 and unknown charset",
			raw:  "=?UTF-8?B?ODM0MVRva2Vu5bel5Y6C?= =?x-unknown-charset?B?SGVsbG8=?=",
			want: "8341Token工厂 =?x-unknown-charset?B?SGVsbG8=?=",
		},
		{
			name: "plain text unchanged",
			raw:  "Alice Example",
			want: "Alice Example",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decodeMIMEHeader(tc.raw); got != tc.want {
				t.Fatalf("decodeMIMEHeader(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseMailDecodesQuotedAndUnquotedRFC2047FromAndSubject(t *testing.T) {
	cases := []struct {
		name        string
		fromHeader  string
		subjHeader  string
		wantAddress string
		wantName    string
		wantSubject string
	}{
		{
			name:        "quoted utf8 base64 8341Token工厂",
			fromHeader:  `"=?UTF-8?B?ODM0MVRva2Vu5bel5Y6C?=" <noreply@8341token.example>`,
			subjHeader:  `=?UTF-8?B?ODM0MVRva2Vu5bel5Y6C?= Notice`,
			wantAddress: "noreply@8341token.example",
			wantName:    "8341Token工厂",
			wantSubject: "8341Token工厂 Notice",
		},
		{
			name:        "quoted utf8 base64 利弗莫尔证券",
			fromHeader:  `"=?UTF-8?B?5Yip5byX6I6r5bCU6K+B5Yi4?=" <service@livermore.example>`,
			subjHeader:  `=?UTF-8?B?5Yip5byX6I6r5bCU6K+B5Yi4?=`,
			wantAddress: "service@livermore.example",
			wantName:    "利弗莫尔证券",
			wantSubject: "利弗莫尔证券",
		},
		{
			name:        "unquoted utf8 base64",
			fromHeader:  `=?UTF-8?B?5Yip5byX6I6r5bCU6K+B5Yi4?= <service@livermore.example>`,
			subjHeader:  `=?ISO-8859-1?Q?Ren=E9_Alert?=`,
			wantAddress: "service@livermore.example",
			wantName:    "利弗莫尔证券",
			wantSubject: "René Alert",
		},
		{
			name:        "unknown charset preserves from address",
			fromHeader:  `=?x-unknown-charset?B?SGVsbG8=?= <fallback@sender.example>`,
			subjHeader:  `=?x-unknown-charset?B?SGVsbG8=?=`,
			wantAddress: "fallback@sender.example",
			wantName:    "Hello",
			wantSubject: "=?x-unknown-charset?B?SGVsbG8=?=",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := parseMail(crlf(
				"From: "+tc.fromHeader,
				"To: user@zmail.example",
				"Subject: "+tc.subjHeader,
				"",
				"body",
			))
			if err != nil {
				t.Fatalf("parseMail: %v", err)
			}
			if parsed.FromAddress != tc.wantAddress || parsed.FromName != tc.wantName || parsed.Subject != tc.wantSubject {
				t.Fatalf("got from=(%q, %q) subject=%q; want (%q, %q) %q",
					parsed.FromAddress, parsed.FromName, parsed.Subject,
					tc.wantAddress, tc.wantName, tc.wantSubject)
			}
		})
	}
}

func TestExternalConnectorsDecodeRFC2047SenderNames(t *testing.T) {
	gmail := gmailMessage{
		ID: "g-1",
		Payload: gmailMessagePart{
			Headers: []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}{
				{Name: "From", Value: `"=?UTF-8?B?ODM0MVRva2Vu5bel5Y6C?=" <noreply@8341token.example>`},
				{Name: "Subject", Value: `=?UTF-8?B?5Yip5byX6I6r5bCU6K+B5Yi4?=`},
			},
		},
	}.external()
	if gmail.FromAddress != "noreply@8341token.example" || gmail.FromName != "8341Token工厂" || gmail.Subject != "利弗莫尔证券" {
		t.Fatalf("gmail external = %+v", gmail)
	}

	outlookRecipientFrom := &outlookRecipient{}
	outlookRecipientFrom.EmailAddress.Address = "service@livermore.example"
	outlookRecipientFrom.EmailAddress.Name = "=?UTF-8?B?5Yip5byX6I6r5bCU6K+B5Yi4?="
	outlook, ok := (outlookMessage{
		ID:      "o-1",
		Subject: "=?UTF-8?B?ODM0MVRva2Vu5bel5Y6C?=",
		From:    outlookRecipientFrom,
	}).message()
	if !ok || outlook.FromAddress != "service@livermore.example" || outlook.FromName != "利弗莫尔证券" || outlook.Subject != "8341Token工厂" {
		t.Fatalf("outlook message = %+v (ok=%v)", outlook, ok)
	}

	if domain.MaxExternalMailBackfillDays != 3650 {
		t.Fatalf("MaxExternalMailBackfillDays = %d, want 3650", domain.MaxExternalMailBackfillDays)
	}
}
