import 'server-only';

import {
  PLATFORM_ACCOUNT_NOTES_MAX_LENGTH,
  PLATFORM_ACCOUNT_STATUSES,
  PLATFORM_HANDLE_MAX_LENGTH,
  PLATFORM_NAME_MAX_LENGTH,
  PLATFORM_PROFILE_URL_MAX_LENGTH,
  type PlatformAccount,
  type PlatformAccountEvent,
  type PlatformAccountEventKind,
  type PlatformAccountFilters,
  type PlatformAccountHandover,
  type PlatformAccountInput,
  type PlatformAccountStatus,
} from '@/features/mailbox/platform-account-schemas';
import type {
  MailboxViewer,
  MockLedgerDirectory,
} from '@/features/mailbox/server/mock-mailbox-store';

/**
 * Development mock of the platform account ledger (`contracts/MAILBOXES.md`, Platform Accounts).
 * It reproduces the API's rules: owners and admins write; members see only the accounts
 * registered with a mailbox they may read or that they own; every status, owner, and mailbox change
 * records an event; a deleted account's events are no longer listed.
 */

export type MockLedgerError = 'forbidden' | 'not_found' | 'taken';
export type MockLedgerResult<T> =
  | { ok: true; value: T }
  | { ok: false; error: MockLedgerError }
  | { ok: false; error: 'validation'; fields: Record<string, string[]> };

type StoredAccount = Omit<PlatformAccount, 'mailbox_address'>;

const maxMockOrganizations = 1_000;

function manages(viewer: MailboxViewer): boolean {
  return viewer.role === 'owner' || viewer.role === 'admin';
}

function hasControl(value: string): boolean {
  return /[\u0000-\u001f\u007f-\u009f]/u.test(value);
}

function validProfileUrl(raw: string): boolean {
  if (new TextEncoder().encode(raw).length > PLATFORM_PROFILE_URL_MAX_LENGTH || hasControl(raw)) {
    return false;
  }
  try {
    const parsed = new URL(raw);
    return (
      (parsed.protocol === 'http:' || parsed.protocol === 'https:') &&
      parsed.hostname !== '' &&
      parsed.username === '' &&
      parsed.password === ''
    );
  } catch {
    return false;
  }
}

function idText(id: number | null): string | null {
  return id === null ? null : String(id);
}

export class MockPlatformAccountStore {
  private nextAccountId = 1;
  private nextEventId = 1;
  private readonly accounts = new Map<number, Map<number, StoredAccount>>();
  private readonly events = new Map<number, PlatformAccountEvent[]>();

  constructor(private readonly now: () => Date = () => new Date()) {}

  list(
    organizationId: number,
    viewer: MailboxViewer,
    directory: MockLedgerDirectory,
    filters: PlatformAccountFilters
  ): PlatformAccount[] {
    const q = (filters.q ?? '').trim().toLowerCase();
    return [...this.accountsOf(organizationId).values()]
      .filter(account => this.visible(account, viewer, directory))
      .filter(account => {
        if (filters.roleId !== undefined && account.role_id !== filters.roleId) return false;
        if (filters.mailboxId !== undefined && account.mailbox_id !== filters.mailboxId)
          return false;
        if (filters.ownerUserId !== undefined && account.owner_user_id !== filters.ownerUserId) {
          return false;
        }
        if (filters.status !== undefined && account.status !== filters.status) return false;
        if (!q) return true;
        const mailbox =
          account.mailbox_id === null ? undefined : directory.mailboxes.get(account.mailbox_id);
        const address =
          mailbox && (manages(viewer) || mailbox.visible) ? mailbox.address.toLowerCase() : '';
        return (
          account.platform.toLowerCase().includes(q) ||
          account.handle.toLowerCase().includes(q) ||
          address.includes(q)
        );
      })
      .sort(
        (left, right) =>
          compare(left.platform.toLowerCase(), right.platform.toLowerCase()) ||
          compare(left.handle.toLowerCase(), right.handle.toLowerCase()) ||
          left.id - right.id
      )
      .map(account => this.view(account, viewer, directory));
  }

  create(
    organizationId: number,
    viewer: MailboxViewer,
    directory: MockLedgerDirectory,
    input: PlatformAccountInput
  ): MockLedgerResult<PlatformAccount> {
    if (!manages(viewer)) return { ok: false, error: 'forbidden' };
    const checked = this.check(directory, input);
    if (!checked.ok) return checked;
    const accounts = this.accountsOf(organizationId);
    if (this.taken(accounts, checked.value)) return { ok: false, error: 'taken' };
    const now = this.now().toISOString();
    const account: StoredAccount = {
      id: this.nextAccountId++,
      ...checked.value,
      created_at: now,
      updated_at: now,
    };
    accounts.set(account.id, account);
    this.record(account.id, 'created', viewer.userId, null, account.status);
    return { ok: true, value: this.view(account, viewer, directory) };
  }

