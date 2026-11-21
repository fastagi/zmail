/** Query parameter of the mail pages that names the organization they show. */
export const MAIL_ORGANIZATION_PARAM = 'org';

/** Query parameter naming the open mailbox; the organization stays in `org`. */
export const MAILBOX_PARAM = 'mailbox';

const storedMailboxPattern = /^[1-9]\d{0,15}$/u;

function storageKey(organizationId: number): string {
  return `zmail.mailbox.${organizationId}`;
}

/** The mailbox last opened in this organization, which a new message starts from. */
export function readStoredMailboxId(organizationId: number): number | null {
  try {
    const raw = window.localStorage.getItem(storageKey(organizationId)) ?? '';
    return storedMailboxPattern.test(raw) ? Number(raw) : null;
  } catch {
    return null;
  }
}

export function writeStoredMailboxId(organizationId: number, mailboxId: number): void {
  try {
    window.localStorage.setItem(storageKey(organizationId), String(mailboxId));
  } catch {
    // Storage may be unavailable; the URL still names the mailbox.
  }
}
