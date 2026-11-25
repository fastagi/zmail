/**
 * Deterministic sender avatar monograms and Feishu × Linear soft-saturated badge palettes.
 * Pure helper with no React or DOM dependencies.
 */

const AVATAR_PALETTE = [
  'bg-blue-500/15 text-blue-700 ring-1 ring-inset ring-blue-500/20 dark:bg-blue-400/15 dark:text-blue-300 dark:ring-blue-400/25',
  'bg-indigo-500/15 text-indigo-700 ring-1 ring-inset ring-indigo-500/20 dark:bg-indigo-400/15 dark:text-indigo-300 dark:ring-indigo-400/25',
  'bg-violet-500/15 text-violet-700 ring-1 ring-inset ring-violet-500/20 dark:bg-violet-400/15 dark:text-violet-300 dark:ring-violet-400/25',
  'bg-rose-500/15 text-rose-700 ring-1 ring-inset ring-rose-500/20 dark:bg-rose-400/15 dark:text-rose-300 dark:ring-rose-400/25',
  'bg-amber-500/15 text-amber-800 ring-1 ring-inset ring-amber-500/25 dark:bg-amber-400/15 dark:text-amber-300 dark:ring-amber-400/25',
  'bg-emerald-500/15 text-emerald-700 ring-1 ring-inset ring-emerald-500/20 dark:bg-emerald-400/15 dark:text-emerald-300 dark:ring-emerald-400/25',
  'bg-teal-500/15 text-teal-700 ring-1 ring-inset ring-teal-500/20 dark:bg-teal-400/15 dark:text-teal-300 dark:ring-teal-400/25',
  'bg-sky-500/15 text-sky-700 ring-1 ring-inset ring-sky-500/20 dark:bg-sky-400/15 dark:text-sky-300 dark:ring-sky-400/25',
] as const;

const CJK_RE = /\p{Script=Han}|\p{Script=Hiragana}|\p{Script=Katakana}|\p{Script=Hangul}/u;
const WORD_CHAR_RE = /[\p{L}\p{N}]/u;

export interface SenderAvatarProps {
  monogram: string;
  className: string;
}

function hashSeed(seed: string): number {
  let hash = 2166136261;
  for (let i = 0; i < seed.length; i += 1) {
    hash ^= seed.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

function extractMonogram(raw: string): string {
  const trimmed = raw.trim().replace(/^["'<\s]+|["'>\s]+$/g, '');
  if (!trimmed) return '?';

  const source = trimmed.includes('@') ? (trimmed.split('@')[0] ?? trimmed) : trimmed;
  const chars = Array.from(source);
  const firstMeaningful = chars.find(ch => WORD_CHAR_RE.test(ch));

  if (firstMeaningful && CJK_RE.test(firstMeaningful)) {
    return firstMeaningful;
  }

  const tokens = source
    .split(/[\s._+-]+/)
    .map(token => Array.from(token).find(ch => WORD_CHAR_RE.test(ch)) ?? '')
    .filter(Boolean);

  if (tokens.length >= 2) {
    if (CJK_RE.test(tokens[0])) return tokens[0];
    return `${tokens[0]}${tokens[1]}`.toUpperCase();
  }

  if (firstMeaningful) {
    return firstMeaningful.toUpperCase();
  }

  return chars[0]?.toUpperCase() ?? '?';
}

/**
 * Returns a deterministic 1-2 character monogram and soft-saturated Tailwind badge classes
 * for a sender's display name or email address.
 */
export function senderAvatarProps(nameOrAddress: string): SenderAvatarProps {
  const normalized = nameOrAddress.trim().toLowerCase();
  const index = hashSeed(normalized || '?') % AVATAR_PALETTE.length;
  return {
    monogram: extractMonogram(nameOrAddress),
    className: AVATAR_PALETTE[index],
  };
}
