import { describe, expect, it } from 'vitest';

import { extractMagicLoginLink } from '@/features/mailbox/utils/magic-link';

describe('extractMagicLoginLink', () => {
  it('extracts a Claude "Sign in to Claude" magic link from HTML and decodes entities', () => {
    const html = `
      <div>
        <p>Click the button below to sign in to your Claude account.</p>
        <a href="https://claude.ai/magic-link#token_abc123&amp;next=%2Fchats" style="padding:12px 24px">
          <span>Sign in to Claude</span>
        </a>
        <footer>
          <a href="https://support.anthropic.com/en/articles/login">Need help? Contact Support</a>
          <a href="https://claude.ai/legal/privacy">Privacy Policy</a>
        </footer>
      </div>
    `;

    expect(extractMagicLoginLink(html, null)).toEqual({
      url: 'https://claude.ai/magic-link#token_abc123&next=%2Fchats',
      label: 'Sign in to Claude',
      host: 'claude.ai',
    });
  });

  it('extracts Chinese sign-in links from HTML', () => {
    const html = `
      <p>点击下方按钮立即登录您的账号：</p>
      <a href="https://app.example.com/auth/callback?token=xyz789">立即登录</a>
      <a href="https://app.example.com/unsubscribe">退订邮件</a>
    `;

    expect(extractMagicLoginLink(html, null)).toEqual({
      url: 'https://app.example.com/auth/callback?token=xyz789',
      label: '立即登录',
      host: 'app.example.com',
    });
  });

  it('extracts plain-text login links when HTML is absent', () => {
    const text = `
Hi there,

Use this one-time link to sign in to Notion:
https://www.notion.so/login?token=secret_magic_token_999.

If you didn't request this, contact support at https://support.notion.so/help.
    `;

    expect(extractMagicLoginLink(null, text)).toEqual({
      url: 'https://www.notion.so/login?token=secret_magic_token_999',
      label: 'notion.so',
      host: 'notion.so',
    });
  });

  it('ignores Support, Unsubscribe, Privacy, Terms, and mailto links', () => {
    const html = `
      <div>
        <a href="mailto:support@example.com">Sign in Support</a>
        <a href="https://support.example.com/login">Sign in to Support</a>
        <a href="https://example.com/unsubscribe?token=abc123">Unsubscribe</a>
        <a href="https://example.com/privacy?token=abc123">Verify Privacy Policy</a>
        <a href="https://example.com/terms?token=abc123">Confirm Terms</a>
      </div>
    `;

    expect(extractMagicLoginLink(html, null)).toBeNull();
  });

  it('rejects insecure http:// links unless localhost', () => {
    const insecureHtml = `<a href="http://evil.example.com/login?token=abc">Sign in</a>`;
    expect(extractMagicLoginLink(insecureHtml, null)).toBeNull();

    const localhostHtml = `<a href="http://localhost:3000/auth/verify?token=dev123">Verify email</a>`;
    expect(extractMagicLoginLink(localhostHtml, null)).toEqual({
      url: 'http://localhost:3000/auth/verify?token=dev123',
      label: 'Verify email',
      host: 'localhost',
    });
  });

  it('ignores redacted or masked placeholder links', () => {
    const redactedHtml = `
      <a href="https://claude.ai/magic-link?token=[REDACTED]">Sign in to Claude</a>
      <a href="https://linear.app/login?token=••••••">Log in to Linear</a>
      <a href="https://notion.so/login?token=***">Sign in</a>
    `;

    expect(extractMagicLoginLink(redactedHtml, null)).toBeNull();
  });

  it('returns null for normal newsletters and product updates', () => {
    const html = `
      <h1>Weekly Engineering Digest</h1>
      <p>Here are our top stories this week.</p>
      <a href="https://blog.example.com/2026/postgres-tuning">Read the full article</a>
      <a href="https://example.com/pricing">Explore plans</a>
      <a href="https://example.com/unsubscribe">Unsubscribe</a>
    `;
    const text = `Weekly Engineering Digest\nRead more at https://blog.example.com/2026/postgres-tuning`;

    expect(extractMagicLoginLink(html, text)).toBeNull();
  });
});
