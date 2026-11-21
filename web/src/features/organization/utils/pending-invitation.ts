/**
 * An invitation link carries its one-time token in the URL fragment, which browsers never send to
 * a server or in a referrer. The `/invite` page keeps it in this tab's session storage while the
 * invitee signs in or signs up, and the organization page offers it in the accept dialog.
 */
const storageKey = 'zmail.pendingInvitation';

/** The token in a `#token` fragment, or null when there is none or it does not decode. */
export function readInvitationFragment(hash: string): string | null {
  const raw = hash.startsWith('#') ? hash.slice(1) : hash;
  if (raw === '') return null;
  try {
    const token = decodeURIComponent(raw).trim();
    return token === '' ? null : token;
  } catch {
    return null;
  }
}

export function stashPendingInvitation(token: string): void {
  try {
    window.sessionStorage.setItem(storageKey, token);
  } catch {
    // Without storage the invitee pastes the token from the mail.
  }
}

/** The token left by an invitation link in this tab, if any. */
export function takePendingInvitation(): string | null {
  try {
    return window.sessionStorage.getItem(storageKey);
  } catch {
    return null;
  }
}

export function clearPendingInvitation(): void {
  try {
    window.sessionStorage.removeItem(storageKey);
  } catch {
    // Nothing stored.
  }
}
