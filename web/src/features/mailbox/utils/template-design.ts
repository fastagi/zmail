import { parseTemplate } from '@/features/mailbox/utils/template-render';

/**
 * The visual template editor's document and its renderer. A design is a list of blocks with a few
 * global colors; the console renders it to email-safe HTML (tables, inline styles) and a plain-text
 * version, and saves all three. The API stores the design as given and always sends the bodies.
 *
 * Text in blocks may use `{{name}}` placeholders and a small markup: `**bold**`, `*italic*`, and
 * `[label](url)`. Placeholders pass through unchanged, so the API fills and escapes them per send.
 */

export const TEMPLATE_DESIGN_VERSION = 1;

export type BlockAlign = 'left' | 'center' | 'right';

export type DesignBlock =
  | { id: string; type: 'heading'; text: string; size: 'large' | 'medium'; align: BlockAlign }
  | { id: string; type: 'text'; text: string; align: BlockAlign }
  | { id: string; type: 'button'; label: string; url: string; align: BlockAlign }
  | {
      id: string;
      type: 'image';
      src: string;
      alt: string;
      href: string;
      width: ImageWidth;
      align: BlockAlign;
    }
  | { id: string; type: 'divider' }
  | { id: string; type: 'spacer'; height: SpacerHeight }
  | { id: string; type: 'footer'; text: string; align: BlockAlign };

export type DesignBlockType = DesignBlock['type'];
export type ImageWidth = 100 | 75 | 50 | 25;
export type SpacerHeight = 16 | 32 | 48;

export interface DesignSettings {
  /** Around the email. */
  background: string;
  /** The email card. */
  surface: string;
  text: string;
  /** Buttons and links. */
  accent: string;
}

/** A type alias, not an interface, so it fits the contract's open object type. */
export type TemplateDesign = {
  version: typeof TEMPLATE_DESIGN_VERSION;
  settings: DesignSettings;
  blocks: DesignBlock[];
};

export const designBlockTypes: readonly DesignBlockType[] = [
  'heading',
  'text',
  'button',
  'image',
  'divider',
  'spacer',
  'footer',
];

export const imageWidths: readonly ImageWidth[] = [100, 75, 50, 25];
export const spacerHeights: readonly SpacerHeight[] = [16, 32, 48];
export const blockAligns: readonly BlockAlign[] = ['left', 'center', 'right'];

export const defaultDesignSettings: DesignSettings = {
  background: '#f4f4f5',
  surface: '#ffffff',
  text: '#18181b',
  accent: '#2563eb',
};

/** The content width of the email card, in pixels. */
const contentWidth = 600;
const sidePadding = 32;
const muted = '#71717a';
const rule = '#e4e4e7';
const fontStack =
  "-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,'PingFang SC','Microsoft YaHei',sans-serif";
const maxBlocks = 100;
const maxFieldLength = 20_000;
const colorPattern = /^#[0-9a-f]{6}$/iu;

let blockSequence = 0;

/** A short id, unique within a design. */
export function newBlockId(): string {
  blockSequence += 1;
  return `b${Date.now().toString(36)}${blockSequence.toString(36)}`;
}

/** A block of the type with neutral content; the editor fills in the wording. */
export function newBlock(type: DesignBlockType, id = newBlockId()): DesignBlock {
  switch (type) {
    case 'heading':
      return { id, type, text: '', size: 'large', align: 'left' };
    case 'text':
      return { id, type, text: '', align: 'left' };
    case 'button':
      return { id, type, label: '', url: 'https://', align: 'left' };
    case 'image':
      return { id, type, src: 'https://', alt: '', href: '', width: 100, align: 'center' };
    case 'divider':
      return { id, type };
    case 'spacer':
      return { id, type, height: 32 };
    case 'footer':
      return { id, type, text: '', align: 'center' };
  }
}

export function emptyDesign(blocks: DesignBlock[] = []): TemplateDesign {
  return { version: TEMPLATE_DESIGN_VERSION, settings: { ...defaultDesignSettings }, blocks };
}

/* ------------------------------------------------------------------------------------------ */
/* Reading a stored design                                                                     */
/* ------------------------------------------------------------------------------------------ */

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function field(value: unknown): string | null {
  return typeof value === 'string' && value.length <= maxFieldLength ? value : null;
}

