import {
  MAILBOX_MAX_PER_ORGANIZATION,
  normalizeDisplayName,
  normalizeLocalPart,
} from '@/features/mailbox/schemas';
import type {
  CreateMailboxInput,
  Mailbox,
  MailboxDomain,
  MailboxImportResult,
  MailboxImportRowResult,
  MailboxListScope,
  MailboxType,
} from '@/features/mailbox/types';
import { mockTransactionalStore } from '@/features/developer/server/mock-transactional-store';
import {
  guardFormulaCell,
  parseImportMembers,
  stripFormulaGuard,
} from '@/features/mailbox/utils/mailbox-import';
import { ApiErrorCode } from '@/http/codes';
import {
  anyAccess,
  awaitsHandover,
  csvField,
  isMembersOwnAccount,
  mailboxAccessFor,
  managesOrganization,
} from '@/features/mailbox/server/mock-store/helpers';
import type { MockMailboxState } from '@/features/mailbox/server/mock-store/state';
import {
  EXPORT_HEADER,
  MAX_MOCK_EXPORT_ROWS,
  MOCK_DAILY_SEND_LIMIT,
  MOCK_MAILBOX_DOMAIN,
  type MailboxState,
  type MailboxViewer,
  type MockDirectoryPage,
  type MockDirectoryQuery,
  type MockImportDomainResolver,
  type MockLedgerDirectory,
  type MockMailDeleteResult,
  type MockMailboxCreateResult,
  type StoredMailbox,
} from '@/features/mailbox/server/mock-store/types';

/**
 * The shared domain first, then the organization's verified domains.
 * `defaultDomain` is the organization's default; without one the shared domain is the default.
 */
export function listDomains(
  verifiedDomains: readonly string[] = [],
  defaultDomain: string | null = null
): MailboxDomain[] {
  const settings = {
    max_mailboxes: MAILBOX_MAX_PER_ORGANIZATION,
    provider: 'log' as const,
    inbound_enabled: false,
    daily_send_limit: MOCK_DAILY_SEND_LIMIT,
    daily_broadcast_limit: MOCK_DAILY_SEND_LIMIT,
    daily_transactional_limit: MOCK_DAILY_SEND_LIMIT,
    transactional_recipient_limit_per_minute: 3,
    transactional_key_limit_per_second: 20,
    simulator_domain: 'test.zmail.im',
    broadcasts_enabled: true,
    custom_domains_enabled: true,
    // The instance switch of the transactional mock; on by default so the console is usable.
    transactional_enabled: mockTransactionalStore.isEnabled(),
  };
  return [
    { domain: MOCK_MAILBOX_DOMAIN, kind: 'shared', default: defaultDomain === null, ...settings },
    ...verifiedDomains.map(domain => ({
      domain,
      kind: 'organization' as const,
      default: domain === defaultDomain,
      ...settings,
    })),
  ];
}

/** The organization's live mailboxes with an address on the domain. */
export function countMailboxesOnDomain(
  ctx: MockMailboxState,
  organizationId: number,
  name: string
): number {
  let count = 0;
  for (const state of ctx.organizations.get(organizationId)?.values() ?? []) {
    if (state.mailbox.address.endsWith(`@${name}`)) count += 1;
  }
  return count;
}

/** Whether a live mailbox of any organization has an address on the domain. */
export function domainInUse(ctx: MockMailboxState, name: string): boolean {
  for (const mailboxes of ctx.organizations.values()) {
    for (const state of mailboxes.values()) {
      if (state.mailbox.address.endsWith(`@${name}`)) return true;
    }
  }
  return false;
}

/**
 * Mailboxes the viewer has any access to, oldest first, as the API lists them. `usable` keeps
 * those the viewer reads or sends from, and for owners and admins those awaiting handover.
 */
