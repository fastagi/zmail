import {
  MAILBOX_MAX_PER_ORGANIZATION,
  normalizeDisplayName,
  normalizeLocalPart,
} from '@/features/mailbox/schemas';
import type {
  MemberImportInput,
  MemberImportResult,
  MemberImportRowResult,
} from '@/features/mailbox/types';
import {
  mockMemberWelcomeStore,
  type MockWelcomeCandidate,
} from '@/features/mailbox/server/mock-member-welcome-store';
import { stripFormulaGuard } from '@/features/mailbox/utils/mailbox-import';
import { ApiErrorCode } from '@/http/codes';
import { managesOrganization } from '@/features/mailbox/server/mock-store/helpers';
import { createMailbox } from '@/features/mailbox/server/mock-store/mailbox-admin-store';
import type { MockMailboxState } from '@/features/mailbox/server/mock-store/state';
import {
  MAX_ROSTER_EMAIL_LENGTH,
  MOCK_MAILBOX_DOMAIN,
  ROSTER_EMAIL_PATTERN,
  type MailboxState,
  type MailboxViewer,
  type MockImportDomainResolver,
  type StoredMailbox,
} from '@/features/mailbox/server/mock-store/types';

/**
 * The roster import mock, mirroring the API's `ImportMembers`: each row reconciles toward a
 * pending invitation plus a personal mailbox reserved for its email (or, for a current member
 * without one, their own mailbox), so sending the same rows again changes nothing.
 */
