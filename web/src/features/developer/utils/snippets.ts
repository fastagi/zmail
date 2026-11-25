/** The example recipient of every snippet; the caller replaces it with the real user. */
const exampleRecipient = 'user@example.com';

export interface SnippetInput {
  /** The public API origin the samples call; the caller resolves it, there is no placeholder. */
  apiOrigin: string;
  idempotencyKey?: string;
  organizationId: number;
  /** When true (e.g. an organization-bound key), omit the Organization-Id header. */
  omitOrganizationHeader?: boolean;
  /** The plaintext key, or a placeholder when it is no longer shown. */
  apiKey: string;
  /** The template key (used in template mode). */
  template: string;
  /** Example values by variable name (used in template mode). */
  variables: Readonly<Record<string, string>>;
}

export interface DirectSnippetInput {
  /** The public API origin the samples call; the caller resolves it, there is no placeholder. */
  apiOrigin: string;
  idempotencyKey?: string;
  organizationId: number;
  /** When true (e.g. an organization-bound key), omit the Organization-Id header. */
  omitOrganizationHeader?: boolean;
  /** The plaintext key, or a placeholder when it is no longer shown. */
  apiKey: string;
  /** Sender address on a verified domain (e.g. `noreply@acme.example`). */
  from: string;
}

export interface Snippets {
  curl: string;
  node: string;
  python: string;
}

function endpoint(apiOrigin: string): string {
  return `${apiOrigin.trim().replace(/\/+$/u, '')}/v1/emails`;
}

/** A single-quoted POSIX shell word. */
function shellQuote(value: string): string {
  return `'${value.replaceAll("'", `'\\''`)}'`;
}

/** A JSON string literal, which is also a valid JavaScript and Python string literal. */
function literal(value: string): string {
  return JSON.stringify(value);
}

function indentLines(text: string, indent: string): string {
  return text
    .split('\n')
    .map((line, index) => (index === 0 ? line : `${indent}${line}`))
    .join('\n');
}

function renderHttpSnippets({
  url,
  headers,
  body,
}: {
  url: string;
  headers: readonly (readonly [string, string])[];
  body: Record<string, unknown>;
}): Snippets {
  const json = JSON.stringify(body, null, 2);

  const curlHeaders = headers.map(([name, value]) => `  -H ${shellQuote(`${name}: ${value}`)} \\`);
  const curl = [
    `curl -X POST ${shellQuote(url)} \\`,
    ...curlHeaders,
    `  -d ${shellQuote(json)}`,
  ].join('\n');

  const nodeHeaders = headers.map(([name, value]) => `    ${literal(name)}: ${literal(value)},`);
  const node = [
    `// Node.js 18+: the global fetch, no SDK.`,
    `const response = await fetch(${literal(url)}, {`,
    `  method: 'POST',`,
    `  headers: {`,
    ...nodeHeaders,
    `  },`,
    `  body: JSON.stringify(${indentLines(json, '  ')}),`,
    `});`,
    `console.log(response.status, await response.json());`,
  ].join('\n');

  const pythonHeaders = headers.map(
    ([name, value]) => `        ${literal(name)}: ${literal(value)},`
  );
  const python = [
    `import json`,
    `import urllib.request`,
    ``,
    `body = ${json}`,
    `request = urllib.request.Request(`,
    `    ${literal(url)},`,
    `    data=json.dumps(body).encode("utf-8"),`,
    `    method="POST",`,
    `    headers={`,
    ...pythonHeaders,
    `    },`,
    `)`,
    `with urllib.request.urlopen(request) as response:`,
    `    print(response.status, json.load(response))`,
  ].join('\n');

  return { curl, node, python };
}

/**
 * Resend-style direct email snippets: POST /v1/emails with `Authorization: Bearer`, direct
 * `from`/`to`/`subject`/`html`, and no template or mailbox required.
 */
export function buildDirectSnippets({
  apiOrigin,
  organizationId,
  idempotencyKey,
  omitOrganizationHeader = false,
  apiKey,
  from,
}: DirectSnippetInput): Snippets {
  const headers: [string, string][] = [['Authorization', `Bearer ${apiKey}`]];
  if (!omitOrganizationHeader) {
    headers.push(['Organization-Id', String(organizationId)]);
  }
  if (idempotencyKey) headers.push(['Idempotency-Key', idempotencyKey]);
  headers.push(['Content-Type', 'application/json']);

  return renderHttpSnippets({
    url: endpoint(apiOrigin),
    headers,
    body: {
      from,
      to: exampleRecipient,
      subject: 'Welcome',
      html: '<p>Hello from Zmail!</p>',
    },
  });
}

/**
 * Template-based transactional email snippets: POST /v1/emails with `Authorization: Bearer`,
 * template key, variables, and an idempotency key so retries never send twice.
 */
export function buildSnippets({
  apiOrigin,
  organizationId,
  idempotencyKey,
  omitOrganizationHeader = false,
  apiKey,
  template,
  variables,
}: SnippetInput): Snippets {
  const headers: [string, string][] = [['Authorization', `Bearer ${apiKey}`]];
  if (!omitOrganizationHeader) {
    headers.push(['Organization-Id', String(organizationId)]);
  }
  if (idempotencyKey) headers.push(['Idempotency-Key', idempotencyKey]);
  headers.push(['Content-Type', 'application/json']);

  return renderHttpSnippets({
    url: endpoint(apiOrigin),
    headers,
    body: { template, to: exampleRecipient, variables: { ...variables } },
  });
}
