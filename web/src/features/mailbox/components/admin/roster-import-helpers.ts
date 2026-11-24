import type { MemberImportResult, MemberImportRowResult } from '@/features/mailbox/types';
import { importRowErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import type { useT } from '@/i18n';

export type MailboxT = ReturnType<typeof useT<'mailbox'>>;

/** Row codes a different mailbox prefix fixes; the row can take a new one inline. */
const PREFIX_ERROR_CODES: readonly string[] = [
  ApiErrorCode.MAILBOX_IMPORT_DUPLICATE_ROW,
  ApiErrorCode.MAILBOX_ADDRESS_INVALID,
  ApiErrorCode.MAILBOX_ADDRESS_RESERVED,
  ApiErrorCode.MAILBOX_ADDRESS_TAKEN,
];

/** A taken address leaves the person un-invited: the admin has to choose another prefix. */
export function isTaken(row: MemberImportRowResult) {
  return row.status === 'skipped' && row.skip_reason === 'mailbox_taken';
}

export function canEditPrefix(row: MemberImportRowResult) {
  return (
    isTaken(row) ||
    (row.status === 'failed' &&
      row.error_code !== null &&
      PREFIX_ERROR_CODES.includes(row.error_code))
  );
}

/**
 * Rows the admin must act on: refused rows, taken addresses, and rows whose welcome email failed,
 * which a retry sends. Other skips are no-ops.
 */
export function needsAttention(row: MemberImportRowResult) {
  return row.status === 'failed' || isTaken(row) || row.welcome_mail_status === 'failed';
}

export function noteTone(row: MemberImportRowResult) {
  if (row.status === 'failed') return 'text-error';
  return isTaken(row) ? 'text-warning' : 'text-muted-foreground';
}

export function statusVariant(status: MemberImportRowResult['status']) {
  switch (status) {
    case 'failed':
      return 'destructive' as const;
    case 'skipped':
      return 'outline' as const;
    case 'valid':
      return 'secondary' as const;
    default:
      return 'default' as const;
  }
}

/** The import toast, naming only what happened: clauses with a zero count are left out. */
export function rosterToast(
  t: MailboxT,
  counts: Pick<ReturnType<typeof rosterSummary>, 'invited' | 'reserved' | 'created' | 'attention'>,
  { retried = false }: { retried?: boolean } = {}
): string {
  const clauses = (['invited', 'reserved', 'created', 'attention'] as const)
    .filter(key => counts[key] > 0)
    .map(key => t(`admin.rosterImport.toast.${key}`, { count: counts[key] }));
  if (clauses.length === 0) clauses.push(t('admin.rosterImport.toast.nothing'));
  if (retried) clauses.unshift(t('admin.rosterImport.toast.retried'));
  return clauses.join(t('admin.rosterImport.toast.separator'));
}

/** The reason or next step of one row, in local copy; backend text is never shown. */
export function rowNote(t: MailboxT, row: MemberImportRowResult): string {
  switch (row.status) {
    case 'failed':
      return t(rosterRowErrorKey(row.error_code), { emails: '' });
    case 'skipped':
      return t(`admin.rosterImport.skip.${row.skip_reason ?? 'already_member'}`);
    case 'invited':
      return row.email_send_status === 'failed' || row.email_send_status === 'not_configured'
        ? t('admin.rosterImport.notes.invitedNoEmail')
        : t('admin.rosterImport.notes.invited');
    case 'created':
      return t('admin.rosterImport.notes.created');
    default:
      return t('admin.rosterImport.notes.valid', { address: row.address });
  }
}

/** Roster-only row codes have their own copy; the rest share the mailbox import's. */
export function rosterRowErrorKey(code: string | null) {
  switch (code) {
    case ApiErrorCode.MAILBOX_IMPORT_EMAIL_INVALID:
      return 'admin.rosterImport.rowErrors.emailInvalid' as const;
    case ApiErrorCode.MAILBOX_IMPORT_ROLE_INVALID:
      return 'admin.rosterImport.rowErrors.roleInvalid' as const;
    case ApiErrorCode.MAILBOX_IMPORT_DOMAIN_REQUIRED:
      return 'admin.rosterImport.rowErrors.domainRequired' as const;
    case ApiErrorCode.MAILBOX_IMPORT_DUPLICATE_ROW:
      return 'admin.rosterImport.rowErrors.duplicate' as const;
    default:
      return importRowErrorKey(code);
  }
}

/**
 * Counts for the summary lines. `reserved` counts the mailboxes held for invited people,
 * `noop` the skips that need nothing, and `attention` the failed rows plus taken addresses.
 */
export function rosterSummary(result: MemberImportResult) {
  const attention = result.rows.filter(needsAttention).length;
  return {
    total: result.total,
    ready: result.valid,
    invited: result.invited,
    reserved: result.rows.filter(row => row.status === 'invited' && row.mailbox_id !== null).length,
    created: result.created,
    noop: result.rows.filter(row => row.status === 'skipped' && !isTaken(row)).length,
    attention,
  };
}

/** Puts the retried rows' outcomes in place of the old ones and counts again. */
export function mergeRetry(
  previous: MemberImportResult,
  indexes: number[],
  next: MemberImportResult
): MemberImportResult {
  const rows = previous.rows.map(row => ({ ...row }));
  next.rows.forEach((row, position) => {
    const index = indexes[position];
    if (index !== undefined && rows[index]) rows[index] = { ...row, row: index + 1 };
  });
  const count = (status: MemberImportRowResult['status']) =>
    rows.filter(row => row.status === status).length;
  const welcome = (...statuses: MemberImportRowResult['welcome_mail_status'][]) =>
    rows.filter(row => statuses.includes(row.welcome_mail_status)).length;
  return {
    dry_run: false,
    total: rows.length,
    valid: count('valid'),
    invited: count('invited'),
    created: count('created'),
    skipped: count('skipped'),
    failed: count('failed'),
    welcome_mails_queued: welcome('queued'),
    welcome_mails_failed: welcome('failed', 'suppressed'),
    rows,
  };
}
