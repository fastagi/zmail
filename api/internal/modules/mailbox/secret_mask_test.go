package mailbox

import (
	"testing"
)

func TestMaskSecretsInTextMasksCodesNearKeywords(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"chinese code after keyword", "您的验证码是 482913，5 分钟内有效。", "您的验证码是 ••••••，5 分钟内有效。"},
		{"chinese colon", "验证码：4829", "验证码：••••••"},
		{"english subject", "Your verification code is 482913", "Your verification code is ••••••"},
		{"keyword after code", "482913 is your Medium login code.", "•••••• is your Medium login code."},
		{"spaced code", "Your code: 482 913", "Your code: ••••••"},
		{"hyphenated code", "Security code 482-913.", "Security code ••••••."},
		{"alphanumeric code", "Use OTP A7K9Q2 to continue", "Use OTP •••••• to continue"},
		{"lowercase alphanumeric", "passcode: k3x9p2", "passcode: ••••••"},
		{"2fa", "2FA token 99887766", "2FA token ••••••"},
		{"pin", "Your PIN is 1234.", "Your PIN is ••••••."},
		{"two codes", "Code 111222 or backup code 333444", "Code •••••• or backup code ••••••"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, masked := maskSecretsInText(tc.in)
			if got != tc.want || !masked {
				t.Fatalf("maskSecretsInText(%q) = %q, %v; want %q, true", tc.in, got, masked, tc.want)
			}
		})
	}
}

func TestMaskSecretsInTextLeavesOrdinaryNumbers(t *testing.T) {
	cases := []struct{ name, in string }{
		{"order number without keyword", "Order 48291377 has shipped."},
		{"shipping is not pin", "Shipping 12345 items today"},
		{"year near code word", "Code of conduct, updated 2026-11-20."},
		{"date", "Your code expires on 2026/11/20."},
		{"time", "Your code expires at 12:30."},
		{"decimal", "Verification fee 1234.56"},
		{"version", "Code release 1.2345.6"},
		{"money", "Your code saved you $1500"},
		{"yuan", "验证码服务费 ￥2000"},
		{"hex color", "code color #a1b2c3"},
		{"long number", "verification id 1234567890123"},
		{"short number", "code 123"},
		{"word without digit", "Your code: ABCDEF"},
		{"plain prose", "No secrets here."},
		{"far keyword", "Verification is important for everyone who signs in to the service today, 482913"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, masked := maskSecretsInText(tc.in)
			if got != tc.in || masked {
				t.Fatalf("maskSecretsInText(%q) = %q, %v; want unchanged", tc.in, got, masked)
			}
		})
	}
}

func TestMaskSecretsInTextMasksSensitiveLinks(t *testing.T) {
	in := "Reset here: https://accounts.example.com/reset?token=abc123 or visit https://example.com/blog/post"
	want := "Reset here: accounts.example.com/•••• or visit https://example.com/blog/post"
	got, masked := maskSecretsInText(in)
	if got != want || !masked {
		t.Fatalf("got %q, %v; want %q", got, masked, want)
	}
	unsubscribe := "Unsubscribe: https://lists.example.com/unsubscribe?u=42"
	if got, masked := maskSecretsInText(unsubscribe); got != unsubscribe || masked {
		t.Fatalf("unsubscribe link changed: %q", got)
	}
	magic := "Sign in with https://app.example.com/auth/magic/XyZ."
	if got, _ := maskSecretsInText(magic); got != "Sign in with app.example.com/••••." {
		t.Fatalf("magic link: %q", got)
	}
}

func TestMaskSecretsInHTMLMasksTextAndLinks(t *testing.T) {
	in := `<html><head><style>.c{color:#a1b2c3} .code{font:12px}</style></head>` +
		`<body><p style="color:#123456">您的验证码：</p><p><b>482913</b></p>` +
		`<a href="https://id.example.com/verify?t=XYZ" class="btn">Verify</a>` +
		`<a href="https://example.com/help">Help</a></body></html>`
	want := `<html><head><style>.c{color:#a1b2c3} .code{font:12px}</style></head>` +
		`<body><p style="color:#123456">您的验证码：</p><p><b>••••••</b></p>` +
		`<a href="https://id.example.com/••••" class="btn">Verify</a>` +
		`<a href="https://example.com/help">Help</a></body></html>`
	got, masked := maskSecretsInHTML(in)
	if got != want || !masked {
		t.Fatalf("maskSecretsInHTML:\n got %s\nwant %s", got, want)
	}
}

func TestMaskSecretsInHTMLKeepsUnchangedBytes(t *testing.T) {
	in := `<!DOCTYPE html><div class='x' data-a=1>Hello &amp; welcome<br/><img src="https://cdn.example.com/a.png"></div>`
	got, masked := maskSecretsInHTML(in)
	if got != in || masked {
		t.Fatalf("unchanged HTML was rewritten:\n got %s\nwant %s", got, in)
	}
}

func TestMaskSecretsInHTMLEscapesMaskedText(t *testing.T) {
	in := `<p>Code 482913 &lt;do not share&gt;</p>`
	want := `<p>Code •••••• &lt;do not share&gt;</p>`
	if got, _ := maskSecretsInHTML(in); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestMaskSnippetSourceDropsCutTail(t *testing.T) {
	if got := maskSnippetSource("Hello there 12", true); got != "Hello there " {
		t.Fatalf("truncated tail kept: %q", got)
	}
	if got := maskSnippetSource("Hello there 12", false); got != "Hello there 12" {
		t.Fatalf("whole source changed: %q", got)
	}
	if got := maskSnippetSource("验证码 482913 有效", false); got != "验证码 •••••• 有效" {
		t.Fatalf("snippet not masked: %q", got)
	}
}
