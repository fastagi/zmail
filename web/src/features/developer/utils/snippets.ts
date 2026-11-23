/** The example recipient of every snippet; the caller replaces it with the real user. */
const exampleRecipient = 'user@example.com';

export interface SnippetInput {
  /** The public API origin the samples call; the caller resolves it, there is no placeholder. */
  apiOrigin: string;
  organizationId: number;
  /** The plaintext key, or a placeholder when it is no longer shown. */
  apiKey: string;
  /** The template key. */
  template: string;
  /** Example values by variable name. */
  variables: Readonly<Record<string, string>>;
}

export interface Snippets {
  curl: string;
  node: string;
  python: string;
}

function endpoint(apiOrigin: string): string {
  return `${apiOrigin.trim().replace(/\/+$/u, '')}/v1/transactional-messages`;
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

/**
 * Code that sends one transactional message with plain HTTP: curl, Node.js 18+ with the global
 * `fetch`, and Python with `urllib.request`. None needs an SDK. The idempotency key is one value per
 * logical message, so a retry of the same message reuses it.
 */
export function buildSnippets({
  apiOrigin,
  organizationId,
  apiKey,
  template,
  variables,
}: SnippetInput): Snippets {
  const url = endpoint(apiOrigin);
  const idempotencyKey = `${template}-user-42`;
  const body = { template, to: exampleRecipient, variables: { ...variables } };
  const json = JSON.stringify(body, null, 2);

  const curl = [
    `curl -X POST ${shellQuote(url)} \\`,
    `  -H ${shellQuote(`X-API-Key: ${apiKey}`)} \\`,
    `  -H ${shellQuote(`Organization-Id: ${organizationId}`)} \\`,
    `  -H ${shellQuote(`Idempotency-Key: ${idempotencyKey}`)} \\`,
    `  -H 'Content-Type: application/json' \\`,
    `  -d ${shellQuote(json)}`,
  ].join('\n');

  const node = [
    `// Node.js 18+: the global fetch, no SDK.`,
    `const response = await fetch(${literal(url)}, {`,
    `  method: 'POST',`,
    `  headers: {`,
    `    'X-API-Key': ${literal(apiKey)},`,
    `    'Organization-Id': ${literal(String(organizationId))},`,
    `    // One key per logical message: a retry of the same message reuses it.`,
    `    'Idempotency-Key': ${literal(idempotencyKey)},`,
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
    `        "Organization-Id": ${literal(String(organizationId))},`,
    `        # One key per logical message: a retry of the same message reuses it.`,
    `        "Idempotency-Key": ${literal(idempotencyKey)},`,
    `        "Content-Type": "application/json",`,
    `    },`,
    `)`,
    `with urllib.request.urlopen(request) as response:`,
    `    print(response.status, json.load(response))`,
  ].join('\n');

  return { curl, node, python };
}
