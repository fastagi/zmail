import { MAILBOX_IMPORT_MAX_ROWS } from '@/features/mailbox/schemas';
import type { MailboxImportRowInput } from '@/features/mailbox/types';

/**
 * Reads CSV text: cells separated by commas, rows by CRLF, LF, or CR, double-quoted cells that
 * may hold commas, line breaks, and doubled quotes. A leading byte order mark is dropped, and
 * blank lines add no row.
 */
export function parseCsv(text: string): string[][] {
  const source = text.startsWith('\ufeff') ? text.slice(1) : text;
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = '';
  let quoted = false;
  let index = 0;

  const endRow = () => {
    row.push(cell);
    // A blank line is one empty cell; it adds no row.
    if (row.length > 1 || row[0] !== '') rows.push(row);
    row = [];
    cell = '';
  };

  while (index < source.length) {
    const char = source[index];
    if (quoted) {
      if (char === '"' && source[index + 1] === '"') {
        cell += '"';
        index += 2;
        continue;
      }
      if (char === '"') {
        quoted = false;
      } else {
        cell += char;
      }
      index += 1;
      continue;
    }
    if (char === '"' && cell === '') {
      quoted = true;
    } else if (char === ',') {
      row.push(cell);
      cell = '';
    } else if (char === '\r' || char === '\n') {
      endRow();
      if (char === '\r' && source[index + 1] === '\n') index += 1;
    } else {
      cell += char;
    }
    index += 1;
  }
  if (cell !== '' || row.length > 0) endRow();
  return rows;
}

/** Writes rows as CSV with CRLF line ends; a cell holding `,`, `"`, CR, or LF is quoted. */
export function toCsv(rows: readonly (readonly string[])[]): string {
  return rows
    .map(
      row =>
        `${row
          .map(cell => (/[",\r\n]/u.test(cell) ? `"${cell.replaceAll('"', '""')}"` : cell))
          .join(',')}\r\n`
    )
    .join('');
}

/** The import columns; each header also has its Simplified Chinese name. */
const importHeaders = {
  address: ['address', '\u90ae\u7bb1\u5730\u5740'],
  display_name: ['display_name', '\u540d\u79f0'],
  members: ['members', '\u6210\u5458'],
} as const;

/** The header line of the import template. */
export const MAILBOX_IMPORT_TEMPLATE = toCsv([['address', 'display_name', 'members']]);

export type ImportRowsResult =
  | { ok: true; rows: MailboxImportRowInput[] }
  | { ok: false; error: 'empty' | 'missing_columns' | 'too_many_rows' };

/**
 * Reads the rows of an import file. Headers match trimmed and ignoring case; other columns are
 * ignored, so a downloaded file of failed rows imports again as it is. `display_name` is optional.
 */
export function readImportRows(text: string): ImportRowsResult {
  const [header, ...data] = parseCsv(text);
  if (!header) return { ok: false, error: 'empty' };
  const names = header.map(cell => cell.trim().toLowerCase());
  const columnOf = (key: keyof typeof importHeaders) =>
    names.findIndex(name => (importHeaders[key] as readonly string[]).includes(name));
  const address = columnOf('address');
  const displayName = columnOf('display_name');
  const members = columnOf('members');
  if (address < 0 || members < 0) return { ok: false, error: 'missing_columns' };
  if (data.length === 0) return { ok: false, error: 'empty' };
  if (data.length > MAILBOX_IMPORT_MAX_ROWS) return { ok: false, error: 'too_many_rows' };
  return {
    ok: true,
    rows: data.map(row => ({
      address: row[address] ?? '',
      display_name: displayName < 0 ? '' : (row[displayName] ?? ''),
      members: row[members] ?? '',
    })),
  };
}