function oneOf<T extends string | number>(value: unknown, allowed: readonly T[]): T | null {
  return allowed.includes(value as T) ? (value as T) : null;
}

function color(value: unknown, fallback: string): string {
  return typeof value === 'string' && colorPattern.test(value) ? value.toLowerCase() : fallback;
}

function readBlock(value: unknown): DesignBlock | null {
  if (!isRecord(value)) return null;
  const id = field(value.id);
  const type = oneOf(value.type, designBlockTypes);
  if (!id || !type) return null;
  const align = oneOf(value.align, blockAligns) ?? 'left';
  switch (type) {
    case 'heading': {
      const text = field(value.text);
      const size = oneOf(value.size, ['large', 'medium'] as const) ?? 'large';
      return text === null ? null : { id, type, text, size, align };
    }
    case 'text':
    case 'footer': {
      const text = field(value.text);
      return text === null ? null : { id, type, text, align };
    }
    case 'button': {
      const label = field(value.label);
      const url = field(value.url);
      return label === null || url === null ? null : { id, type, label, url, align };
    }
    case 'image': {
      const src = field(value.src);
      const alt = field(value.alt);
      const href = field(value.href);
      const width = oneOf(value.width, imageWidths) ?? 100;
      if (src === null || alt === null || href === null) return null;
      return { id, type, src, alt, href, width, align };
    }
    case 'divider':
      return { id, type };
    case 'spacer':
      return { id, type, height: oneOf(value.height, spacerHeights) ?? 32 };
  }
}

/** The design in a stored template; null when there is none or it is not a design this editor reads. */
export function readDesign(value: unknown): TemplateDesign | null {
  if (!isRecord(value) || value.version !== TEMPLATE_DESIGN_VERSION) return null;
  if (!Array.isArray(value.blocks) || value.blocks.length > maxBlocks) return null;
  const blocks: DesignBlock[] = [];
  const ids = new Set<string>();
  for (const item of value.blocks) {
    const block = readBlock(item);
    if (!block || ids.has(block.id)) return null;
    ids.add(block.id);
    blocks.push(block);
  }
  const settings = isRecord(value.settings) ? value.settings : {};
  return {
    version: TEMPLATE_DESIGN_VERSION,
    settings: {
      background: color(settings.background, defaultDesignSettings.background),
      surface: color(settings.surface, defaultDesignSettings.surface),
      text: color(settings.text, defaultDesignSettings.text),
      accent: color(settings.accent, defaultDesignSettings.accent),
    },
    blocks,
  };
}

/* ------------------------------------------------------------------------------------------ */
/* Rendering                                                                                   */
/* ------------------------------------------------------------------------------------------ */

function escapeHtml(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}

/** Placeholders are swapped for private-use markers while markup is parsed, then restored. */
const markerStart = '\u{F0000}';
const markerEnd = '\u{F0001}';

function protectPlaceholders(source: string): { text: string; placeholders: string[] } {
  const parts = parseTemplate(source);
  if (!parts) return { text: source, placeholders: [] };
  const placeholders: string[] = [];
  let text = '';
  for (const part of parts) {
    if (part.kind === 'literal') {
      text += part.text;
      continue;
    }
    placeholders.push(
      part.fallback === null ? `{{${part.name}}}` : `{{${part.name}|${part.fallback}}}`
    );
    text += `${markerStart}${placeholders.length - 1}${markerEnd}`;
  }
  return { text, placeholders };
}

function restorePlaceholders(text: string, placeholders: readonly string[]): string {
  return text.replace(
    new RegExp(`${markerStart}(\\d+)${markerEnd}`, 'gu'),
    (_, index: string) => placeholders[Number(index)] ?? ''
  );
}

/**
 * A link target that is safe in an email: http, https, or mailto, or a URL that begins with a
 * placeholder, which the sender fills. Anything else, such as `javascript:`, is refused.
 */