  update(
    organizationId: number,
    viewer: MailboxViewer,
    directory: MockLedgerDirectory,
    accountId: number,
    input: PlatformAccountInput
  ): MockLedgerResult<PlatformAccount> {
    if (!manages(viewer)) return { ok: false, error: 'forbidden' };
    const accounts = this.accountsOf(organizationId);
    const before = accounts.get(accountId);
    if (!before) return { ok: false, error: 'not_found' };
    const checked = this.check(directory, input);
    if (!checked.ok) return checked;
    if (this.taken(accounts, checked.value, accountId)) return { ok: false, error: 'taken' };
    const account: StoredAccount = {
      ...before,
      ...checked.value,
      updated_at: this.now().toISOString(),
    };
    accounts.set(accountId, account);
    if (before.status !== account.status) {
      this.record(accountId, 'status_changed', viewer.userId, before.status, account.status);
    }
    if (before.owner_user_id !== account.owner_user_id) {
      this.record(
        accountId,
        'owner_changed',
        viewer.userId,
        idText(before.owner_user_id),
        idText(account.owner_user_id)
      );
    }
    if (before.mailbox_id !== account.mailbox_id) {
      this.record(
        accountId,
        'mailbox_changed',
        viewer.userId,
        idText(before.mailbox_id),
        idText(account.mailbox_id)
      );
    }
    return { ok: true, value: this.view(account, viewer, directory) };
  }

  remove(organizationId: number, viewer: MailboxViewer, accountId: number): MockLedgerResult<null> {
    if (!manages(viewer)) return { ok: false, error: 'forbidden' };
    const accounts = this.accountsOf(organizationId);
    const account = accounts.get(accountId);
    if (!account) return { ok: false, error: 'not_found' };
    this.record(accountId, 'deleted', viewer.userId, account.status, null);
    accounts.delete(accountId);
    return { ok: true, value: null };
  }

  /** Newest first, with the listing's visibility. */
  eventsOf(
    organizationId: number,
    viewer: MailboxViewer,
    directory: MockLedgerDirectory,
    accountId: number
  ): MockLedgerResult<PlatformAccountEvent[]> {
    const account = this.accountsOf(organizationId).get(accountId);
    if (!account || !this.visible(account, viewer, directory)) {
      return { ok: false, error: 'not_found' };
    }
    return { ok: true, value: [...(this.events.get(accountId) ?? [])].reverse() };
  }

  handOver(
    organizationId: number,
    viewer: MailboxViewer,
    directory: MockLedgerDirectory,
    fromUserId: number,
    toUserId: number
  ): MockLedgerResult<PlatformAccountHandover> {
    if (!manages(viewer)) return { ok: false, error: 'forbidden' };
    if (toUserId === fromUserId) {
      return {
        ok: false,
        error: 'validation',
        fields: { to_user_id: ['must differ from from_user_id'] },
      };
    }
    if (!directory.isMember(toUserId)) {
      return {
        ok: false,
        error: 'validation',
        fields: { to_user_id: ['must be a current member of the organization'] },
      };
    }
    const moved: number[] = [];
    const now = this.now().toISOString();
    for (const account of this.accountsOf(organizationId).values()) {
      if (account.owner_user_id !== fromUserId) continue;
      account.owner_user_id = toUserId;
      account.updated_at = now;
      moved.push(account.id);
      this.record(account.id, 'owner_changed', viewer.userId, String(fromUserId), String(toUserId));
    }
    moved.sort((left, right) => left - right);
    return {
      ok: true,
      value: { from_user_id: fromUserId, to_user_id: toUserId, account_ids: moved },
    };
  }

  /** Test seam: forget every account and event. */
  reset(): void {
    this.nextAccountId = 1;
    this.nextEventId = 1;
    this.accounts.clear();
    this.events.clear();
  }

  private visible(
    account: StoredAccount,
    viewer: MailboxViewer,
    directory: MockLedgerDirectory
  ): boolean {
    if (manages(viewer) || account.owner_user_id === viewer.userId) return true;
    if (account.mailbox_id === null) return false;
    return directory.mailboxes.get(account.mailbox_id)?.readable === true;
  }

  private view(
    account: StoredAccount,
    viewer: MailboxViewer,
    directory: MockLedgerDirectory
  ): PlatformAccount {
    const mailbox =
      account.mailbox_id === null ? undefined : directory.mailboxes.get(account.mailbox_id);
    const shown = mailbox && (manages(viewer) || mailbox.visible) ? mailbox.address : null;
    // A deleted role or mailbox reads as null, as the API's ON DELETE SET NULL does.
    return {
      ...account,
      role_id:
        account.role_id !== null && directory.roleIds.has(account.role_id) ? account.role_id : null,
      mailbox_id: mailbox ? account.mailbox_id : null,
      mailbox_address: shown,
    };
  }

