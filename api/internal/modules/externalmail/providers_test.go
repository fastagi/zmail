package externalmail

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestDetectProviderKnownDomains(t *testing.T) {
	tests := []struct {
		email        string
		wantProvider string
		wantAuth     AuthStrategy
		wantOAuth2   bool
		wantAppPwd   bool
		wantIMAPHost string
		wantIMAPPort int
		wantIMAPTLS  TLSSecurity
		wantSMTPHost string
		wantSMTPPort int
		wantSMTPTLS  TLSSecurity
	}{
		{"alice@gmail.com", "gmail", AuthStrategyOAuth2, true, false, "imap.gmail.com", 993, TLSModeImplicit, "smtp.gmail.com", 465, TLSModeImplicit},
		{"alice@googlemail.com", "gmail", AuthStrategyOAuth2, true, false, "imap.gmail.com", 993, TLSModeImplicit, "smtp.gmail.com", 465, TLSModeImplicit},
		{"bob@outlook.com", "outlook", AuthStrategyOAuth2, true, false, "outlook.office365.com", 993, TLSModeImplicit, "smtp.office365.com", 587, TLSModeSTARTTLS},
		{"bob@hotmail.com", "outlook", AuthStrategyOAuth2, true, false, "outlook.office365.com", 993, TLSModeImplicit, "smtp.office365.com", 587, TLSModeSTARTTLS},
		{"bob@live.com", "outlook", AuthStrategyOAuth2, true, false, "outlook.office365.com", 993, TLSModeImplicit, "smtp.office365.com", 587, TLSModeSTARTTLS},
		{"bob@msn.com", "outlook", AuthStrategyOAuth2, true, false, "outlook.office365.com", 993, TLSModeImplicit, "smtp.office365.com", 587, TLSModeSTARTTLS},
		{"carol@icloud.com", "icloud", AuthStrategyAppPassword, false, true, "imap.mail.me.com", 993, TLSModeImplicit, "smtp.mail.me.com", 587, TLSModeSTARTTLS},
		{"carol@me.com", "icloud", AuthStrategyAppPassword, false, true, "imap.mail.me.com", 993, TLSModeImplicit, "smtp.mail.me.com", 587, TLSModeSTARTTLS},
		{"carol@mac.com", "icloud", AuthStrategyAppPassword, false, true, "imap.mail.me.com", 993, TLSModeImplicit, "smtp.mail.me.com", 587, TLSModeSTARTTLS},
		{"dave@qq.com", "qq", AuthStrategyAppPassword, false, true, "imap.qq.com", 993, TLSModeImplicit, "smtp.qq.com", 465, TLSModeImplicit},
		{"dave@foxmail.com", "qq", AuthStrategyAppPassword, false, true, "imap.qq.com", 993, TLSModeImplicit, "smtp.qq.com", 465, TLSModeImplicit},
		{"erin@163.com", "netease163", AuthStrategyAppPassword, false, true, "imap.163.com", 993, TLSModeImplicit, "smtp.163.com", 465, TLSModeImplicit},
		{"erin@126.com", "netease163", AuthStrategyAppPassword, false, true, "imap.126.com", 993, TLSModeImplicit, "smtp.126.com", 465, TLSModeImplicit},
		{"erin@yeah.net", "netease163", AuthStrategyAppPassword, false, true, "imap.yeah.net", 993, TLSModeImplicit, "smtp.yeah.net", 465, TLSModeImplicit},
		{"frank@fastmail.com", "fastmail", AuthStrategyAppPassword, false, true, "imap.fastmail.com", 993, TLSModeImplicit, "smtp.fastmail.com", 465, TLSModeImplicit},
		{"frank@fastmail.fm", "fastmail", AuthStrategyAppPassword, false, true, "imap.fastmail.com", 993, TLSModeImplicit, "smtp.fastmail.com", 465, TLSModeImplicit},
		{"grace@yahoo.com", "yahoo", AuthStrategyAppPassword, false, true, "imap.mail.yahoo.com", 993, TLSModeImplicit, "smtp.mail.yahoo.com", 465, TLSModeImplicit},
		{"grace@ymail.com", "yahoo", AuthStrategyAppPassword, false, true, "imap.mail.yahoo.com", 993, TLSModeImplicit, "smtp.mail.yahoo.com", 465, TLSModeImplicit},
		{"heidi@zoho.com", "zoho", AuthStrategyAppPassword, false, true, "imap.zoho.com", 993, TLSModeImplicit, "smtp.zoho.com", 465, TLSModeImplicit},
		{"heidi@zohomail.com", "zoho", AuthStrategyAppPassword, false, true, "imap.zoho.com", 993, TLSModeImplicit, "smtp.zoho.com", 465, TLSModeImplicit},
		{"ivan@exmail.qq.com", "exmail", AuthStrategyAppPassword, false, true, "imap.exmail.qq.com", 993, TLSModeImplicit, "smtp.exmail.qq.com", 465, TLSModeImplicit},
		{"judy@feishu.cn", "feishu", AuthStrategyAppPassword, false, true, "imap.feishu.cn", 993, TLSModeImplicit, "smtp.feishu.cn", 465, TLSModeImplicit},
		{"judy@larksuite.com", "feishu", AuthStrategyAppPassword, false, true, "imap.feishu.cn", 993, TLSModeImplicit, "smtp.feishu.cn", 465, TLSModeImplicit},
		{"ken@aliyun.com", "aliyun", AuthStrategyAppPassword, false, true, "imap.aliyun.com", 993, TLSModeImplicit, "smtp.aliyun.com", 465, TLSModeImplicit},
		{"li@sina.com", "sina", AuthStrategyAppPassword, false, true, "imap.sina.com", 993, TLSModeImplicit, "smtp.sina.com", 465, TLSModeImplicit},
		{"li@sina.cn", "sina", AuthStrategyAppPassword, false, true, "imap.sina.com", 993, TLSModeImplicit, "smtp.sina.com", 465, TLSModeImplicit},
	}

	for _, tc := range tests {
		t.Run(tc.email, func(t *testing.T) {
			got := DetectProvider(tc.email)
			if !got.Valid || !got.Matched {
				t.Fatalf("DetectProvider(%q): expected Valid=true Matched=true, got %+v", tc.email, got)
			}
			if got.ProviderID != tc.wantProvider {
				t.Errorf("ProviderID = %q, want %q", got.ProviderID, tc.wantProvider)
			}
			if got.AuthStrategy != tc.wantAuth {
				t.Errorf("AuthStrategy = %q, want %q", got.AuthStrategy, tc.wantAuth)
			}
			if got.RequiresOAuth2 != tc.wantOAuth2 {
				t.Errorf("RequiresOAuth2 = %v, want %v", got.RequiresOAuth2, tc.wantOAuth2)
			}
			if got.RequiresAppPassword != tc.wantAppPwd {
				t.Errorf("RequiresAppPassword = %v, want %v", got.RequiresAppPassword, tc.wantAppPwd)
			}
			if got.IMAP.Host != tc.wantIMAPHost || got.IMAP.Port != tc.wantIMAPPort || got.IMAP.TLS != tc.wantIMAPTLS {
				t.Errorf("IMAP = %+v, want {%s %d %s}", got.IMAP, tc.wantIMAPHost, tc.wantIMAPPort, tc.wantIMAPTLS)
			}
			if got.SMTP.Host != tc.wantSMTPHost || got.SMTP.Port != tc.wantSMTPPort || got.SMTP.TLS != tc.wantSMTPTLS {
				t.Errorf("SMTP = %+v, want {%s %d %s}", got.SMTP, tc.wantSMTPHost, tc.wantSMTPPort, tc.wantSMTPTLS)
			}
			if !got.Capabilities.Read || !got.Capabilities.Send {
				t.Errorf("expected Read and Send capabilities enabled, got %+v", got.Capabilities)
			}
			if !strings.HasPrefix(got.HelpURL, "https://") {
				t.Errorf("expected https HelpURL, got %q", got.HelpURL)
			}
		})
	}
}