export function importRosterMembers(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  input: MemberImportInput,
  resolveDomain: MockImportDomainResolver
): { ok: true; value: MemberImportResult } | { ok: false; error: 'forbidden' } {
  if (!managesOrganization(viewer)) return { ok: false, error: 'forbidden' };
  const members = new Map<string, number>();
  for (const [userId, email] of ctx.dependencies.memberEmails(organizationId)) {
    members.set(email, userId);
  }
  const defaultDomain = input.domain.trim().toLowerCase().replace(/\.$/u, '');
  const seenEmails = new Set<string>();
  const seenAddresses = new Set<string>();
  let planned = ctx.mailboxesOf(organizationId).size;
  const result: MemberImportResult = {
    dry_run: input.dryRun,
    total: input.rows.length,
    valid: 0,
    invited: 0,
    created: 0,
    skipped: 0,
    failed: 0,
    welcome_mails_queued: 0,
    welcome_mails_failed: 0,
    rows: [],
  };

  input.rows.forEach((raw, index) => {
    const email = stripFormulaGuard(raw.email.trim()).trim().toLowerCase();
    let address = '';
    const push = (
      status: MemberImportRowResult['status'],
      extra: Partial<MemberImportRowResult> = {}
    ) => {
      const mailboxId = extra.mailbox_id ?? null;
      const provisioned =
        !input.dryRun &&
        mailboxId !== null &&
        (status === 'invited' ||
          status === 'created' ||
          (status === 'skipped' && extra.skip_reason === 'already_invited'));
      const welcome = provisioned
        ? mockMemberWelcomeStore.welcome(organizationId, mailboxId)
        : null;
      result.rows.push({
        row: index + 1,
        email,
        address,
        status,
        skip_reason: null,
        error_code: null,
        mailbox_id: null,
        invitation_id: null,
        email_send_status: null,
        welcome_mail_status: welcome,
        welcome_mail_error_code: null,
        ...extra,
      });
      result[status] += 1;
      if (welcome === 'queued') result.welcome_mails_queued += 1;
    };
    const fail = (code: string) => push('failed', { error_code: code });

    const duplicateEmail = seenEmails.has(email);
    if (email !== '') seenEmails.add(email);
    if (email === '' || email.length > MAX_ROSTER_EMAIL_LENGTH || !ROSTER_EMAIL_PATTERN.test(email)) {
      return fail(ApiErrorCode.MAILBOX_IMPORT_EMAIL_INVALID);
    }
    const roleText = raw.role.trim().toLowerCase();
    if (roleText !== '' && roleText !== 'member' && roleText !== 'admin') {
      return fail(ApiErrorCode.MAILBOX_IMPORT_ROLE_INVALID);
    }
    const role = roleText === 'admin' ? 'admin' : 'member';
    const mailboxText = stripFormulaGuard(raw.mailbox.trim()).trim().toLowerCase();
    const at = mailboxText.lastIndexOf('@');
    let rawLocal = at >= 0 ? mailboxText.slice(0, at) : mailboxText;
    const domainName =
      at >= 0 ? mailboxText.slice(at + 1).replace(/\.$/u, '') : defaultDomain;
    if (rawLocal === '') rawLocal = email.slice(0, email.indexOf('@')).split('+')[0] ?? '';
    const localPart = normalizeLocalPart(rawLocal, {
      sharedDomain: domainName === MOCK_MAILBOX_DOMAIN,
    });
    if (!localPart.ok) {
      return fail(
        localPart.reason === 'reserved'
          ? ApiErrorCode.MAILBOX_ADDRESS_RESERVED
          : ApiErrorCode.MAILBOX_ADDRESS_INVALID
      );
    }
    if (domainName === '' || domainName === MOCK_MAILBOX_DOMAIN) {
      return fail(ApiErrorCode.MAILBOX_IMPORT_DOMAIN_REQUIRED);
    }
    address = `${localPart.value}@${domainName}`;
    const displayName = normalizeDisplayName(stripFormulaGuard(raw.name));
    if (displayName === null) return fail(ApiErrorCode.MAILBOX_ADDRESS_INVALID);
    const duplicateAddress = seenAddresses.has(address);
    seenAddresses.add(address);
    if (duplicateEmail || duplicateAddress) {
      return fail(ApiErrorCode.MAILBOX_IMPORT_DUPLICATE_ROW);
    }
    const placement = resolveDomain(domainName);
    if (!placement.ok) {
      return fail(
        placement.error === 'not_verified'
          ? ApiErrorCode.MAILBOX_DOMAIN_NOT_VERIFIED
          : ApiErrorCode.MAILBOX_DOMAIN_NOT_FOUND
      );
    }

    const reservation = () => reservationFor(ctx, organizationId, address, email);
    const plan = (): string | null => {
      if (planned >= MAILBOX_MAX_PER_ORGANIZATION) return ApiErrorCode.MAILBOX_LIMIT_REACHED;
      planned += 1;
      return null;
    };

    const memberId = members.get(email);
    if (memberId !== undefined) {
      if (ownsPersonal(ctx, organizationId, memberId)) {
        return push('skipped', { skip_reason: 'already_member' });
      }
      const reserved = reservation();
      if (reserved === 'taken') return push('skipped', { skip_reason: 'mailbox_taken' });
      if (reserved) {
        if (input.dryRun) return push('valid', { mailbox_id: reserved.mailbox.id });
        claimReservedMailboxes(ctx, organizationId, memberId, email);
        return push('created', { mailbox_id: reserved.mailbox.id });
      }
      if (input.dryRun) {
        const limited = plan();
        return limited ? fail(limited) : push('valid');
      }
      const created = createMailbox(
        ctx,
        organizationId,
        viewer,
        {
          local_part: localPart.value,
          display_name: displayName,
          type: 'personal',
          owner_user_id: memberId,
        },
        placement.name
      );
      if (!created.ok) return fail(ApiErrorCode.MAILBOX_LIMIT_REACHED);
      return push('created', { mailbox_id: created.mailbox.id });
    }

    const pending = ctx.dependencies.rosterInviter.pendingInvitationIdFor(organizationId, email);
    const reserved = reservation();
    if (reserved === 'taken') return push('skipped', { skip_reason: 'mailbox_taken' });
    if (pending !== null && reserved) {
      return push('skipped', {
        skip_reason: 'already_invited',
        mailbox_id: reserved.mailbox.id,
        invitation_id: pending,
      });
    }
    let mailboxId: number | null = reserved ? reserved.mailbox.id : null;
    if (!reserved) {
      const limited = input.dryRun
        ? plan()
        : ctx.mailboxesOf(organizationId).size >= MAILBOX_MAX_PER_ORGANIZATION
          ? ApiErrorCode.MAILBOX_LIMIT_REACHED
          : null;
      if (limited) return fail(limited);
      if (!input.dryRun) {
        mailboxId = reservePersonal(ctx, organizationId, viewer, address, displayName, email);
      }
    }
    if (input.dryRun) return push('valid', { mailbox_id: mailboxId });
    if (pending !== null) {
      return push('invited', { mailbox_id: mailboxId, invitation_id: pending });
    }
    const invitation = ctx.dependencies.rosterInviter.inviteRosterMember(
      organizationId,
      email,
      role,
      input.features
    );
    return push('invited', {
      mailbox_id: mailboxId,
      invitation_id: invitation.id,
      email_send_status: invitation.email_send_status,
    });
  });
  return { ok: true, value: result };
}

