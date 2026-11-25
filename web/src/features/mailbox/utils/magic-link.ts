/** A detected sign-in / verification magic link extracted from an email body. */
export interface MagicLoginLink {
  url: string;
  label: string;
  host: string;
}

const ANCHOR_RE =
  /<a\b([^>]*)\bhref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+))([^>]*)>([\s\S]*?)<\/a>/giu;

const PLAIN_URL_RE = /https?:\/\/[^\s<>()"'\]]+/giu;

const REDACTED_RE =
  /(\[?\s*(?:redacted|masked)\s*\]?|%5b(?:redacted|masked)%5d|\*{3,}|•{3,}|●{3,})/iu;

const EXCLUDED_TEXT_RE =
  /\b(unsubscribe|opt[-_\s]?out|support|help\s*center|helpdesk|privacy|terms(?:\s+of\s+(?:service|use))?|legal|cookie\s*policy|email\s*preferences|manage\s*preferences)\b|退订|取消订阅|隐私|条款|客服|帮助中心/iu;

const EXCLUDED_HOST_OR_PATH_RE =
  /(?:^|[./_-])(unsubscribe|opt[-_]?out|support|privacy|terms|legal|preferences|helpcenter|helpdesk)(?:$|[./_-])/iu;

const EXCLUDED_QUERY_RE = /[?&](?:action|type|cmd)=unsubscribe\b|\bunsubscribe=true\b/iu;

const STRONG_AUTH_LABEL_RE =
  /\b(sign\s*[- ]?in|log\s*[- ]?in|magic\s*link|one[- ]time\s*link)\b|立即登录|点击登录|快捷登录|登录/iu;

const SECONDARY_AUTH_LABEL_RE =
  /\b(verify|confirm|authenticate|activate\s+your\s+account)\b|验证|确认邮箱|激活账号/iu;

const AUTH_URL_PATH_RE =
  /\/(?:magic(?:[-_][a-z0-9]+)?|login|log-in|signin|sign-in|auth|verify(?:[-_][a-z0-9]+)?|confirm(?:[-_][a-z0-9]+)?|sso|oauth|callback|activate)(?:\/|$)/iu;

const AUTH_URL_QUERY_RE =
  /[?&](?:[a-z0-9_-]*_)?token=[^&\s]+/iu;

const LOCALHOST_HOSTS = new Set(['localhost', '127.0.0.1', '[::1]', '::1']);

function decodeHtmlEntities(value: string): string {
  return value
    .replace(/&amp;/giu, '&')
    .replace(/&lt;/giu, '<')
    .replace(/&gt;/giu, '>')
    .replace(/&quot;/giu, '"')
    .replace(/&#0*39;|&apos;/giu, "'")
    .replace(/&nbsp;/giu, ' ')
    .replace(/&#(\d+);/gu, (_, code: string) => {
      const n = Number(code);
      return Number.isFinite(n) ? String.fromCodePoint(n) : _;
    })
    .replace(/&#x([0-9a-f]+);/giu, (_, hex: string) => {
      const n = Number.parseInt(hex, 16);
      return Number.isFinite(n) ? String.fromCodePoint(n) : _;
    });
}

function stripTagsAndNormalize(htmlFragment: string): string {
  const withoutTags = htmlFragment.replace(/<[^>]*>/gu, ' ');
  return decodeHtmlEntities(withoutTags).replace(/\s+/gu, ' ').trim();
}

function parseSafeAuthUrl(rawUrl: string): { url: string; host: string; parsed: URL } | null {
  const trimmed = rawUrl.trim();
  if (!trimmed || REDACTED_RE.test(trimmed)) return null;

  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return null;
  }

  const protocol = parsed.protocol.toLowerCase();
  const hostname = parsed.hostname.toLowerCase();
  if (protocol === 'http:') {
    if (!LOCALHOST_HOSTS.has(hostname)) return null;
  } else if (protocol !== 'https:') {
    return null;
  }

  if (!hostname) return null;
  const host = hostname.replace(/^www\./iu, '');
  return { url: parsed.toString(), host, parsed };
}