func TestDetectProviderNormalization(t *testing.T) {
	cases := []struct {
		raw        string
		wantEmail  string
		wantDomain string
		wantID     string
	}{
		{"  Alice.Smith@GMAIL.COM  ", "alice.smith@gmail.com", "gmail.com", "gmail"},
		{"\tBOB+tag@FoxMail.COM\n", "bob+tag@foxmail.com", "foxmail.com", "qq"},
		{"  CAROL@126.COM ", "carol@126.com", "126.com", "netease163"},
		{"  Dan@Acme-Corp.IO\r\n", "dan@acme-corp.io", "acme-corp.io", "imap"},
	}

	for _, tc := range cases {
		got := DetectProvider(tc.raw)
		if !got.Valid {
			t.Fatalf("DetectProvider(%q) expected Valid=true", tc.raw)
		}
		if got.Email != tc.wantEmail || got.Domain != tc.wantDomain || got.ProviderID != tc.wantID {
			t.Errorf("DetectProvider(%q) = (email=%q domain=%q id=%q), want (%q, %q, %q)",
				tc.raw, got.Email, got.Domain, got.ProviderID, tc.wantEmail, tc.wantDomain, tc.wantID)
		}
	}
}

func TestDetectProviderInvalidEmails(t *testing.T) {
	invalid := []string{
		"",
		"   ",
		"not-an-email",
		"@gmail.com",
		"alice@",
		"a@b@gmail.com",
		"alice@localhost",
		"alice@.com",
		"alice@example..com",
		"alice@-example.com",
		"alice@example-.com",
		"alice @gmail.com",
		".alice@gmail.com",
		"alice.@gmail.com",
		"al..ice@gmail.com",
		"alice@example.c",
		"alice@exa_mple.com",
	}

	for _, raw := range invalid {
		got := DetectProvider(raw)
		if got.Valid || got.Matched || got.ProviderID != "" {
			t.Errorf("DetectProvider(%q) expected invalid result, got %+v", raw, got)
		}
	}
}