  private check(
    directory: MockLedgerDirectory,
    input: PlatformAccountInput
  ): MockLedgerResult<Omit<StoredAccount, 'id' | 'created_at' | 'updated_at'>> {
    const fields: Record<string, string[]> = {};
    const platform = input.platform.trim();
    const handle = input.handle.trim();
    const notes = (input.notes ?? '').trim();
    const profileUrl = (input.profile_url ?? '').trim();
    const status: PlatformAccountStatus = input.status ?? 'active';
    if (platform.length < 1 || [...platform].length > PLATFORM_NAME_MAX_LENGTH) {
      fields.platform = ['must be 1 to 64 characters after trimming'];
    } else if (hasControl(platform)) {
      fields.platform = ['must not contain control characters'];
    }
    if (handle.length < 1 || [...handle].length > PLATFORM_HANDLE_MAX_LENGTH) {
      fields.handle = ['must be 1 to 200 characters after trimming'];
    } else if (hasControl(handle)) {
      fields.handle = ['must not contain control characters'];
    }
    if ([...notes].length > PLATFORM_ACCOUNT_NOTES_MAX_LENGTH) {
      fields.notes = ['must be at most 2000 characters after trimming'];
    }
    if (profileUrl && !validProfileUrl(profileUrl)) {
      fields.profile_url = ['must be an absolute http or https URL'];
    }
    if (!PLATFORM_ACCOUNT_STATUSES.includes(status)) {
      fields.status = ['must be a known status'];
    }
    const roleId = input.role_id ?? null;
    if (roleId !== null && !directory.roleIds.has(roleId)) {
      fields.role_id = ['must be a mail role of the organization or null'];
    }
    const mailboxId = input.mailbox_id ?? null;
    if (mailboxId !== null && !directory.mailboxes.has(mailboxId)) {
      fields.mailbox_id = ['must be a mailbox of the organization or null'];
    }
    const ownerUserId = input.owner_user_id ?? null;
    if (ownerUserId !== null && !directory.isMember(ownerUserId)) {
      fields.owner_user_id = ['must be a current member of the organization or null'];
    }
    if (Object.keys(fields).length > 0) return { ok: false, error: 'validation', fields };
    return {
      ok: true,
      value: {
        platform,
        handle,
        profile_url: profileUrl || null,
        role_id: roleId,
        mailbox_id: mailboxId,
        owner_user_id: ownerUserId,
        status,
        notes,
      },
    };
  }

  private taken(
    accounts: Map<number, StoredAccount>,
    candidate: { platform: string; handle: string },
    exceptId?: number
  ): boolean {
    const platform = candidate.platform.toLowerCase();
    const handle = candidate.handle.toLowerCase();
    for (const account of accounts.values()) {
      if (account.id === exceptId) continue;
      if (account.platform.toLowerCase() === platform && account.handle.toLowerCase() === handle) {
        return true;
      }
    }
    return false;
  }

  private record(
    accountId: number,
    kind: PlatformAccountEventKind,
    actorUserId: number,
    from: string | null,
    to: string | null
  ): void {
    const events = this.events.get(accountId) ?? [];
    events.push({
      id: this.nextEventId++,
      account_id: accountId,
      kind,
      actor_user_id: actorUserId,
      from,
      to,
      created_at: this.now().toISOString(),
    });
    this.events.set(accountId, events);
  }

  private accountsOf(organizationId: number): Map<number, StoredAccount> {
    const existing = this.accounts.get(organizationId);
    if (existing) return existing;
    if (this.accounts.size >= maxMockOrganizations) {
      const oldest = this.accounts.keys().next().value;
      if (oldest !== undefined) this.accounts.delete(oldest);
    }
    const created = new Map<number, StoredAccount>();
    this.accounts.set(organizationId, created);
    return created;
  }
}

function compare(left: string, right: string): number {
  return left < right ? -1 : left > right ? 1 : 0;
}

export const mockPlatformAccountStore = new MockPlatformAccountStore();

/** One page of a mock listing; `hrefPrefix` is the path with its filters, ready for `page=`. */
export function mockLedgerPage<T>(
  items: T[],
  page: number,
  perPage: number,
  hrefPrefix: string
): {
  items: T[];
  meta: {
    current_page: number;
    per_page: number;
    total: number;
    last_page: number;
    from: number;
    to: number;
  };
  links: { first: string; last: string; prev: string | null; next: string | null };
} {
  const total = items.length;
  const lastPage = Math.max(1, Math.ceil(total / perPage));
  const start = (page - 1) * perPage;
  const data = items.slice(start, start + perPage);
  const href = (target: number) => `${hrefPrefix}page=${target}&per_page=${perPage}`;
  return {
    items: data,
    meta: {
      current_page: page,
      per_page: perPage,
      total,
      last_page: lastPage,
      from: data.length ? start + 1 : 0,
      to: data.length ? start + data.length : 0,
    },
    links: {
      first: href(1),
      last: href(lastPage),
      prev: page > 1 ? href(page - 1) : null,
      next: page < lastPage ? href(page + 1) : null,
    },
  };
}
