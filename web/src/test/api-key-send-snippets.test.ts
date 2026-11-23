import { describe, expect, it } from 'vitest';

import { buildSendSnippets } from '@/features/api-key/utils/send-snippets';

const input = {
  apiOrigin: 'https://zmail.im/',
  organizationId: 12,
  apiKey: 'zmail_ab12cd34.secret',
  from: 'dev@acme.zmail.im',
  to: "o'brien@example.com",
};

describe('send-with-a-key snippets', () => {
  it('fills in the real endpoint, key, organization, and sender', () => {
    const { curl, node, python } = buildSendSnippets(input);
    for (const snippet of [curl, node, python]) {
      expect(snippet).toContain('https://zmail.im/v1/outbound-mail-messages');
      expect(snippet).toContain('zmail_ab12cd34.secret');
      expect(snippet).toContain('dev@acme.zmail.im');
      expect(snippet).toContain('Organization-Id');
      expect(snippet).toContain('12');
    }
    expect(curl).toContain("-H 'X-API-Key: zmail_ab12cd34.secret'");
    expect(curl).toContain("-H 'Organization-Id: 12'");
  });

  it('quotes a recipient with a quote for the shell', () => {
    const { curl } = buildSendSnippets(input);
    expect(curl).toContain(`o'\\''brien@example.com`);
  });

  it('sends a JSON body the outbound route accepts', () => {
    const { curl } = buildSendSnippets(input);
    const body = curl.slice(curl.indexOf("-d '") + 4, curl.lastIndexOf("'")).replaceAll(`'\\''`, "'");
    expect(JSON.parse(body)).toEqual({
      from: 'dev@acme.zmail.im',
      to: ["o'brien@example.com"],
      subject: expect.any(String),
      text: expect.any(String),
    });
  });

  it('uses the site origin shape without a trailing slash and a placeholder origin when empty', () => {
    expect(buildSendSnippets({ ...input, apiOrigin: 'http://localhost:3181' }).curl).toContain(
      'http://localhost:3181/v1/outbound-mail-messages'
    );
    expect(buildSendSnippets({ ...input, apiOrigin: '' }).curl).toContain(
      'https://api.example.com/v1/outbound-mail-messages'
    );
  });
});