export function listMailboxes(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  scope?: MailboxListScope
): Mailbox[] {
  return ctx
    .statesOf(organizationId)
    .flatMap(state => {
      const access = mailboxAccessFor(state.access, viewer);
      if (!anyAccess(access)) return [];
      const awaiting = awaitsHandover(state.access);
      if (
        scope === 'usable' &&
        !access.view &&
        !access.send &&
        !(awaiting && managesOrganization(viewer))
      ) {
        return [];
      }
      return [ctx.view(state, viewer, access)];
    })
    .sort((left, right) => left.id - right.id);
}

/** `addressDomain` is the shared domain or a verified domain the route already resolved. */
export function createMailbox(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  input: Omit<CreateMailboxInput, 'type'> & { type?: unknown },
  addressDomain: string = MOCK_MAILBOX_DOMAIN
): MockMailboxCreateResult {
  const localPart = normalizeLocalPart(input.local_part, {
    sharedDomain: addressDomain === MOCK_MAILBOX_DOMAIN,
  });
  if (!localPart.ok) return { ok: false, error: localPart.reason };
  const displayName = normalizeDisplayName(input.display_name ?? '');
  if (displayName === null) return { ok: false, error: 'invalid' };
  const access = ctx.initialAccess(organizationId, input);
  if (!access) return { ok: false, error: 'access_invalid' };

  const address = `${localPart.value}@${addressDomain}`;
  // The API checks the organization's limit before the address, so an organization at the
  // limit learns about the limit even when the address is also taken.
  const mailboxes = ctx.mailboxesOf(organizationId);
  if (mailboxes.size >= MAILBOX_MAX_PER_ORGANIZATION) return { ok: false, error: 'limit' };
  if (ctx.claimedAddresses.has(address)) return { ok: false, error: 'taken' };

  const mailbox: StoredMailbox = {
    id: ctx.nextMailboxId++,
    address,
    display_name: displayName,
    created_at: ctx.timestamp(),
  };
  const state: MailboxState = {
    mailbox,
    access,
    roleId: null,
    messages: new Map(),
    reads: new Map(),
    stars: new Map(),
    folders: new Map(),
    labels: new Map(),
    files: new Map(),
  };
  for (const member of access.members.values()) member.granted_by = viewer.userId;
  ctx.record(state, 'created', viewer.userId, access.ownerUserId, { type: access.type });
  for (const member of access.members.values()) {
    ctx.record(state, 'member_set', viewer.userId, member.user_id, {
      membership_id: ctx.dependencies.membershipOf(organizationId, member.user_id),
      permission: member.permission,
    });
  }
  mailboxes.set(mailbox.id, state);
  ctx.claimedAddresses.add(address);
  ctx.store(state, ctx.welcomeMessage(mailbox));
  // As in the API, a create response reports 0 unread.
  return { ok: true, mailbox: { ...ctx.view(state, viewer), unread_count: 0 } };
}

/**
 * The `external` mailbox of a newly bound outside account, owned by `ownerUserId` alone, with a
 * few synced copies so the development inbox shows them. The address is the outside account's,
 * never claimed, and the mailbox does not count toward the organization's limit.
 */
export function createExternalMailbox(
  ctx: MockMailboxState,
  organizationId: number,
  ownerUserId: number,
  address: string,
  provider: string,
  backfillDays: number
): number {
  const mailbox: StoredMailbox = {
    id: ctx.nextMailboxId++,
    address,
    display_name: address,
    created_at: ctx.timestamp(),
  };
  const state: MailboxState = {
    mailbox,
    access: {
      type: 'personal',
      origin: 'connected',
      ownerUserId,
      ownerLeft: null,
      sealedBefore: null,
      sealedThroughMessageId: 0,
      webhooksEnabled: false,
      members: new Map(),
      events: [],
    },
    roleId: null,
    messages: new Map(),
    reads: new Map(),
    stars: new Map(),
    folders: new Map(),
    labels: new Map(),
    files: new Map(),
  };
  ctx.record(state, 'created', null, ownerUserId, { type: 'personal' });
  ctx.mailboxesOf(organizationId).set(mailbox.id, state);
  syncExternalMailbox(ctx, organizationId, mailbox.id, provider, backfillDays);
  return mailbox.id;
}