func TestDetectProviderGenericIMAPFallback(t *testing.T) {
	cases := []struct {
		email    string
		domain   string
		imapHost string
		smtpHost string
	}{
		{"ceo@acme.io", "acme.io", "imap.acme.io", "smtp.acme.io"},
		{"ops@mail.corp.example.co.uk", "mail.corp.example.co.uk", "imap.mail.corp.example.co.uk", "smtp.mail.corp.example.co.uk"},
	}

	for _, tc := range cases {
		got := DetectProvider(tc.email)
		if !got.Valid {
			t.Fatalf("DetectProvider(%q): expected Valid=true", tc.email)
		}
		if got.Matched {
			t.Errorf("DetectProvider(%q): expected Matched=false for custom domain", tc.email)
		}
		if got.ProviderID != "imap" {
			t.Errorf("ProviderID = %q, want imap", got.ProviderID)
		}
		if got.AuthStrategy != AuthStrategyPassword || got.RequiresOAuth2 || got.RequiresAppPassword {
			t.Errorf("unexpected auth flags for generic fallback: %+v", got)
		}
		if got.IMAP.Host != tc.imapHost || got.IMAP.Port != 993 || got.IMAP.TLS != TLSModeImplicit {
			t.Errorf("IMAP = %+v, want %s:993/tls", got.IMAP, tc.imapHost)
		}
		if got.SMTP.Host != tc.smtpHost || got.SMTP.Port != 465 || got.SMTP.TLS != TLSModeImplicit {
			t.Errorf("SMTP = %+v, want %s:465/tls", got.SMTP, tc.smtpHost)
		}
	}
}