function isExcludedLink(parsed: URL, label: string): boolean {
  if (label && EXCLUDED_TEXT_RE.test(label)) return true;
  if (EXCLUDED_HOST_OR_PATH_RE.test(parsed.hostname)) return true;
  if (EXCLUDED_HOST_OR_PATH_RE.test(parsed.pathname)) return true;
  if (parsed.search && EXCLUDED_QUERY_RE.test(parsed.search)) return true;
  return false;
}

function hasAuthUrlSignal(parsed: URL): boolean {
  if (AUTH_URL_PATH_RE.test(parsed.pathname)) return true;
  if (parsed.search && AUTH_URL_QUERY_RE.test(parsed.search)) return true;
  if (parsed.hash && AUTH_URL_QUERY_RE.test(`?${parsed.hash.slice(1)}`)) return true;
  return false;
}

function scoreCandidate(parsed: URL, label: string): number {
  const urlMatch = hasAuthUrlSignal(parsed);
  const strongLabel = Boolean(label) && STRONG_AUTH_LABEL_RE.test(label);
  const secondaryLabel = Boolean(label) && SECONDARY_AUTH_LABEL_RE.test(label);

  if (urlMatch && (strongLabel || secondaryLabel)) return 3;
  if (strongLabel || urlMatch) return 2;
  if (secondaryLabel) return 1;
  return 0;
}

function extractFromHtml(html: string): MagicLoginLink | null {
  let best: (MagicLoginLink & { score: number }) | null = null;
  ANCHOR_RE.lastIndex = 0;

  let match: RegExpExecArray | null;
  while ((match = ANCHOR_RE.exec(html)) !== null) {
    const rawHref = match[2] ?? match[3] ?? match[4] ?? '';
    const decodedHref = decodeHtmlEntities(rawHref);
    const parsedResult = parseSafeAuthUrl(decodedHref);
    if (!parsedResult) continue;

    const label = stripTagsAndNormalize(match[6] ?? '');
    if (isExcludedLink(parsedResult.parsed, label)) continue;

    const score = scoreCandidate(parsedResult.parsed, label);
    if (score === 0) continue;

    if (!best || score > best.score) {
      best = {
        url: parsedResult.url,
        label: label || parsedResult.host,
        host: parsedResult.host,
        score,
      };
    }
  }

  return best ? { url: best.url, label: best.label, host: best.host } : null;
}

function extractFromPlainText(text: string): MagicLoginLink | null {
  let best: (MagicLoginLink & { score: number }) | null = null;
  PLAIN_URL_RE.lastIndex = 0;

  let match: RegExpExecArray | null;
  while ((match = PLAIN_URL_RE.exec(text)) !== null) {
    const raw = match[0].replace(/[.,;:!?)}\]>'"]+$/u, '');
    const parsedResult = parseSafeAuthUrl(raw);
    if (!parsedResult) continue;

    const contextStart = Math.max(0, match.index - 100);
    const preceding = text.slice(contextStart, match.index);
    const lineStart = preceding.lastIndexOf('\n');
    const nearbyLine = preceding.slice(lineStart + 1).trim();
    const prevChunk = preceding.slice(Math.max(0, preceding.length - 80)).trim();

    if (isExcludedLink(parsedResult.parsed, nearbyLine)) continue;

    const urlMatch = hasAuthUrlSignal(parsedResult.parsed);
    const contextMatch =
      STRONG_AUTH_LABEL_RE.test(prevChunk) || SECONDARY_AUTH_LABEL_RE.test(prevChunk);

    let score = 0;
    if (urlMatch && contextMatch) score = 3;
    else if (urlMatch) score = 2;
    else if (STRONG_AUTH_LABEL_RE.test(nearbyLine)) score = 1;

    if (score === 0) continue;

    if (!best || score > best.score) {
      best = {
        url: parsedResult.url,
        label: parsedResult.host,
        host: parsedResult.host,
        score,
      };
    }
  }

  return best ? { url: best.url, label: best.label, host: best.host } : null;
}

/**
 * Extracts a sign-in / magic login link from an email's HTML or plain-text body.
 * Returns null for ordinary newsletters, non-auth links, or masked/redacted placeholders.
 */
export function extractMagicLoginLink(
  html?: string | null,
  text?: string | null
): MagicLoginLink | null {
  if (html) {
    const fromHtml = extractFromHtml(html);
    if (fromHtml) return fromHtml;
  }
  if (text) {
    return extractFromPlainText(text);
  }
  return null;
}