export function safeUrl(url: string): boolean {
  const trimmed = url.trim();
  if (/^\{\{\s*[A-Za-z][A-Za-z0-9_]*\s*(\|[^}]*)?\}\}/u.test(trimmed)) return true;
  if (/[\s"'<>]/u.test(trimmed)) return false;
  return /^(https?:\/\/[^/\s]+|mailto:[^\s@]+@[^\s@]+)/iu.test(trimmed);
}

/** An image source: https only, or a placeholder. */
export function safeImageSource(url: string): boolean {
  const trimmed = url.trim();
  if (/^\{\{\s*[A-Za-z][A-Za-z0-9_]*\s*(\|[^}]*)?\}\}/u.test(trimmed)) return true;
  return /^https:\/\/[^/\s"'<>]+[^\s"'<>]*$/iu.test(trimmed);
}

function attribute(value: string): string {
  return escapeHtml(value.trim());
}

/** Inline markup to HTML: escapes, then bold, italic, and safe links; placeholders stay as they are. */
export function inlineHtml(source: string, accent: string): string {
  const { text, placeholders } = protectPlaceholders(source);
  let html = escapeHtml(text);
  html = html.replace(/\[([^\]\n]+)\]\(([^)\s]+)\)/gu, (whole, label: string, url: string) => {
    const target = restorePlaceholders(url, placeholders);
    // The URL is escaped already; read it back to judge it.
    const raw = target
      .replaceAll('&quot;', '"')
      .replaceAll('&lt;', '<')
      .replaceAll('&gt;', '>')
      .replaceAll('&amp;', '&');
    if (!safeUrl(raw)) return label;
    return `<a href="${target}" style="color:${accent};text-decoration:underline;">${label}</a>`;
  });
  html = html.replace(/\*\*([^*\n]+)\*\*/gu, '<strong>$1</strong>');
  html = html.replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/gu, '$1<em>$2</em>');
  return restorePlaceholders(html, placeholders);
}

/** Inline markup to plain text: links become `label (url)`. */
export function inlineText(source: string): string {
  return source
    .replace(/\[([^\]\n]+)\]\(([^)\s]+)\)/gu, (_, label: string, url: string) =>
      safeUrl(url) ? `${label} (${url})` : label
    )
    .replace(/\*\*([^*\n]+)\*\*/gu, '$1')
    .replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/gu, '$1$2');
}

function paragraphs(source: string): string[] {
  return source
    .split(/\n\s*\n/u)
    .map(paragraph => paragraph.trim())
    .filter(Boolean);
}

/** Black or white, whichever reads better on the color. */
function contrastText(hex: string): string {
  const value = Number.parseInt(hex.slice(1), 16);
  const r = (value >> 16) & 255;
  const g = (value >> 8) & 255;
  const b = value & 255;
  const luminance = (0.299 * r + 0.587 * g + 0.114 * b) / 255;
  return luminance > 0.6 ? '#18181b' : '#ffffff';
}

function blockHtml(block: DesignBlock, settings: DesignSettings): string {
  const accent = settings.accent;
  switch (block.type) {
    case 'heading': {
      const size = block.size === 'large' ? 26 : 19;
      const lines = block.text
        .trim()
        .split('\n')
        .map(line => inlineHtml(line, accent));
      return `<h${block.size === 'large' ? 1 : 2} style="margin:0;font-size:${size}px;line-height:1.3;font-weight:700;color:${settings.text};text-align:${block.align};">${lines.join('<br>')}</h${block.size === 'large' ? 1 : 2}>`;
    }
    case 'text':
      return paragraphs(block.text)
        .map(
          paragraph =>
            `<p style="margin:0 0 14px;font-size:15px;line-height:1.65;color:${settings.text};text-align:${block.align};">${paragraph
              .split('\n')
              .map(line => inlineHtml(line, accent))
              .join('<br>')}</p>`
        )
        .join('');
    case 'footer':
      return paragraphs(block.text)
        .map(
          paragraph =>
            `<p style="margin:0 0 8px;font-size:12px;line-height:1.6;color:${muted};text-align:${block.align};">${paragraph
              .split('\n')
              .map(line => inlineHtml(line, muted))
              .join('<br>')}</p>`
        )
        .join('');
    case 'button': {
      if (!block.label.trim() || !safeUrl(block.url)) return '';
      const foreground = contrastText(accent);
      return `<table role="presentation" cellpadding="0" cellspacing="0" border="0" align="${block.align}" style="margin:${block.align === 'center' ? '6px auto' : block.align === 'right' ? '6px 0 6px auto' : '6px 0'};"><tr><td style="border-radius:6px;background:${accent};"><a href="${attribute(block.url)}" style="display:inline-block;padding:12px 24px;font-size:15px;font-weight:600;line-height:1.2;color:${foreground};text-decoration:none;border-radius:6px;">${inlineHtml(block.label.trim(), foreground)}</a></td></tr></table>`;
    }
    case 'image': {
      if (!safeImageSource(block.src)) return '';
      const pixels = Math.round(((contentWidth - sidePadding * 2) * block.width) / 100);
      const margin =
        block.align === 'center' ? '0 auto' : block.align === 'right' ? '0 0 0 auto' : '0';
      const image = `<img src="${attribute(block.src)}" alt="${attribute(block.alt)}" width="${pixels}" style="display:block;width:100%;max-width:${pixels}px;height:auto;border:0;margin:${margin};">`;
      return block.href.trim() && safeUrl(block.href)
        ? `<a href="${attribute(block.href)}" style="display:block;">${image}</a>`
        : image;
    }
    case 'divider':
      return `<div style="border-top:1px solid ${rule};margin:8px 0;font-size:1px;line-height:1px;">&nbsp;</div>`;
    case 'spacer':
      return `<div style="height:${block.height}px;line-height:${block.height}px;font-size:1px;">&nbsp;</div>`;
  }
}