func TestListPresetsDeterministicAndIsolated(t *testing.T) {
	expectedOrder := []string{
		"gmail", "outlook", "icloud", "qq", "netease163", "fastmail",
		"yahoo", "zoho", "exmail", "feishu", "aliyun", "sina", "imap",
	}

	first := ListPresets()
	if len(first) != len(expectedOrder) {
		t.Fatalf("len(ListPresets()) = %d, want %d", len(first), len(expectedOrder))
	}
	for i, wantID := range expectedOrder {
		if first[i].ID != wantID {
			t.Errorf("preset[%d].ID = %q, want %q", i, first[i].ID, wantID)
		}
	}

	// Mutating returned slices must not affect subsequent calls.
	first[0].Domains[0] = "mutated.invalid"

	second := ListPresets()
	if second[0].Domains[0] != "gmail.com" {
		t.Fatalf("registry state was mutated through returned slice: %v", second[0].Domains)
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("preset order changed at index %d: %q vs %q", i, first[i].ID, second[i].ID)
		}
	}
}

func TestProviderRegistryRegisterAndGet(t *testing.T) {
	reg := NewProviderRegistry()

	if err := reg.Register(nil); err == nil {
		t.Fatal("expected error registering nil strategy")
	}
	if err := reg.Register(newPresetStrategy(ProviderSpec{ID: "  "}, nil)); err == nil {
		t.Fatal("expected error registering empty ID strategy")
	}

	custom := newPresetStrategy(ProviderSpec{
		ID:           "proton",
		DisplayName:  "Proton Mail Bridge",
		Region:       "global",
		Domains:      []string{"proton.me", "protonmail.com"},
		AuthStrategy: AuthStrategyAppPassword,
		IMAP:         ServerEndpoint{Host: "127.0.0.1", Port: 1143, TLS: TLSModeSTARTTLS},
		SMTP:         ServerEndpoint{Host: "127.0.0.1", Port: 1025, TLS: TLSModeSTARTTLS},
		Capabilities: ProviderCapabilities{Read: true, Send: true, AppPassword: true},
		HelpURL:      "https://proton.me/mail/bridge",
	}, nil)

	if err := reg.Register(custom); err != nil {
		t.Fatalf("Register custom failed: %v", err)
	}

	spec, ok := reg.Get("PROTON")
	if !ok || spec.ID != "proton" {
		t.Fatalf("Get(PROTON) = (%+v, %v), want proton", spec, ok)
	}
	if _, ok := reg.Get("nonexistent"); ok {
		t.Fatal("expected nonexistent provider lookup to return false")
	}

	res := reg.DetectProvider("alice@proton.me")
	if !res.Valid || !res.Matched || res.ProviderID != "proton" || !res.RequiresAppPassword {
		t.Fatalf("unexpected custom DetectProvider result: %+v", res)
	}
}

func TestProviderRegistryConcurrentAccess(t *testing.T) {
	reg := NewProviderRegistry()
	emails := []string{
		"a@gmail.com",
		"b@outlook.com",
		"c@qq.com",
		"d@126.com",
		"e@custom-enterprise.io",
		"invalid-email",
	}

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				email := emails[(worker+i)%len(emails)]
				res := reg.DetectProvider(email)
				if email == "invalid-email" && res.Valid {
					t.Errorf("expected invalid for %q", email)
				}
				if email != "invalid-email" && !res.Valid {
					t.Errorf("expected valid for %q", email)
				}
				if presets := reg.ListPresets(); len(presets) < 13 {
					t.Errorf("expected >= 13 presets, got %d", len(presets))
				}
				if _, ok := reg.Get("gmail"); !ok {
					t.Errorf("expected gmail preset to exist")
				}
				if worker%4 == 0 && i%25 == 0 {
					id := fmt.Sprintf("custom-%d", worker)
					_ = reg.Register(newPresetStrategy(ProviderSpec{
						ID:           id,
						DisplayName:  id,
						Domains:      []string{fmt.Sprintf("%s.example.com", id)},
						AuthStrategy: AuthStrategyAppPassword,
						IMAP:         ServerEndpoint{Host: "imap.example.com", Port: 993, TLS: TLSModeImplicit},
						SMTP:         ServerEndpoint{Host: "smtp.example.com", Port: 465, TLS: TLSModeImplicit},
					}, nil))
				}
			}
		}(g)
	}
	wg.Wait()
}
