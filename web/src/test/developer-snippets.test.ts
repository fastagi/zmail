import { describe, expect, it } from 'vitest';

import { buildDirectSnippets, buildSnippets } from '@/features/developer/utils/snippets';

const input = {
  apiOrigin: 'https://api.acme.example/',
  organizationId: 42,
  apiKey: 'fastbuild_test_abcd1234',
  template: 'signup-code',
  variables: { code: '482913', app: "Acme's app" },
};

describe('developer snippets', () => {
  it('direct snippets call /v1/emails with Authorization Bearer and omit Organization-Id when bound', () => {
    const snippets = buildDirectSnippets({
      apiOrigin: 'https://zmail.im/',
      organizationId: 42,
      omitOrganizationHeader: true,
      apiKey: 'zmail_live_abcd1234',
      from: 'noreply@kfk.im',
    });
    for (const code of [snippets.curl, snippets.node, snippets.python]) {
      expect(code).toContain('https://zmail.im/v1/emails');
      expect(code).toContain('Bearer zmail_live_abcd1234');
      expect(code).toContain('noreply@kfk.im');
      expect(code).not.toContain('Organization-Id');
      expect(code).not.toContain('Idempotency-Key');
    }
  });

  it('template snippets carry the headers and never an SDK import', () => {
    const snippets = buildSnippets(input);
    for (const code of [snippets.curl, snippets.node, snippets.python]) {
      expect(code).toContain('https://api.acme.example/v1/emails');
      expect(code).not.toContain('example//v1');
      expect(code).toContain('Authorization');
      expect(code).toContain('Bearer fastbuild_test_abcd1234');
      expect(code).toContain('Organization-Id');
      expect(code).toContain('42');
      expect(code).toContain('Idempotency-Key');
      expect(code).toContain('signup-code');
      expect(code).toContain('482913');
      expect(code).not.toMatch(/\brequire\(/u);
      expect(code).not.toMatch(/\b(?:axios|requests|zmail|fastbuild-sdk|node-fetch)\b/u);
    }
    // Node uses the global fetch and imports nothing.
    expect(snippets.node).toContain('await fetch(');
    expect(snippets.node).not.toMatch(/^\s*import\s/mu);
    // Python uses only the standard library.
    expect(snippets.python).toContain('urllib.request');
    const pythonImports = snippets.python
      .split('\n')
      .filter(line => /^\s*(?:import|from)\s/u.test(line));
    expect(pythonImports).toEqual(['import json', 'import urllib.request']);
    // A quote inside a value cannot break out of the shell string.
    expect(snippets.curl).toContain(`Acme'\\''s app`);
  });

  it('calls the given origin without a trailing slash and never a placeholder host (#144)', () => {
    const snippets = buildSnippets({ ...input, apiOrigin: 'https://zmail.im/' });
    expect(snippets.curl).toContain("'https://zmail.im/v1/emails'");
    expect(snippets.python).toContain('https://zmail.im/v1/emails');
    for (const sample of Object.values(snippets)) expect(sample).not.toContain('api.example.com');
  });
});
