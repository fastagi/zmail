import { MAIL_TEMPLATE_VARIABLE_PATTERN } from '@/features/mailbox/schemas';

/**
 * The template language of `contracts/MAILBOXES.md`: `{{name}}` and `{{name|fallback}}`, with no
 * logic. The API is authoritative; this copy lets the development mock and the editor agree with
 * it. Values are HTML-escaped in the HTML body, and line breaks become spaces in the subject.
 */

const maxFallbackLength = 500;

type TemplatePart =
  { kind: 'literal'; text: string } | { kind: 'variable'; name: string; fallback: string | null };

/** Splits source into literal text and placeholders; null when any `{{` is malformed. */
export function parseTemplate(source: string): TemplatePart[] | null {
  const parts: TemplatePart[] = [];
  let rest = source;
  while (true) {
    const start = rest.indexOf('{{');
    if (start < 0) {
      if (rest !== '') parts.push({ kind: 'literal', text: rest });
      return parts;
    }
    if (start > 0) parts.push({ kind: 'literal', text: rest.slice(0, start) });
    const after = rest.slice(start + 2);
    const end = after.indexOf('}}');
    if (end < 0) return null;
    const body = after.slice(0, end);
    if (body.includes('{{') || /[\r\n]/u.test(body)) return null;
    const bar = body.indexOf('|');
    const name = (bar < 0 ? body : body.slice(0, bar)).trim();
    const fallback = bar < 0 ? null : body.slice(bar + 1).trim();
    if (!MAIL_TEMPLATE_VARIABLE_PATTERN.test(name)) return null;
    if (fallback !== null && Array.from(fallback).length > maxFallbackLength) return null;
    parts.push({ kind: 'variable', name, fallback });
    rest = after.slice(end + 2);
  }
}

export interface TemplateSource {
  subject: string;
  html: string;
  text: string;
}

export interface RenderedTemplate {
  subject: string;
  html: string;
  text: string;
  variables: string[];
  missing: string[];
}

type Escaping = 'html' | 'line' | 'none';

function escapeHtml(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&#34;')
    .replaceAll("'", '&#39;');
}

function renderParts(
  parts: TemplatePart[],
  values: Readonly<Record<string, string>>,
  escaping: Escaping,
  missing: Set<string>
): string {
  let out = '';
  for (const part of parts) {
    if (part.kind === 'literal') {
      out += part.text;
      continue;
    }
    let value = values[part.name] ?? '';
    if (value === '') {
      if (part.fallback === null) {
        missing.add(part.name);
        continue;
      }
      value = part.fallback;
    }
    if (escaping === 'html') value = escapeHtml(value);
    if (escaping === 'line') value = value.split(/\s+/u).filter(Boolean).join(' ');
    out += value;
  }
  return out;
}

/** Every variable the source uses, sorted; null when the source is malformed. */
export function templateVariables(source: TemplateSource): string[] | null {
  const names = new Set<string>();
  for (const text of [source.subject, source.html, source.text]) {
    const parts = parseTemplate(text);
    if (!parts) return null;
    for (const part of parts) if (part.kind === 'variable') names.add(part.name);
  }
  return [...names].sort();
}

/**
 * Every variable with its fallback, sorted by name. The fallback is null when at least one use of
 * the variable has none, because that use fails without a value. Null when the source is malformed.
 */
export function templateFallbacks(
  source: TemplateSource
): { name: string; fallback: string | null }[] | null {
  const fallbacks = new Map<string, string | null>();
  for (const text of [source.subject, source.html, source.text]) {
    const parts = parseTemplate(text);
    if (!parts) return null;
    for (const part of parts) {
      if (part.kind !== 'variable') continue;
      const known = fallbacks.get(part.name);
      fallbacks.set(
        part.name,
        part.fallback === null || known === null ? null : (known ?? part.fallback)
      );
    }
  }
  return [...fallbacks.keys()]
    .sort()
    .map(name => ({ name, fallback: fallbacks.get(name) ?? null }));
}

/** Renders the source with values; null when the source is malformed. */
export function renderTemplate(
  source: TemplateSource,
  values: Readonly<Record<string, string>>
): RenderedTemplate | null {
  const subject = parseTemplate(source.subject);
  const html = parseTemplate(source.html);
  const text = parseTemplate(source.text);
  const variables = templateVariables(source);
  if (!subject || !html || !text || !variables) return null;
  const missing = new Set<string>();
  return {
    subject: renderParts(subject, values, 'line', missing).trim(),
    html: renderParts(html, values, 'html', missing),
    text: renderParts(text, values, 'none', missing),
    variables,
    missing: [...missing].sort(),
  };
}