/** Records whether a connected mailbox sends through its account's SMTP server. */
export function setConnectedMailboxSend(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  sends: boolean
): void {
  const state = ctx.mailboxesOf(organizationId).get(mailboxId);
  if (state && state.access.origin === 'connected') state.access.connectedSend = sends;
}

/** Copies one more synced message into an external mailbox, as a sync would. */
export function syncExternalMailbox(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  provider: string,
  backfillDays: number
): void {
  const state = ctx.mailboxesOf(organizationId).get(mailboxId);
  if (!state || state.access.origin !== 'connected') return;
  const now = ctx.dependencies.now().getTime();
  const samples = [
    { hoursAgo: 2, subject: 'Your order has shipped', from: 'orders@shop.example' },
    { hoursAgo: 30, subject: 'Notes from Tuesday', from: 'sam@partner.example' },
    {
      hoursAgo: 24 * Math.min(backfillDays, 10),
      subject: 'Welcome aboard',
      from: 'hi@news.example',
    },
  ];
  const stored = new Set([...state.messages.values()].map(message => message.message_id));
  for (const [index, sample] of samples.entries()) {
    // A copy is stored once per provider message id, so a rebind never adds a second one.
    const providerMessageId = `mock-${mailboxId}-${index}@${provider}.example`;
    if (stored.has(providerMessageId)) continue;
    const message = ctx.welcomeMessage(state.mailbox);
    ctx.store(state, {
      ...message,
      from: { address: sample.from, name: '' },
      subject: sample.subject,
      source: provider,
      created_at: new Date(now - sample.hoursAgo * 60 * 60 * 1_000).toISOString(),
      text_body: 'A read-only copy synced by the development mock connector.',
      html_body:
        '<p>A read-only copy synced by the <strong>development mock</strong> connector.</p>',
      message_id: providerMessageId,
    });
  }
}

/**
 * Deletes a connected mailbox and its synced copies for good, as a purge of a revoked connection
 * does; answers how many copies went, or null when there is no such connected mailbox.
 */
export function removeExternalMailbox(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number
): number | null {
  const mailboxes = ctx.mailboxesOf(organizationId);
  const state = mailboxes.get(mailboxId);
  if (!state || state.access.origin !== 'connected') return null;
  mailboxes.delete(mailboxId);
  return state.messages.size;
}

/** One page of the administration directory, newest first; owners and admins only. */
export function listMailboxDirectory(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  query: MockDirectoryQuery
): { ok: true; value: MockDirectoryPage } | { ok: false; error: 'forbidden' } {
  if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
  const needle = query.q?.trim().toLowerCase() ?? '';
  const states = ctx
    .statesOf(organizationId)
    .sort((left, right) => right.mailbox.id - left.mailbox.id);
  const matching = states.filter(
    state =>
      !isMembersOwnAccount(state.access) &&
      (query.type === undefined || state.access.type === query.type) &&
      (query.beforeId === undefined || state.mailbox.id < query.beforeId) &&
      (needle === '' ||
        state.mailbox.address.toLowerCase().includes(needle) ||
        state.mailbox.display_name.toLowerCase().includes(needle))
  );
  const page = matching.slice(0, query.perPage);
  const items = page.map(state => ctx.directoryEntry(state));
  return {
    ok: true,
    value: {
      items,
      nextBeforeId:
        matching.length > query.perPage ? (page[page.length - 1]?.mailbox.id ?? null) : null,
      mailboxCount: states.length,
      sentLast24h: states.reduce((total, state) => total + ctx.sentLast24h(state), 0),
    },
  };
}

/** Changes the display name and records `renamed`; owners and admins only. */
export function renameMailbox(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer,
  rawDisplayName: string
):
  | { ok: true; mailbox: Mailbox }
  | { ok: false; error: 'forbidden' | 'mailbox_not_found' | 'invalid' } {
  if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
  const state = ctx.stateOf(organizationId, mailboxId);
  if (!state) return { ok: false, error: 'mailbox_not_found' };
  const displayName = normalizeDisplayName(rawDisplayName);
  if (displayName === null) return { ok: false, error: 'invalid' };
  const from = state.mailbox.display_name;
  if (from !== displayName) {
    state.mailbox.display_name = displayName;
    ctx.record(state, 'renamed', viewer.userId, null, { from, to: displayName });
  }
  return { ok: true, mailbox: ctx.view(state, viewer) };
}