/** Email-safe HTML for the design: one table row per block, inline styles only. */
export function renderDesignHtml(design: TemplateDesign): string {
  const { settings } = design;
  const rows = design.blocks
    .map(block => ({ block, html: blockHtml(block, settings) }))
    .filter(row => row.html !== '')
    .map(({ block, html }, index, all) => {
      const top = index === 0 ? 32 : block.type === 'spacer' ? 0 : 6;
      const bottom = index === all.length - 1 ? 32 : block.type === 'spacer' ? 0 : 6;
      return `<tr><td style="padding:${top}px ${sidePadding}px ${bottom}px;font-family:${fontStack};">${html}</td></tr>`;
    })
    .join('\n');
  return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
</head>
<body style="margin:0;padding:0;background:${settings.background};">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:${settings.background};">
<tr><td align="center" style="padding:24px 12px;">
<table role="presentation" width="${contentWidth}" cellpadding="0" cellspacing="0" border="0" style="max-width:${contentWidth}px;width:100%;background:${settings.surface};border-radius:8px;">
${rows}
</table>
</td></tr>
</table>
</body>
</html>
`;
}

/** The plain-text version: the same content without markup. */
export function renderDesignText(design: TemplateDesign): string {
  const parts: string[] = [];
  for (const block of design.blocks) {
    switch (block.type) {
      case 'heading':
      case 'text':
      case 'footer': {
        const text = inlineText(block.text.trim());
        if (text) parts.push(text);
        break;
      }
      case 'button':
        if (block.label.trim() && safeUrl(block.url)) {
          parts.push(`${inlineText(block.label.trim())}: ${block.url.trim()}`);
        }
        break;
      case 'image':
        if (block.alt.trim() && block.href.trim() && safeUrl(block.href)) {
          parts.push(`${block.alt.trim()}: ${block.href.trim()}`);
        } else if (block.alt.trim()) {
          parts.push(block.alt.trim());
        }
        break;
      case 'divider':
        parts.push('---');
        break;
      case 'spacer':
        break;
    }
  }
  return parts.join('\n\n');
}

/** Problems that keep a block out of the email, for the editor to show. */
export type BlockIssue = 'buttonUrl' | 'buttonLabel' | 'imageSource' | 'imageLink';

export function blockIssues(block: DesignBlock): BlockIssue[] {
  const issues: BlockIssue[] = [];
  if (block.type === 'button') {
    if (!block.label.trim()) issues.push('buttonLabel');
    if (!safeUrl(block.url)) issues.push('buttonUrl');
  }
  if (block.type === 'image') {
    if (!safeImageSource(block.src)) issues.push('imageSource');
    if (block.href.trim() && !safeUrl(block.href)) issues.push('imageLink');
  }
  return issues;
}
