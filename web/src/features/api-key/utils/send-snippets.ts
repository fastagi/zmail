export interface SendSnippetInput {
  /** The public API origin the samples call; the caller resolves it, there is no placeholder. */
  apiOrigin: string;
  organizationId: number;
  /** A key bound to its organization acts there without the `Organization-Id` header. */
  omitOrganizationHeader?: boolean;
  /** The plaintext key, or a placeholder once it is no longer shown. */
  apiKey: string;
  /** A mailbox of the organization the key's owner may send from. */
  from: string;
  to: string;
}

export interface SendSnippets {
  curl: string;
  node: string;
  python: string;
}

export const SEND_SNIPPET_SUBJECT = 'Hello from Zmail';
const SEND_SNIPPET_TEXT = 'My first email sent with the Zmail API.';

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

/**
 * Code that sends one message with `POST /v1/outbound-mail-messages`: curl, Node.js 18+ with the
 * global `fetch`, and Python's standard library. None needs an SDK.
 */
export function buildSendSnippets({
  apiOrigin,
  organizationId,
  omitOrganizationHeader = false,
  apiKey,
  from,
  to,
}: SendSnippetInput): SendSnippets {
  const origin = apiOrigin.trim().replace(/\/+$/u, '');
  const url = `${origin}/v1/outbound-mail-messages`;
  const json = JSON.stringify(
    { from, to: [to], subject: SEND_SNIPPET_SUBJECT, text: SEND_SNIPPET_TEXT },
    null,
    2
  );
  const organization = String(organizationId);
  const withOrganization = (line: string) => (omitOrganizationHeader ? [] : [line]);

  const curl = [
    `curl -X POST ${shellQuote(url)} \\`,
    `  -H ${shellQuote(`X-API-Key: ${apiKey}`)} \\`,
    ...withOrganization(`  -H ${shellQuote(`Organization-Id: ${organization}`)} \\`),
    `  -H 'Content-Type: application/json' \\`,
    `  -d ${shellQuote(json)}`,
  ].join('\n');

  const node = [
    `// Node.js 18+ (save as send.mjs): the global fetch, no SDK.`,
    `const response = await fetch(${literal(url)}, {`,
    `  method: 'POST',`,
    `  headers: {`,
    `    'X-API-Key': ${literal(apiKey)},`,
    ...withOrganization(`    'Organization-Id': ${literal(organization)},`),
    `    'Content-Type': 'application/json',`,
    `  },`,
    `  body: JSON.stringify(${indentLines(json, '  ')}),`,
    `});`,
    `console.log(response.status, await response.json());`,
  ].join('\n');

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
    `        "X-API-Key": ${literal(apiKey)},`,
    ...withOrganization(`        "Organization-Id": ${literal(organization)},`),
    `        "Content-Type": "application/json",`,
    `    },`,
    `)`,
    `with urllib.request.urlopen(request) as response:`,
    `    print(response.status, json.load(response))`,
  ].join('\n');

  return { curl, node, python };
}
