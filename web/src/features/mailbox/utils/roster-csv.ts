import { MEMBER_IMPORT_MAX_ROWS } from '@/features/mailbox/schemas';
import type { MemberImportRowInput } from '@/features/mailbox/types';
import { parseCsv, toCsv } from '@/features/mailbox/utils/csv';

/** The roster columns; each header also has its Simplified Chinese names. */
const rosterHeaders = {
  email: ['email', '\u90ae\u7bb1', '\u767b\u5f55\u90ae\u7bb1', '\u53d7\u9080\u90ae\u7bb1'],
  name: ['name', '\u59d3\u540d', '\u540d\u79f0'],
  role: ['role', '\u89d2\u8272'],
  mailbox: ['mailbox', '\u90ae\u7bb1\u524d\u7f00', '\u4f01\u4e1a\u90ae\u7bb1', '\u90ae\u7bb1\u5730\u5740'],
} as const;

/** The roster template: the header line and two example rows to replace. */
export const ROSTER_IMPORT_TEMPLATE = toCsv([
  ['email', 'name', 'role', 'mailbox'],
  ['alice@example.com', 'Alice Wang', 'member', 'alice'],
  ['bob@example.com', 'Bob Li', 'admin', ''],
]);

export type RosterRowsResult =
  | { ok: true; rows: MemberImportRowInput[] }
  | { ok: false; error: 'empty' | 'missing_columns' | 'too_many_rows' };

/**
 * Reads roster rows from CSV text, or from tab-separated text pasted out of a spreadsheet.
 * Headers match trimmed and ignoring case; other columns are ignored, so a downloaded file of
 * failed rows imports again as it is. Only `email` is required.
 */
export function readRosterRows(text: string): RosterRowsResult {
  const [header, ...data] = parseRoster(text).filter(row => row.some(cell => cell.trim() !== ''));
  if (!header) return { ok: false, error: 'empty' };
  const names = header.map(cell => cell.trim().toLowerCase());
  const columnOf = (key: keyof typeof rosterHeaders) =>
    names.findIndex(name => (rosterHeaders[key] as readonly string[]).includes(name));
  const email = columnOf('email');
  const name = columnOf('name');
  const role = columnOf('role');
  const mailbox = columnOf('mailbox');
  if (email < 0) return { ok: false, error: 'missing_columns' };
  if (data.length === 0) return { ok: false, error: 'empty' };
  if (data.length > MEMBER_IMPORT_MAX_ROWS) return { ok: false, error: 'too_many_rows' };
  const cell = (row: string[], index: number) => (index < 0 ? '' : (row[index] ?? '').trim());
  return {
    ok: true,
    rows: data.map(row => ({
      email: cell(row, email),
      name: cell(row, name),
      role: cell(row, role),
      mailbox: cell(row, mailbox),
    })),
  };
}

/** Spreadsheets copy cells as tab-separated lines; a file or typed text is CSV. */
function parseRoster(text: string): string[][] {
  const source = text.startsWith('\ufeff') ? text.slice(1) : text;
  const firstLine = source.split(/\r\n|\r|\n/u, 1)[0] ?? '';
  if (firstLine.includes('\t') && !firstLine.includes(',')) {
    return source.split(/\r\n|\r|\n/u).map(line => line.split('\t'));
  }
  return parseCsv(source);
}