/**
 * Checks or creates shared mailboxes from import rows with the API's rules and row codes. A
 * real run creates each passing row through the create path; a dry run counts the limit in row
 * order and changes nothing.
 */
export function importMailboxRows(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  rows: readonly { address: string; display_name: string; members: string }[],
  dryRun: boolean,
  resolveDomain: MockImportDomainResolver
): { ok: true; value: MailboxImportResult } | { ok: false; error: 'forbidden' } {
  if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
  const emails = new Map<string, number>();
  for (const [userId, email] of ctx.dependencies.memberEmails(organizationId)) {
    emails.set(email, userId);
  }
  const seen = new Set<string>();
  let planned = ctx.mailboxesOf(organizationId).size;
  const result: MailboxImportResult = { dry_run: dryRun, succeeded: 0, failed: 0, rows: [] };
  rows.forEach((raw, index) => {
    const address = stripFormulaGuard(raw.address.trim()).trim();
    const outcome = (
      status: MailboxImportRowResult['status'],
      extra: Partial<MailboxImportRowResult> = {}
    ) => {
      result.rows.push({
        row: index + 1,
        address,
        status,
        mailbox_id: null,
        error_code: null,
        missing_emails: [],
        ...extra,
      });
      if (status === 'failed') result.failed += 1;
      else result.succeeded += 1;
    };
    const fail = (code: string, missing: string[] = []) =>
      outcome('failed', { error_code: code, missing_emails: missing });

    const key = address.toLowerCase();
    const duplicate = seen.has(key);
    seen.add(key);
    const at = address.lastIndexOf('@');
    if (at <= 0 || at === address.length - 1) return fail(ApiErrorCode.MAILBOX_ADDRESS_INVALID);
    if (duplicate) return fail(ApiErrorCode.MAILBOX_IMPORT_DUPLICATE_ROW);
    const displayName = normalizeDisplayName(stripFormulaGuard(raw.display_name));
    if (displayName === null) return fail(ApiErrorCode.MAILBOX_ADDRESS_INVALID);
    const members = parseImportMembers(stripFormulaGuard(raw.members.trim()));
    if (members === null) return fail(ApiErrorCode.MAILBOX_ACCESS_INVALID);
    const missing = members.filter(member => !emails.has(member.email)).map(item => item.email);
    if (missing.length > 0) return fail(ApiErrorCode.MAILBOX_IMPORT_MEMBER_NOT_FOUND, missing);
    const localPart = normalizeLocalPart(address.slice(0, at), {
      sharedDomain: address.slice(at + 1).toLowerCase() === MOCK_MAILBOX_DOMAIN,
    });
    if (!localPart.ok) {
      return fail(
        localPart.reason === 'reserved'
          ? ApiErrorCode.MAILBOX_ADDRESS_RESERVED
          : ApiErrorCode.MAILBOX_ADDRESS_INVALID
      );
    }
    const placement = resolveDomain(address.slice(at + 1));
    if (!placement.ok) {
      return fail(
        placement.error === 'not_verified'
          ? ApiErrorCode.MAILBOX_DOMAIN_NOT_VERIFIED
          : ApiErrorCode.MAILBOX_DOMAIN_NOT_FOUND
      );
    }
    if (ctx.claimedAddresses.has(`${localPart.value}@${placement.name}`)) {
      return fail(ApiErrorCode.MAILBOX_ADDRESS_TAKEN);
    }
    if (dryRun) {
      if (planned >= MAILBOX_MAX_PER_ORGANIZATION) return fail(ApiErrorCode.MAILBOX_LIMIT_REACHED);
      planned += 1;
      return outcome('valid');
    }
    const created = createMailbox(
      ctx,
      organizationId,
      viewer,
      {
        local_part: localPart.value,
        display_name: displayName,
        type: 'shared',
        members: members.map(member => ({
          user_id: emails.get(member.email) ?? 0,
          permission: member.permission,
        })),
      },
      placement.name
    );
    if (created.ok) return outcome('created', { mailbox_id: created.mailbox.id });
    const codes = {
      invalid: ApiErrorCode.MAILBOX_ADDRESS_INVALID,
      reserved: ApiErrorCode.MAILBOX_ADDRESS_RESERVED,
      taken: ApiErrorCode.MAILBOX_ADDRESS_TAKEN,
      limit: ApiErrorCode.MAILBOX_LIMIT_REACHED,
      access_invalid: ApiErrorCode.MAILBOX_ACCESS_INVALID,
    } as const;
    return fail(codes[created.error]);
  });
  return { ok: true, value: result };
}

