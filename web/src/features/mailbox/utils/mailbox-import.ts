import { MAILBOX_MAX_MEMBERS, MAILBOX_PERMISSIONS, normalizeRecipient } from '@/features/mailbox/schemas';
import type { MailboxPermission } from '@/features/mailbox/types';

/**
 * The cell rules of mailbox import and export in `contracts/MAILBOXES.md`, shared by the import
 * page and the development mock. The API remains the authority.
 */

const formulaStart = /^[=+\-@]/u;

/** Puts an apostrophe before a cell starting with =, +, -, or @, as the export does. */
export function guardFormulaCell(value: string): string {
  return formulaStart.test(value) ? `'${value}` : value;
}

/** Removes the one apostrophe an export put before =, +, -, or @. */
export function stripFormulaGuard(value: string): string {
  return value.startsWith("'") && formulaStart.test(value.slice(1)) ? value.slice(1) : value;
}

export interface ImportMember {
  email: string;
  permission: MailboxPermission;
}

/**
 * Reads a members cell: `email[:permission]` joined by `;`, blank segments skipped, `send` by
 * default, 1 to 100 members, each email once ignoring case. Null for anything else.
 */
export function parseImportMembers(cell: string): ImportMember[] | null {
  const members: ImportMember[] = [];
  const seen = new Set<string>();
  for (const raw of cell.split(';')) {
    const segment = raw.trim();
    if (segment === '') continue;
    const colon = segment.indexOf(':');
    const email = normalizeRecipient(colon < 0 ? segment : segment.slice(0, colon));
    const permission = colon < 0 ? 'send' : segment.slice(colon + 1).trim().toLowerCase();
    if (email === null || !(MAILBOX_PERMISSIONS as readonly string[]).includes(permission)) {
      return null;
    }
    if (seen.has(email)) return null;
    seen.add(email);
    members.push({ email, permission: permission as MailboxPermission });
  }
  return members.length === 0 || members.length > MAILBOX_MAX_MEMBERS ? null : members;
}
