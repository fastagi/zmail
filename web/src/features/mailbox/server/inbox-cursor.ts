import 'server-only';

import { INBOX_CURSOR_MAX_LENGTH } from '@/features/mailbox/schemas';

/** Where an inbox page ends, in `(created_at DESC, id DESC)` order. */
export interface InboxPosition {
  /** `created_at` in Unix nanoseconds. */
  createdAt: bigint;
  id: bigint;
}

const int64Max = BigInt('9223372036854775807');
const int64Min = BigInt('-9223372036854775808');
const uint64Max = BigInt('18446744073709551615');
const cursorPattern = /^[A-Za-z0-9_-]+$/u;
const cursorBodyPattern = /^(0|-?[1-9]\d*)\.([1-9]\d*)$/u;

/** The cursor the API writes: base64url of `<unix nanoseconds>.<id>` without padding. */
export function encodeInboxCursor(position: InboxPosition): string {
  return Buffer.from(`${position.createdAt}.${position.id}`).toString('base64url');
}

/**
 * Reads a cursor from `encodeInboxCursor`. Like the API, only the exact spelling it writes is a
 * cursor: no padding, sign, leading zero, or id 0, and both numbers within the API's integers.
 */
export function decodeInboxCursor(token: string): InboxPosition | null {
  if (token.length > INBOX_CURSOR_MAX_LENGTH || !cursorPattern.test(token)) return null;
  const match = cursorBodyPattern.exec(Buffer.from(token, 'base64url').toString('latin1'));
  if (!match) return null;
  const createdAt = BigInt(match[1]);
  const id = BigInt(match[2]);
  if (createdAt > int64Max || createdAt < int64Min || id > uint64Max) return null;
  const position = { createdAt, id };
  return encodeInboxCursor(position) === token ? position : null;
}