/**
 * The mailboxes a welcome mail goes to, oldest first: Zmail personal mailboxes on an organization
 * domain reserved for an invitee or owned by a current member other than the viewer.
 */
export function listWelcomeCandidates(
  ctx: MockMailboxState,
  organizationId: number,
  viewerUserId: number
): MockWelcomeCandidate[] {
  return ctx
    .statesOf(organizationId)
    .filter(({ mailbox, access }) => {
      if (access.type !== 'personal' || access.origin !== 'zmail' || access.ownerLeft) return false;
      if (mailbox.address.endsWith(`@${MOCK_MAILBOX_DOMAIN}`)) return false;
      return access.ownerUserId === null
        ? Boolean(access.pendingOwnerEmail)
        : access.ownerUserId !== viewerUserId;
    })
    .sort((left, right) => left.mailbox.id - right.mailbox.id)
    .map(({ mailbox, access }) => ({
      mailbox_id: mailbox.id,
      address: mailbox.address,
      display_name: mailbox.display_name,
      owner_user_id: access.ownerUserId,
      invitation_pending: access.ownerUserId === null,
    }));
}

/**
 * Gives `userId` every personal mailbox reserved for `email` in the organization, as the API
 * does on `member_joined`; access history records `owner_assigned`.
 */
export function claimReservedMailboxes(
  ctx: MockMailboxState,
  organizationId: number,
  userId: number,
  email: string
): number[] {
  const claimed: number[] = [];
  for (const state of ctx.mailboxesOf(organizationId).values()) {
    if (state.access.pendingOwnerEmail !== email || state.access.ownerUserId !== null) continue;
    state.access.ownerUserId = userId;
    delete state.access.pendingOwnerEmail;
    ctx.record(state, 'owner_assigned', null, userId, {});
    claimed.push(state.mailbox.id);
  }
  return claimed;
}

/** The live reservation of `address` for `email`, null when the address is free, else taken. */
function reservationFor(
  ctx: MockMailboxState,
  organizationId: number,
  address: string,
  email: string
): MailboxState | null | 'taken' {
  for (const state of ctx.mailboxesOf(organizationId).values()) {
    if (state.mailbox.address !== address) continue;
    return state.access.type === 'personal' &&
      state.access.ownerUserId === null &&
      state.access.pendingOwnerEmail === email
      ? state
      : 'taken';
  }
  return ctx.claimedAddresses.has(address) ? 'taken' : null;
}

function ownsPersonal(ctx: MockMailboxState, organizationId: number, userId: number): boolean {
  for (const state of ctx.mailboxesOf(organizationId).values()) {
    const { access } = state;
    if (access.type === 'personal' && access.origin === 'zmail' && access.ownerUserId === userId) {
      return true;
    }
  }
  return false;
}

/** A personal mailbox with no owner, held for `email`: nobody reads it until they join. */
function reservePersonal(
  ctx: MockMailboxState,
  organizationId: number,
  viewer: MailboxViewer,
  address: string,
  displayName: string,
  email: string
): number {
  const mailbox: StoredMailbox = {
    id: ctx.nextMailboxId++,
    address,
    display_name: displayName,
    created_at: ctx.timestamp(),
  };
  const state: MailboxState = {
    mailbox,
    access: {
      type: 'personal',
      origin: 'zmail',
      ownerUserId: null,
      ownerLeft: null,
      sealedBefore: null,
      sealedThroughMessageId: 0,
      webhooksEnabled: false,
      members: new Map(),
      events: [],
      pendingOwnerEmail: email,
    },
    roleId: null,
    messages: new Map(),
    reads: new Map(),
    stars: new Map(),
    folders: new Map(),
    labels: new Map(),
    files: new Map(),
  };
  ctx.record(state, 'created', viewer.userId, null, { type: 'personal' });
  ctx.mailboxesOf(organizationId).set(mailbox.id, state);
  ctx.claimedAddresses.add(address);
  return mailbox.id;
}
