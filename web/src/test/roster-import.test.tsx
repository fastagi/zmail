import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { createTranslator, NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { memberImportResultSchema } from '@/features/mailbox/schemas';
import { MockMailboxStore } from '@/features/mailbox/server/mock-mailbox-store';
import { mockMemberWelcomeStore } from '@/features/mailbox/server/mock-member-welcome-store';
import type { MemberImportResult, MemberImportRowResult } from '@/features/mailbox/types';
import { readRosterRows, ROSTER_IMPORT_TEMPLATE } from '@/features/mailbox/utils/roster-csv';
import { MockOrganizationStore } from '@/features/organization/server/mock-organization-store';
import { messages } from '@/i18n/modules';
import zhMailbox from '@/i18n/modules/mailbox/zh-Hans';

const service = vi.hoisted(() => ({ domains: vi.fn() }));
const admin = vi.hoisted(() => ({ importMembers: vi.fn() }));
const toast = vi.hoisted(() => ({ success: vi.fn(), warning: vi.fn(), error: vi.fn() }));
const welcome = vi.hoisted(() => ({ setting: vi.fn() }));

vi.mock('sonner', () => ({ toast }));
vi.mock('@/features/mailbox/services/mailbox-service', () => ({ mailboxService: service }));
vi.mock('@/features/mailbox/services/mailbox-admin-service', () => ({
  mailboxAdminService: admin,
}));
vi.mock('@/features/mailbox/services/member-welcome-service', () => ({
  memberWelcomeService: welcome,
}));

const { RosterImportDialog, mergeRetry, rosterSummary, rosterToast } =
  await import('@/features/mailbox/components/admin/roster-import-dialog');

describe('roster CSV', () => {
  it('reads the template and Chinese headers, and ignores unknown columns', () => {
    expect(readRosterRows(ROSTER_IMPORT_TEMPLATE)).toEqual({
      ok: true,
      rows: [
        { email: 'alice@example.com', name: 'Alice Wang', role: 'member', mailbox: 'alice' },
        { email: 'bob@example.com', name: 'Bob Li', role: 'admin', mailbox: '' },
      ],
    });
    expect(readRosterRows('﻿邮箱,姓名,部门\r\ncarol@example.com,Carol,Sales\r\n')).toEqual({
      ok: true,
      rows: [{ email: 'carol@example.com', name: 'Carol', role: '', mailbox: '' }],
    });
  });

  it('reads cells pasted from a spreadsheet as tab-separated text', () => {
    expect(readRosterRows('email\tmailbox\ndan@example.com\tdan.w\n\n')).toEqual({
      ok: true,
      rows: [{ email: 'dan@example.com', name: '', role: '', mailbox: 'dan.w' }],
    });
  });

  it('refuses text without an email column, without rows, or over 500 rows', () => {
    expect(readRosterRows('name,mailbox\nAlice,alice')).toEqual({
      ok: false,
      error: 'missing_columns',
    });
    expect(readRosterRows('email\n')).toEqual({ ok: false, error: 'empty' });
    expect(readRosterRows('')).toEqual({ ok: false, error: 'empty' });
    const many = ['email', ...Array.from({ length: 501 }, (_, i) => `u${i}@example.com`)];
    expect(readRosterRows(many.join('\n'))).toEqual({ ok: false, error: 'too_many_rows' });
  });
});

function row(
  index: number,
  status: MemberImportRowResult['status'],
  extra: Partial<MemberImportRowResult> = {}
): MemberImportRowResult {
  return {
    row: index,
    email: `u${index}@example.com`,
    address: `u${index}@news.example.com`,
    status,
    skip_reason: null,
    error_code: null,
    mailbox_id: null,
    invitation_id: null,
    email_send_status: null,
    welcome_mail_status: null,
    welcome_mail_error_code: null,
    ...extra,
  };
}

function result(rows: MemberImportRowResult[], dryRun = false): MemberImportResult {
  const count = (status: MemberImportRowResult['status']) =>
    rows.filter(item => item.status === status).length;
  return {
    dry_run: dryRun,
    total: rows.length,
    valid: count('valid'),
    invited: count('invited'),
    created: count('created'),
    skipped: count('skipped'),
    failed: count('failed'),
    welcome_mails_queued: rows.filter(item => item.welcome_mail_status === 'queued').length,
    welcome_mails_failed: rows.filter(
      item => item.welcome_mail_status === 'failed' || item.welcome_mail_status === 'suppressed'
    ).length,
    rows,
  };
}

describe('mergeRetry', () => {
  it('replaces only the retried rows and counts again', () => {
    const previous = result([
      row(1, 'invited'),
      row(2, 'failed', { error_code: 'MAILBOX.LIMIT_REACHED' }),
      row(3, 'skipped', { skip_reason: 'already_member' }),
      row(4, 'failed', { error_code: 'COMMON.SERVICE_UNAVAILABLE' }),
    ]);
    const retried = result([
      row(1, 'invited', { email: 'u2@example.com' }),
      row(2, 'failed', { email: 'u4@example.com' }),
    ]);
    const merged = mergeRetry(previous, [1, 3], retried);
    expect(merged.rows.map(item => [item.row, item.email, item.status])).toEqual([
      [1, 'u1@example.com', 'invited'],
      [2, 'u2@example.com', 'invited'],
      [3, 'u3@example.com', 'skipped'],
      [4, 'u4@example.com', 'failed'],
    ]);
    expect(merged).toMatchObject({ total: 4, invited: 2, skipped: 1, failed: 1 });
  });
});

describe('rosterSummary', () => {
  it('counts a taken address as needing attention and invited mailboxes as reserved', () => {
    const summary = rosterSummary(
      result([
        row(1, 'invited', { mailbox_id: 11 }),
        row(2, 'invited', { mailbox_id: 12 }),
        row(3, 'created', { mailbox_id: 13 }),
        row(4, 'skipped', { skip_reason: 'mailbox_taken' }),
        row(5, 'skipped', { skip_reason: 'already_member' }),
        row(6, 'skipped', { skip_reason: 'already_invited', mailbox_id: 14 }),
        row(7, 'failed', { error_code: 'MAILBOX.IMPORT_EMAIL_INVALID' }),
      ])
    );
    expect(summary).toEqual({
      total: 7,
      ready: 0,
      invited: 2,
      reserved: 2,
      created: 1,
      noop: 2,
      attention: 2,
    });
  });
});

describe('welcome email failures', () => {
  it('reads an import result from an API without welcome mail as none sent', () => {
    const legacy = {
      ...result([row(1, 'invited', { mailbox_id: 11 })]),
      welcome_mails_queued: undefined,
      welcome_mails_failed: undefined,
      rows: [
        {
          ...row(1, 'invited'),
          welcome_mail_status: undefined,
          welcome_mail_error_code: undefined,
        },
      ],
    };
    const parsed = memberImportResultSchema.parse(JSON.parse(JSON.stringify(legacy)));
    expect(parsed).toMatchObject({ welcome_mails_queued: 0, welcome_mails_failed: 0 });
    expect(parsed.rows[0]?.welcome_mail_status).toBeNull();
  });

  it('need attention and are recounted after a retry', () => {
    const previous = result([
      row(1, 'invited', { mailbox_id: 11, welcome_mail_status: 'failed' }),
      row(2, 'invited', { mailbox_id: 12, welcome_mail_status: 'queued' }),
    ]);
    expect(rosterSummary(previous).attention).toBe(1);
    const retried = result([
      row(1, 'skipped', {
        skip_reason: 'already_invited',
        mailbox_id: 11,
        welcome_mail_status: 'queued',
      }),
    ]);
    const merged = mergeRetry(previous, [0], retried);
    expect(merged).toMatchObject({ welcome_mails_queued: 2, welcome_mails_failed: 0 });
    expect(rosterSummary(merged).attention).toBe(0);
  });
});

describe('roster import mock', () => {
  const features = {
    broadcasts: true,
    contacts: false,
    templates: true,
    api_keys: false,
    mail_clients: true,
  };
  const owner = { userId: 1, role: 'owner' as const };
  const domain = (name: string) =>
    name === 'news.example.com'
      ? ({ ok: true, name } as const)
      : ({ ok: false, error: 'not_found' } as const);

  function stores() {
    mockMemberWelcomeStore.reset();
    const organization = new MockOrganizationStore({
      now: () => new Date('2026-11-23T10:00:00Z'),
      invitationToken: () => 'oinv_test.roster',
    });
    const mailbox = new MockMailboxStore({
      now: () => new Date('2026-11-23T10:00:00Z'),
      membershipOf: (org, user) => organization.membershipIdOf(org, user),
      memberEmails: org => organization.memberEmails(org),
      rosterInviter: organization,
    });
    organization.onMemberJoined((org, user, email) => mailbox.claimReserved(org, user, email));
    return { organization, mailbox };
  }

  it('invites, reserves, stays idempotent, and gives the mailbox to the person who joins', () => {
    const { organization, mailbox } = stores();
    const rows = [
      { email: 'Erin+hr@Gmail.com', name: 'Erin', role: '', mailbox: '' },
      { email: 'member@example.com', name: 'Riley', role: 'member', mailbox: 'riley' },
      { email: 'bad', name: '', role: '', mailbox: '' },
      { email: 'ops@example.com', name: '', role: 'owner', mailbox: '' },
      { email: 'erin+hr@gmail.com', name: '', role: '', mailbox: 'other' },
    ];

    const dry = mailbox.importMembers(
      1,
      owner,
      {
        dryRun: true,
        domain: 'news.example.com',
        features,
        rows,
      },
      domain
    );
    expect(dry.ok && dry.value).toMatchObject({ valid: 2, failed: 3, invited: 0 });
    expect(mailbox.list(1, owner).some(item => item.address.endsWith('@news.example.com'))).toBe(
      false
    );

    const first = mailbox.importMembers(
      1,
      owner,
      {
        dryRun: false,
        domain: 'news.example.com',
        features,
        rows,
      },
      domain
    );
    if (!first.ok) throw new Error('import refused');
    // Each new mailbox gets one welcome email.
    expect(first.value).toMatchObject({ welcome_mails_queued: 2, welcome_mails_failed: 0 });
    expect(first.value.rows.map(item => [item.address, item.status, item.error_code])).toEqual([
      ['erin@news.example.com', 'invited', null],
      ['riley@news.example.com', 'created', null],
      ['', 'failed', 'MAILBOX.IMPORT_EMAIL_INVALID'],
      ['', 'failed', 'MAILBOX.IMPORT_ROLE_INVALID'],
      ['other@news.example.com', 'failed', 'MAILBOX.IMPORT_DUPLICATE_ROW'],
    ]);

    // Nobody reads the reserved mailbox before the person joins.
    const reserved = mailbox.list(1, owner).find(item => item.address === 'erin@news.example.com');
    expect(reserved).toMatchObject({ type: 'personal', owner_user_id: null });
    expect(reserved?.access.view).toBe(false);

    const again = mailbox.importMembers(
      1,
      owner,
      {
        dryRun: false,
        domain: 'news.example.com',
        features,
        rows: rows.slice(0, 2),
      },
      domain
    );
    expect(again.ok && again.value.rows.map(item => item.skip_reason)).toEqual([
      'already_invited',
      'already_member',
    ]);
    expect(again.ok && again.value.rows[0]?.welcome_mail_status).toBe('already_sent');

    const joined = organization.acceptInvitation(
      { id: 'erin', email: 'erin+hr@gmail.com', name: 'Erin' },
      { token: 'oinv_test.roster' }
    );
    expect(joined).toMatchObject({ role: 'member', features });
    const erin = { userId: 4, role: 'member' as const };
    expect(mailbox.list(1, erin).map(item => item.address)).toContain('erin@news.example.com');
    expect(mailbox.list(1, { userId: 3, role: 'member' }).map(item => item.address)).not.toContain(
      'erin@news.example.com'
    );
  });

  it('refuses the shared domain, an unknown domain, and a member caller', () => {
    const { mailbox } = stores();
    const input = (mailboxValue: string) => ({
      dryRun: true,
      domain: 'news.example.com',
      features,
      rows: [{ email: 'frank@example.com', name: '', role: '', mailbox: mailboxValue }],
    });
    const shared = mailbox.importMembers(1, owner, input('frank@mail.example.com'), domain);
    const unknown = mailbox.importMembers(1, owner, input('frank@other.example.com'), domain);
    expect(shared.ok && shared.value.rows[0]?.error_code).toBe('MAILBOX.IMPORT_DOMAIN_REQUIRED');
    expect(unknown.ok && unknown.value.rows[0]?.error_code).toBe('MAILBOX.DOMAIN_NOT_FOUND');
    expect(mailbox.importMembers(1, { userId: 3, role: 'member' }, input(''), domain)).toEqual({
      ok: false,
      error: 'forbidden',
    });
  });
});

function renderDialog() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <RosterImportDialog organizationId={1} open onClose={vi.fn()} />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

function domainEntry(domain: string, kind: 'shared' | 'organization') {
  return {
    domain,
    kind,
    max_mailboxes: 50,
    provider: 'log',
    inbound_enabled: true,
    daily_send_limit: 200,
    daily_broadcast_limit: 1000,
    daily_transactional_limit: 1000,
    broadcasts_enabled: false,
    custom_domains_enabled: true,
  };
}

describe('rosterToast', () => {
  type Translate = Parameters<typeof rosterToast>[0];
  const translator = (locale: 'en-US' | 'zh-Hans') =>
    createTranslator({
      locale,
      messages: (locale === 'en-US'
        ? messages
        : { ...messages, mailbox: zhMailbox }) as unknown as AbstractIntlMessages,
      namespace: 'mailbox',
    }) as unknown as Translate;

  it('names only what happened, in either language', () => {
    const counts = { invited: 1, reserved: 1, created: 0, attention: 0 };
    expect(rosterToast(translator('zh-Hans'), counts)).toBe('已邀请 1 人，已为他们预留 1 个邮箱');
    expect(rosterToast(translator('en-US'), counts)).toBe(
      'Invited 1 person. Reserved 1 mailbox for them.'
    );
    expect(
      rosterToast(translator('en-US'), { invited: 0, reserved: 0, created: 2, attention: 1 })
    ).toBe('2 existing members got a mailbox. 1 row needs attention.');
    expect(
      rosterToast(translator('zh-Hans'), { invited: 0, reserved: 0, created: 0, attention: 0 })
    ).toBe('没有需要导入的新内容');
    expect(
      rosterToast(
        translator('zh-Hans'),
        { invited: 2, reserved: 0, created: 0, attention: 3 },
        {
          retried: true,
        }
      )
    ).toBe('重试完成，已邀请 2 人，3 行需要处理');
  });
});

describe('roster import dialog', () => {
  beforeEach(() => {
    service.domains.mockReset();
    admin.importMembers.mockReset();
    welcome.setting.mockReset();
    welcome.setting.mockResolvedValue({ ...mockMemberWelcomeStore.setting(1), language: 'en-US' });
    for (const fn of Object.values(toast)) fn.mockReset();
  });

  it('asks for a verified domain first and links to the domain page', async () => {
    service.domains.mockResolvedValue([domainEntry('mail.zmail.test', 'shared')]);
    renderDialog();
    expect(await screen.findByText('Connect a domain first')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Connect a domain' })).toHaveAttribute(
      'href',
      '/console/admin/domains'
    );
  });

  it('previews with row errors, imports, and retries only the failed rows', async () => {
    service.domains.mockResolvedValue([
      domainEntry('mail.zmail.test', 'shared'),
      domainEntry('news.example.com', 'organization'),
    ]);
    const rows = [
      { email: 'alice@example.com', name: 'Alice', role: 'member', mailbox: 'alice' },
      { email: 'bad', name: '', role: '', mailbox: '' },
      { email: 'bob@example.com', name: 'Bob', role: 'admin', mailbox: '' },
    ];
    admin.importMembers
      .mockResolvedValueOnce(
        result(
          [
            row(1, 'valid', { email: rows[0].email }),
            row(2, 'failed', {
              email: 'bad',
              address: '',
              error_code: 'MAILBOX.IMPORT_EMAIL_INVALID',
            }),
            row(3, 'valid', { email: rows[2].email }),
          ],
          true
        )
      )
      .mockResolvedValueOnce(
        result([
          row(1, 'invited', {
            email: rows[0].email,
            mailbox_id: 11,
            email_send_status: 'accepted_by_provider',
            welcome_mail_status: 'queued',
          }),
          row(2, 'failed', {
            email: 'bad',
            address: '',
            error_code: 'MAILBOX.IMPORT_EMAIL_INVALID',
          }),
          row(3, 'failed', { email: rows[2].email, error_code: 'COMMON.SERVICE_UNAVAILABLE' }),
        ])
      )
      .mockResolvedValueOnce(
        result([
          row(1, 'failed', {
            email: 'bad',
            address: '',
            error_code: 'MAILBOX.IMPORT_EMAIL_INVALID',
          }),
          row(2, 'invited', {
            email: rows[2].email,
            mailbox_id: 13,
            email_send_status: 'not_configured',
          }),
        ])
      );

    renderDialog();
    const paste = await screen.findByLabelText('Or paste rows');
    fireEvent.change(paste, {
      target: {
        value:
          'email,name,role,mailbox\nalice@example.com,Alice,member,alice\nbad,,,\nbob@example.com,Bob,admin,\n',
      },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Check rows' }));

    expect(
      await screen.findByText('3 rows: 2 ready, 0 need no change, 1 need attention')
    ).toBeInTheDocument();
    expect(admin.importMembers).toHaveBeenLastCalledWith(1, {
      dryRun: true,
      domain: 'news.example.com',
      features: {
        broadcasts: false,
        contacts: true,
        templates: true,
        api_keys: false,
        mail_clients: true,
      },
      rows,
    });
    const failedRow = screen
      .getAllByText('Enter a valid email of up to 100 characters.')[0]
      ?.closest('tr');
    expect(failedRow).toHaveAttribute('data-status', 'failed');
    // A bad email is not fixed by another prefix, so the row offers none.
    expect(screen.queryByLabelText('New mailbox prefix for bad')).toBeNull();

    // Before importing, the admin learns that the new members get a welcome email, and where to
    // change it.
    expect(
      await screen.findByText(
        'After import, each of the 2 new members gets a welcome email on how to sign in and set up a mail app.'
      )
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Edit or turn off in Invitations' })).toHaveAttribute(
      'href',
      '/console/organizations/1?tab=invitations'
    );

    fireEvent.click(screen.getByRole('button', { name: 'Import 2 rows' }));
    expect(
      await screen.findByText(
        '3 rows: 1 invited with 1 mailbox reserved, 0 mailboxes created for existing members, 0 need no change, 2 need attention'
      )
    ).toBeInTheDocument();
    expect(admin.importMembers).toHaveBeenLastCalledWith(
      1,
      expect.objectContaining({ dryRun: false, rows })
    );

    expect(toast.warning).toHaveBeenLastCalledWith(
      'Invited 1 person. Reserved 1 mailbox for them. 2 rows need attention.'
    );
    expect(
      screen.getByText('Welcome emails: 1 sent to new members, 0 could not be sent.')
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Retry 2 rows that need attention' }));
    await waitFor(() =>
      expect(admin.importMembers).toHaveBeenLastCalledWith(
        1,
        expect.objectContaining({ dryRun: false, rows: [rows[1], rows[2]] })
      )
    );
    expect(
      await screen.findByText(
        '3 rows: 2 invited with 2 mailboxes reserved, 0 mailboxes created for existing members, 0 need no change, 1 need attention'
      )
    ).toBeInTheDocument();
    const bobRow = screen.getByText('bob@example.com').closest('tr');
    expect(bobRow).not.toBeNull();
    expect(
      within(bobRow as HTMLElement).getAllByText(
        'Invitation created, but no email went out. Resend it from the Invitations tab.'
      )
    ).toHaveLength(2);
    expect(toast.warning).toHaveBeenCalled();
  });

  it('flags a taken address, lets the admin pick a new prefix, and retries that row', async () => {
    service.domains.mockResolvedValue([domainEntry('news.example.com', 'organization')]);
    const rows = [
      { email: 'alice@example.com', name: 'Alice', role: 'admin', mailbox: '' },
      { email: 'carol@example.com', name: 'Carol', role: '', mailbox: 'ops' },
      { email: 'dave@example.com', name: 'Dave', role: '', mailbox: '' },
    ];
    const taken = row(2, 'skipped', {
      email: rows[1].email,
      address: 'ops@news.example.com',
      skip_reason: 'mailbox_taken',
    });
    const invitedBefore = row(3, 'skipped', {
      email: rows[2].email,
      address: 'dave@news.example.com',
      skip_reason: 'already_invited',
      mailbox_id: 30,
    });
    admin.importMembers
      .mockResolvedValueOnce(
        result(
          [
            row(1, 'valid', { email: rows[0].email, address: 'alice@news.example.com' }),
            taken,
            invitedBefore,
          ],
          true
        )
      )
      .mockResolvedValueOnce(
        result([
          row(1, 'invited', {
            email: rows[0].email,
            address: 'alice@news.example.com',
            mailbox_id: 10,
            email_send_status: 'accepted_by_provider',
          }),
          taken,
          invitedBefore,
        ])
      )
      .mockResolvedValueOnce(
        result([
          row(1, 'invited', {
            email: rows[1].email,
            address: 'carol.w@news.example.com',
            mailbox_id: 20,
            email_send_status: 'accepted_by_provider',
          }),
        ])
      );

    renderDialog();
    fireEvent.change(await screen.findByLabelText('Or paste rows'), {
      target: {
        value:
          'email,name,role,mailbox\nalice@example.com,Alice,admin,\ncarol@example.com,Carol,,ops\ndave@example.com,Dave,,\n',
      },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Check rows' }));

    // A taken address is not a no-op: the person is never invited.
    expect(
      await screen.findByText('3 rows: 1 ready, 1 need no change, 1 need attention')
    ).toBeInTheDocument();
    const carolRow = screen.getByText('carol@example.com').closest('tr') as HTMLElement;
    expect(carolRow).toHaveAttribute('data-attention', 'true');
    expect(within(carolRow).getAllByText('Address taken').length).toBeGreaterThan(0);
    const daveRow = screen.getByText('dave@example.com').closest('tr') as HTMLElement;
    expect(daveRow).toHaveAttribute('data-attention', 'false');
    // The concrete plan and the role show in the preview.
    expect(
      screen.getAllByText('Will invite them and reserve alice@news.example.com.').length
    ).toBeGreaterThan(0);
    expect(screen.getAllByText('Admin').length).toBeGreaterThan(0);

    fireEvent.click(screen.getByRole('button', { name: 'Import 1 row' }));
    expect(
      await screen.findByText(
        '3 rows: 1 invited with 1 mailbox reserved, 0 mailboxes created for existing members, 1 need no change, 1 need attention'
      )
    ).toBeInTheDocument();
    expect(toast.warning).toHaveBeenLastCalledWith(
      'Invited 1 person. Reserved 1 mailbox for them. 1 row needs attention.'
    );

    fireEvent.change(screen.getByLabelText('New mailbox prefix for carol@example.com'), {
      target: { value: 'carol.w' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Retry 1 row that needs attention' }));
    await waitFor(() =>
      expect(admin.importMembers).toHaveBeenLastCalledWith(
        1,
        expect.objectContaining({ dryRun: false, rows: [{ ...rows[1], mailbox: 'carol.w' }] })
      )
    );
    expect(
      await screen.findByText(
        '3 rows: 2 invited with 2 mailboxes reserved, 0 mailboxes created for existing members, 1 need no change, 0 need attention'
      )
    ).toBeInTheDocument();
    expect(toast.success).toHaveBeenLastCalledWith(
      'Retried. Invited 1 person. Reserved 1 mailbox for them.'
    );
  });

  it('says before importing that welcome emails are off', async () => {
    service.domains.mockResolvedValue([domainEntry('news.example.com', 'organization')]);
    welcome.setting.mockResolvedValue({ ...mockMemberWelcomeStore.setting(1), enabled: false });
    admin.importMembers.mockResolvedValueOnce(
      result([row(1, 'valid', { email: 'alice@example.com' })], true)
    );
    renderDialog();
    fireEvent.change(await screen.findByLabelText('Or paste rows'), {
      target: { value: 'email\nalice@example.com\n' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Check rows' }));
    expect(
      await screen.findByText('Welcome emails are turned off, so importing sends none.')
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Turn on in Invitations' })).toHaveAttribute(
      'href',
      '/console/organizations/1?tab=invitations'
    );
  });

  it('says why text cannot be read before calling the API', async () => {
    service.domains.mockResolvedValue([domainEntry('news.example.com', 'organization')]);
    renderDialog();
    fireEvent.change(await screen.findByLabelText('Or paste rows'), {
      target: { value: 'name\nAlice' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Check rows' }));
    expect(await screen.findByText('The first line needs an email column.')).toBeInTheDocument();
    expect(admin.importMembers).not.toHaveBeenCalled();
  });
});