/** Every mailbox of one type as the API's CSV file, newest first; owners and admins only. */
export function exportMailboxesCsv(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  type: MailboxType
): { ok: true; csv: string; filename: string } | { ok: false; error: 'forbidden' } {
  if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
  const emails = ctx.dependencies.memberEmails(organizationId);
  const lines = ctx
    .statesOf(organizationId)
    .filter(state => state.access.type === type && !isMembersOwnAccount(state.access))
    .sort((left, right) => right.mailbox.id - left.mailbox.id)
    .slice(0, MAX_MOCK_EXPORT_ROWS)
    .map(state => {
      const entry = ctx.directoryEntry(state);
      const members: string[] = [];
      const owner = state.access.ownerUserId;
      if (owner !== null && emails.has(owner)) members.push(emails.get(owner) ?? '');
      for (const member of state.access.members.values()) {
        const email = emails.get(member.user_id);
        if (email) members.push(`${email}:${member.permission}`);
      }
      members.sort();
      return [
        guardFormulaCell(entry.address),
        guardFormulaCell(entry.display_name),
        guardFormulaCell(members.join(';')),
        String(entry.member_count),
        String(entry.sent_last_24h),
        new Date(entry.created_at).toISOString().replace(/\.\d{3}Z$/u, 'Z'),
      ]
        .map(csvField)
        .join(',');
    });
  const day = ctx.dependencies.now().toISOString().slice(0, 10).replaceAll('-', '');
  return {
    ok: true,
    csv: `\uFEFF${[EXPORT_HEADER, ...lines].join('\r\n')}\r\n`,
    filename: `${type}-mailboxes-${day}.csv`,
  };
}

/** Deletes the mailbox and its messages; the address stays claimed. Needs `administer`. */
export function deleteMailbox(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number,
  viewer: MailboxViewer
): MockMailDeleteResult {
  const found = ctx.authorize(organizationId, mailboxId, viewer, 'administer');
  if (!found.ok) return found;
  ctx.mailboxesOf(organizationId).delete(mailboxId);
  return { ok: true };
}

/** True when the organization has the mailbox. */
export function hasMailbox(
  ctx: MockMailboxState,
  organizationId: number,
  mailboxId: number
): boolean {
  return ctx.mailboxesOf(organizationId).has(mailboxId);
}

/**
 * What the platform account ledger mock needs to judge a request as the API does: each mailbox
 * with whether the viewer may read it or see it at all, the organization's role ids, and who is a
 * current member.
 */
export function buildLedgerDirectory(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer
): MockLedgerDirectory {
  const mailboxes = new Map<number, { address: string; readable: boolean; visible: boolean }>();
  for (const state of ctx.statesOf(organizationId)) {
    const access = mailboxAccessFor(state.access, viewer);
    mailboxes.set(state.mailbox.id, {
      address: state.mailbox.address,
      readable: access.view,
      visible: anyAccess(access),
    });
  }
  return {
    mailboxes,
    roleIds: new Set(ctx.rolesOf(organizationId).keys()),
    isMember: userId => ctx.dependencies.membershipOf(organizationId, userId) !== null,
  };
}
