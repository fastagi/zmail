import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { MockMailboxStore } from '@/features/mailbox/server/mock-mailbox-store';
import type { MemberImportResult, MemberImportRowResult } from '@/features/mailbox/types';
import { readRosterRows, ROSTER_IMPORT_TEMPLATE } from '@/features/mailbox/utils/roster-csv';
import { MockOrganizationStore } from '@/features/organization/server/mock-organization-store';
import { messages } from '@/i18n/modules';

const service = vi.hoisted(() => ({ domains: vi.fn() }));
const admin = vi.hoisted(() => ({ importMembers: vi.fn() }));
const toast = vi.hoisted(() => ({ success: vi.fn(), warning: vi.fn(), error: vi.fn() }));

vi.mock('sonner', () => ({ toast }));
vi.mock('@/features/mailbox/services/mailbox-service', () => ({ mailboxService: service }));
vi.mock('@/features/mailbox/services/mailbox-admin-service', () => ({
  mailboxAdminService: admin,
}));

const { RosterImportDialog, mergeRetry } =
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

describe('roster import mock', () => {
  const features = { broadcasts: true, contacts: false, templates: true, api_keys: false };
  const owner = { userId: 1, role: 'owner' as const };
  const domain = (name: string) =>
    name === 'news.example.com'
      ? ({ ok: true, name } as const)
      : ({ ok: false, error: 'not_found' } as const);

  function stores() {
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

describe('roster import dialog', () => {
  beforeEach(() => {
    service.domains.mockReset();
    admin.importMembers.mockReset();
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
          row(1, 'invited', { email: rows[0].email, email_send_status: 'accepted_by_provider' }),
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
          row(2, 'invited', { email: rows[2].email, email_send_status: 'not_configured' }),
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
      await screen.findByText('3 rows: 2 ready, 0 need no change, 1 with problems')
    ).toBeInTheDocument();
    expect(admin.importMembers).toHaveBeenLastCalledWith(1, {
      dryRun: true,
      domain: 'news.example.com',
      features: { broadcasts: false, contacts: true, templates: true, api_keys: false },
      rows,
    });
    const failedRow = screen
      .getByText('Enter a valid email of up to 100 characters.')
      .closest('tr');
    expect(failedRow).toHaveAttribute('data-status', 'failed');

    fireEvent.click(screen.getByRole('button', { name: 'Import 2 rows' }));
    expect(
      await screen.findByText('3 rows: 1 invited, 0 mailboxes created, 0 skipped, 2 failed')
    ).toBeInTheDocument();
    expect(admin.importMembers).toHaveBeenLastCalledWith(
      1,
      expect.objectContaining({ dryRun: false, rows })
    );

    fireEvent.click(screen.getByRole('button', { name: 'Retry 2 failed rows' }));
    await waitFor(() =>
      expect(admin.importMembers).toHaveBeenLastCalledWith(
        1,
        expect.objectContaining({ dryRun: false, rows: [rows[1], rows[2]] })
      )
    );
    expect(
      await screen.findByText('3 rows: 2 invited, 0 mailboxes created, 0 skipped, 1 failed')
    ).toBeInTheDocument();
    const bobRow = screen.getByText('bob@example.com').closest('tr');
    expect(bobRow).not.toBeNull();
    expect(
      within(bobRow as HTMLElement).getByText(
        'Invitation created, but no email went out. Resend it from the Invitations tab.'
      )
    ).toBeInTheDocument();
    expect(toast.warning).toHaveBeenCalled();
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
