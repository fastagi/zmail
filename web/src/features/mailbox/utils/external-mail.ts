import { ROUTES } from '@/constants/routes';
import { MAIL_ORGANIZATION_PARAM } from '@/features/mailbox/components/mailbox-params';
import type {
  ExternalMailConnectionStatus,
  ExternalMailConnector,
} from '@/features/mailbox/external-mail-schemas';

/**
 * Display names of the connectors Zmail knows. They are product names, not copy, so they are not
 * translated; any other connector id is shown as the id itself, capitalized, since new connectors
 * add ids without a contract change.
 */
const PROVIDER_NAMES: Record<string, string> = {
  gmail: 'Gmail',
  outlook: 'Outlook',
  imap: 'IMAP',
  yahoo: 'Yahoo Mail',
  icloud: 'iCloud Mail',
};

export function providerName(id: string): string {
  const known = PROVIDER_NAMES[id];
  if (known) return known;
  const words = id.split('_').filter(Boolean);
  return words.length === 0
    ? id
    : words.map(word => word.charAt(0).toUpperCase() + word.slice(1)).join(' ');
}

/** The status dot: green while syncing, amber when the grant is lost, grey once unbound. */
export function statusDotClassName(status: ExternalMailConnectionStatus): string {
  if (status === 'active') return 'bg-success';
  if (status === 'needs_reconnect') return 'bg-warning';
  return 'bg-text-faint';
}

/**
 * What the callback page needs to finish a sign-in, kept in session storage under the `state` the
 * provider sends back. The provider never sees it: the organization stays out of the provider URL.
 */
export interface PendingExternalAuthorization {
  organizationId: number;
  provider: string;
  /** The backfill the member chose; null for a reconnect. */
  backfillDays: number | null;
  /** The connection being reconnected; null for a new binding. */
  connectionId: number | null;
}

const STORAGE_PREFIX = 'zmail.externalMailAuthorization.';
const MAX_STATE_LENGTH = 2_048;

export function rememberAuthorization(state: string, pending: PendingExternalAuthorization): void {
  if (state.length === 0 || state.length > MAX_STATE_LENGTH) return;
  try {
    window.sessionStorage.setItem(STORAGE_PREFIX + state, JSON.stringify(pending));
  } catch {
    // Without storage the callback page reports the sign-in as unknown and offers to start again.
  }
}

export function recallAuthorization(state: string): PendingExternalAuthorization | null {
  if (state.length === 0 || state.length > MAX_STATE_LENGTH) return null;
  try {
    const raw = window.sessionStorage.getItem(STORAGE_PREFIX + state);
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== 'object' || value === null) return null;
    const item = value as Record<string, unknown>;
    const organizationId = item.organizationId;
    const provider = item.provider;
    const backfillDays = item.backfillDays;
    const connectionId = item.connectionId;
    if (
      typeof organizationId !== 'number' ||
      !Number.isSafeInteger(organizationId) ||
      organizationId < 1 ||
      typeof provider !== 'string' ||
      !(backfillDays === null || (typeof backfillDays === 'number' && backfillDays > 0)) ||
      !(connectionId === null || (typeof connectionId === 'number' && connectionId > 0))
    ) {
      return null;
    }
    return { organizationId, provider, backfillDays, connectionId };
  } catch {
    return null;
  }
}

export function forgetAuthorization(state: string): void {
  try {
    window.sessionStorage.removeItem(STORAGE_PREFIX + state);
  } catch {
    // Nothing to forget.
  }
}

/** Sends the browser to the provider's sign-in page; a seam for tests. */
export function navigateToProvider(url: string): void {
  window.location.assign(url);
}

/** The callback page of the connect flow; the API's `EXTERNAL_MAIL_REDIRECT_URL` points here. */
export const EXTERNAL_MAIL_CALLBACK_PATH = '/console/mailboxes/connected/callback';

/** The inbox narrowed to a connected mailbox, as after a completed connect. */
export function connectedMailboxHref(organizationId: number, mailboxId: number): string {
  const params = new URLSearchParams({
    [MAIL_ORGANIZATION_PARAM]: String(organizationId),
    box: String(mailboxId),
  });
  return `${ROUTES.CONSOLE.MAILBOXES}?${params.toString()}`;
}

/**
 * Connectors Zmail knows bind with an app password. They are used only when the instance's
 * connector list could not be read, so a password connection never falls back to an OAuth
 * redirect; the list, when known, decides.
 */
const KNOWN_PASSWORD_CONNECTORS = new Set(['imap']);

/**
 * How a connection is reconnected: through the provider's sign-in page (`oauth`), the app password
 * form (`password`), or not at all because the instance no longer runs its connector
 * (`unavailable`). `connectors` is undefined while the list is unknown (loading or failed).
 */
export function reconnectKind(
  provider: string,
  connectors: readonly ExternalMailConnector[] | undefined
): 'oauth' | 'password' | 'unavailable' {
  if (connectors === undefined) {
    return KNOWN_PASSWORD_CONNECTORS.has(provider) ? 'password' : 'oauth';
  }
  const connector = connectors.find(item => item.id === provider);
  if (!connector) return 'unavailable';
  if (connector.auth_kind === 'password') return 'password';
  return connector.auth_kind === 'oauth2' ? 'oauth' : 'unavailable';
}
