import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { EmailLog } from '@/features/mailbox/components/email-log';
import {
  forwardDraft,
  quotedReplyDraft,
  replyAllDraft,
} from '@/features/mailbox/components/compose-dialog';
import { MailDomainStatus } from '@/features/mailbox/components/mail-domain-status';
import type { MailMessage } from '@/features/mailbox/types';
import {
  emailLogSearch,
  parseEmailLogView,
  type EmailLogView,
} from '@/features/mailbox/utils/email-log-view';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
const state = vi.hoisted(() => {
  const query = () => ({
    data: undefined as unknown,
    isPending: false,
    isSuccess: true,
    error: null as unknown,
    refetch: vi.fn(),
  });
  const mutation = () => ({ isPending: false, mutate: vi.fn(), mutateAsync: vi.fn() });
  return {
    organizations: query(),
    /** The organization id each mailbox hook was called with, in call order. */
    organizationIds: [] as number[],
    domains: query(),
    mailboxes: query(),
    log: query(),
    stats: query(),
    detail: query(),
    logCalls: [] as { filters: Record<string, unknown>; page: number }[],
    statsCalls: [] as number[],
    detailCalls: [] as { mailboxId: number; messageId: number | null }[],
    sendCalls: [] as number[],
    markRead: mutation(),
    send: mutation(),
    update: mutation(),
    removeMessage: mutation(),
  };
});

/** A router whose `replace` changes the URL the page reads, as the real one does. */
const navigation = vi.hoisted(() => {
  const listeners = new Set<() => void>();
  return {
    listeners,
    replace: vi.fn((url: string) => {
      window.history.replaceState(null, '', url);
      for (const listener of listeners) listener();
    }),
  };
});

vi.mock('sonner', () => ({ toast }));
vi.mock('next/navigation', async () => {
  const { useSyncExternalStore } = await import('react');
  const router = { replace: navigation.replace };
  const subscribe = (listener: () => void) => {
    navigation.listeners.add(listener);
    return () => navigation.listeners.delete(listener);
  };
  return {
    useRouter: () => router,
    usePathname: () => window.location.pathname,
    useSearchParams: () =>
      new URLSearchParams(useSyncExternalStore(subscribe, () => window.location.search)),
  };
});
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => state.organizations,
}));
const templateState = vi.hoisted(() => ({ data: [] as unknown[] }));
vi.mock('@/features/mailbox/hooks/use-mail-templates', () => ({
  useMailTemplates: () => templateState,
}));
vi.mock('@/features/mailbox/hooks/use-mail-drafts', () => ({
  useMailDrafts: () => ({ data: [], isPending: false, isError: false }),
  useOpenMailDraft: () => ({ mutate: () => undefined }),
  useSaveMailDraft: () => ({ mutateAsync: () => Promise.resolve({ id: 1 }) }),
  useDeleteMailDraft: () => ({
    mutate: () => undefined,
    mutateAsync: () => Promise.resolve(),
    isPending: false,
  }),
}));
vi.mock('@/features/mailbox/hooks/use-mail-signature', () => ({
  useMailSignature: () => ({ data: undefined }),
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', () => {
  const scoped = <T,>(organizationId: number, result: T): T => {
    state.organizationIds.push(organizationId);
    return result;
  };
  return {
    MAIL_STATS_DAYS: 7,
    useMailboxDomains: (organizationId: number) => scoped(organizationId, state.domains),
    // Connected domains are covered by mail-domains-ui.test.tsx; these tests keep them off.
    useMailDomains: () => ({ isPending: false, error: null, data: [], refetch: () => undefined }),
    useConnectMailDomain: () => ({ isPending: false, mutateAsync: async () => undefined }),
    useRefreshMailDomain: () => ({ isPending: false, mutateAsync: async () => undefined }),
    useRemoveMailDomain: () => ({ isPending: false, mutateAsync: async () => undefined }),
    usePreviewMailDomain: () => ({ isPending: false, mutateAsync: async () => undefined }),
    useListMailZones: () => ({ isPending: false, error: null, data: [], refetch: () => undefined }),
    useMailboxes: (organizationId: number) => scoped(organizationId, state.mailboxes),
    useMailLog: (organizationId: number, filters: Record<string, unknown>, page: number) => {
      state.logCalls.push({ filters, page });
      return scoped(organizationId, state.log);
    },
    useMailStats: (organizationId: number, days: number) => {
      state.statsCalls.push(days);
      return scoped(organizationId, state.stats);
    },
    useMailMessage: (organizationId: number, mailboxId: number, messageId: number | null) => {
      state.detailCalls.push({ mailboxId, messageId });
      return scoped(organizationId, state.detail);
    },
    useMarkMailMessageRead: (organizationId: number) => scoped(organizationId, state.markRead),
    useSendMailMessage: (organizationId: number, mailboxId: number) => {
      state.sendCalls.push(mailboxId);
      return scoped(organizationId, state.send);
    },
    useUpdateMailMessage: (organizationId: number) => scoped(organizationId, state.update),
    useDeleteMailMessage: (organizationId: number) => scoped(organizationId, state.removeMessage),
  };
});

beforeEach(() => {
  toast.success.mockReset();
  toast.error.mockReset();
  navigation.replace.mockClear();
  window.history.replaceState(null, '', '/console/emails');
  state.organizations.data = page([organization(1, 'Acme', 'owner')]);
  state.organizationIds = [];
  state.domains.data = [domain()];
  state.mailboxes.data = [mailbox(1, 'hello'), mailbox(2, 'sales')];
  state.log.data = page([
    entry({ id: 31, subject: 'Unread question', read: false }),
    entry({
      id: 30,
      mailbox_id: 2,
      mailbox_address: 'sales@mail.example.com',
      direction: 'outbound',
      status: 'failed',
      failure_code: 'provider.rejected',
      from: { address: 'sales@mail.example.com', name: '' },
      to: ['carol@example.com', 'dave@example.com'],
      subject: 'Bounced offer',
      read: true,
    }),
    entry({
      id: 29,
      subject: 'Old news',
      read: true,
      from: { address: 'eve@example.com', name: '' },
    }),
    entry({
      id: 28,
      direction: 'outbound',
      status: 'queued',
      from: { address: 'hello@mail.example.com', name: 'Hello' },
      to: ['frank@example.com'],
      subject: '',
      read: true,
    }),
  ]);
  state.stats.data = [
    { day: '2026-11-10', sent: 0, failed: 0, received: 0 },
    { day: '2026-11-11', sent: 4, failed: 0, received: 1 },
    { day: '2026-11-12', sent: 0, failed: 0, received: 0 },
    { day: '2026-11-13', sent: 1, failed: 2, received: 0 },
    { day: '2026-11-14', sent: 0, failed: 0, received: 5 },
    { day: '2026-11-15', sent: 0, failed: 0, received: 0 },
    { day: '2026-11-16', sent: 3, failed: 1, received: 2 },
  ];
  state.detail.data = message();
  for (const query of [
    state.organizations,
    state.domains,
    state.mailboxes,
    state.log,
    state.stats,
    state.detail,
  ]) {
    query.isPending = false;
    query.isSuccess = true;
    query.error = null;
    query.refetch.mockReset();
  }
  state.logCalls = [];
  state.statsCalls = [];
  state.detailCalls = [];
  state.sendCalls = [];
  for (const mutation of [state.markRead, state.send, state.update, state.removeMessage]) {
    mutation.isPending = false;
    mutation.mutate.mockReset();
    mutation.mutateAsync.mockReset();
  }
});

afterEach(() => {
  vi.useRealTimers();
});

describe('emails console', () => {
  it('totals the last seven days and labels every bar of the chart', () => {
    renderLog();

    expect(state.statsCalls.at(-1)).toBe(7);
    const stats = screen.getByRole('region', { name: 'Last 7 days' });
    const figure = (label: string) =>
      within(stats).getByText(label).closest('div')?.querySelector('dd')?.textContent;
    expect(figure('Sent')).toBe('8');
    expect(figure('Failed')).toBe('3');
    expect(figure('Received')).toBe('8');

    const chart = within(stats).getByRole('group', { name: 'Emails per day over the last 7 days' });
    const bars = within(chart).getAllByRole('img');
    expect(bars.map(bar => bar.getAttribute('aria-label'))).toEqual([
      'Nov 10: 0 sent, 0 failed, 0 received',
      'Nov 11: 4 sent, 0 failed, 1 received',
      'Nov 12: 0 sent, 0 failed, 0 received',
      'Nov 13: 1 sent, 2 failed, 0 received',
      'Nov 14: 0 sent, 0 failed, 5 received',
      'Nov 15: 0 sent, 0 failed, 0 received',
      'Nov 16: 3 sent, 1 failed, 2 received',
    ]);
    // The busiest count fills the chart; an empty count draws nothing.
    const heights = [...bars[4].querySelectorAll('span[style]')].map(
      bar => (bar as HTMLElement).style.height
    );
    expect(heights).toEqual(['0px', '0px', '100%']);
  });

  it('shows a retryable error when the statistics cannot be loaded', () => {
    state.stats.data = undefined;
    state.stats.error = new ApiError('boom', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE);
    renderLog();

    expect(screen.getByText('The mailbox service is temporarily unavailable.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(state.stats.refetch).toHaveBeenCalled();
    // The list below still works.
    expect(screen.getByText('Unread question')).toBeInTheDocument();
  });

  it('starts from the view in the URL and resets the page when a filter changes', () => {
    renderLog({ status: 'failed', page: 3 });

    expect(state.logCalls.at(-1)).toEqual({ filters: { status: 'failed' }, page: 3 });
    expect(screen.getByLabelText('Status')).toHaveValue('failed');
    expect(window.location.search).toBe('?org=1&status=failed&page=3');

    fireEvent.change(screen.getByLabelText('Direction'), { target: { value: 'outbound' } });
    expect(state.logCalls.at(-1)).toEqual({
      filters: { direction: 'outbound', status: 'failed' },
      page: 1,
    });
    expect(window.location.search).toBe('?org=1&direction=outbound&status=failed');

    fireEvent.change(screen.getByLabelText('Mailbox'), { target: { value: '2' } });
    expect(state.logCalls.at(-1)).toEqual({
      filters: { direction: 'outbound', status: 'failed', mailboxId: 2 },
      page: 1,
    });
    expect(window.location.search).toBe('?org=1&direction=outbound&status=failed&mailbox=2');

    fireEvent.change(screen.getByLabelText('Status'), { target: { value: '' } });
    fireEvent.change(screen.getByLabelText('Direction'), { target: { value: '' } });
    fireEvent.change(screen.getByLabelText('Mailbox'), { target: { value: '' } });
    expect(state.logCalls.at(-1)).toEqual({ filters: {}, page: 1 });
    expect(window.location.search).toBe('?org=1');
  });

  it('offers every direction, status, and mailbox as a filter', () => {
    renderLog();
    const options = (label: string) =>
      [...(screen.getByLabelText(label) as HTMLSelectElement).options].map(option => [
        option.value,
        option.text,
      ]);

    expect(options('Direction')).toEqual([
      ['', 'All directions'],
      ['inbound', 'Received'],
      ['outbound', 'Sent'],
    ]);
    expect(options('Status')).toEqual([
      ['', 'All statuses'],
      ['received', 'Received'],
      ['queued', 'Queued'],
      ['sent', 'Sent'],
      ['failed', 'Failed'],
    ]);
    expect(options('Mailbox')).toEqual([
      ['', 'All mailboxes'],
      ['1', 'hello@mail.example.com'],
      ['2', 'sales@mail.example.com'],
    ]);
  });

  it('searches after a pause, not on every keystroke, and resets the page', () => {
    vi.useFakeTimers();
    renderLog({ page: 2 });
    const search = screen.getByLabelText('Search emails');

    fireEvent.change(search, { target: { value: 'inv' } });
    act(() => void vi.advanceTimersByTime(200));
    fireEvent.change(search, { target: { value: ' invoice ' } });
    act(() => void vi.advanceTimersByTime(299));
    expect(state.logCalls.at(-1)).toEqual({ filters: {}, page: 2 });

    act(() => void vi.advanceTimersByTime(1));
    expect(state.logCalls.at(-1)).toEqual({ filters: { q: 'invoice' }, page: 1 });
    expect(window.location.search).toBe('?org=1&q=invoice');
    expect(state.logCalls.some(call => call.filters.q === 'inv')).toBe(false);

    fireEvent.change(search, { target: { value: '   ' } });
    act(() => void vi.advanceTimersByTime(300));
    expect(state.logCalls.at(-1)).toEqual({ filters: {}, page: 1 });
    expect(window.location.search).toBe('?org=1');
  });

  it('reads and writes the view as a query string', () => {
    expect(
      parseEmailLogView({
        direction: 'outbound',
        status: 'failed',
        mailbox: '12',
        q: '  100% off ',
        page: '4',
      })
    ).toEqual({ direction: 'outbound', status: 'failed', mailboxId: 12, q: '100% off', page: 4 });
    // Unknown or malformed values are ignored rather than shown as an error.
    expect(
      parseEmailLogView({
        direction: 'sideways',
        status: ['sent', 'failed'],
        mailbox: '0',
        q: 'a'.repeat(250),
        page: '-2',
        other: 'x',
      })
    ).toEqual({ status: 'sent', q: 'a'.repeat(200), page: 1 });
    expect(parseEmailLogView({})).toEqual({ page: 1 });

    const view: EmailLogView = { direction: 'inbound', mailboxId: 3, q: '100% off', page: 2 };
    expect(emailLogSearch(view)).toBe('?direction=inbound&mailbox=3&q=100%25+off&page=2');
    expect(
      parseEmailLogView(Object.fromEntries(new URLSearchParams(emailLogSearch(view))))
    ).toEqual(view);
    expect(emailLogSearch({ page: 1 })).toBe('');
    // The Emails page names its organization first, also on the unfiltered first page.
    expect(emailLogSearch(view, 7)).toBe('?org=7&direction=inbound&mailbox=3&q=100%25+off&page=2');
    expect(emailLogSearch({ page: 1 }, 7)).toBe('?org=7');
    expect(
      parseEmailLogView(Object.fromEntries(new URLSearchParams(emailLogSearch(view, 7))))
    ).toEqual(view);
  });

  it('keeps the organization in the URL while filters, search, and pages change', () => {
    vi.useFakeTimers();
    state.organizations.data = page([
      organization(1, 'Acme', 'owner'),
      organization(2, 'Globex', 'member'),
    ]);
    state.log.data = page([entry({ id: 31 })], { current_page: 1, last_page: 3, total: 41 });
    window.history.replaceState(null, '', '/console/emails?org=2&status=failed');
    renderLog({ status: 'failed', page: 1 });

    expect(screen.getByLabelText('Organization')).toHaveValue('2');
    expect(new Set(state.organizationIds)).toEqual(new Set([2]));
    expect(window.location.search).toBe('?org=2&status=failed');

    fireEvent.change(screen.getByLabelText('Direction'), { target: { value: 'inbound' } });
    expect(window.location.search).toBe('?org=2&direction=inbound&status=failed');
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(window.location.search).toBe('?org=2&direction=inbound&status=failed&page=2');
    fireEvent.change(screen.getByLabelText('Search emails'), { target: { value: 'invoice' } });
    act(() => void vi.advanceTimersByTime(300));
    expect(window.location.search).toBe('?org=2&direction=inbound&status=failed&q=invoice');
    fireEvent.change(screen.getByLabelText('Status'), { target: { value: '' } });
    fireEvent.change(screen.getByLabelText('Direction'), { target: { value: '' } });
    fireEvent.change(screen.getByLabelText('Search emails'), { target: { value: '' } });
    act(() => void vi.advanceTimersByTime(300));
    expect(window.location.search).toBe('?org=2');
    // The organization never came from a fallback, and every request stayed in it.
    expect(navigation.replace).not.toHaveBeenCalled();
    expect(new Set(state.organizationIds)).toEqual(new Set([2]));
  });

  it('starts another organization without the filters of the previous one', () => {
    state.organizations.data = page([
      organization(1, 'Acme', 'owner'),
      organization(2, 'Globex', 'member'),
    ]);
    window.history.replaceState(null, '', '/console/emails?org=1&status=failed&mailbox=2&page=3');
    renderLog({ status: 'failed', mailboxId: 2, page: 3 });
    expect(state.logCalls.at(-1)).toEqual({ filters: { status: 'failed', mailboxId: 2 }, page: 3 });

    state.organizationIds = [];
    fireEvent.change(screen.getByLabelText('Organization'), { target: { value: '2' } });

    expect(navigation.replace).toHaveBeenCalledWith('/console/emails?org=2', { scroll: false });
    expect(state.logCalls.at(-1)).toEqual({ filters: {}, page: 1 });
    expect(screen.getByLabelText('Status')).toHaveValue('');
    expect(window.location.search).toBe('?org=2');
    expect(new Set(state.organizationIds)).toEqual(new Set([2]));
  });

  it('names the only organization as text and writes the fallback into the URL', () => {
    renderLog();

    expect(screen.getByTestId('mail-organization-name')).toHaveTextContent('Acme');
    expect(screen.queryByLabelText('Organization')).toBeNull();
    expect(window.location.search).toBe('?org=1');
    expect(new Set(state.organizationIds)).toEqual(new Set([1]));
  });

  it('falls back to the first organization and keeps the filters of the URL', () => {
    state.organizations.data = page([
      organization(4, 'Acme', 'owner'),
      organization(5, 'Globex', 'member'),
    ]);
    window.history.replaceState(null, '', '/console/emails?org=9&status=failed&page=3');
    renderLog({ status: 'failed', page: 3 });

    expect(screen.getByLabelText('Organization')).toHaveValue('4');
    expect(window.location.search).toBe('?org=4&status=failed&page=3');
    expect(state.logCalls.at(-1)).toEqual({ filters: { status: 'failed' }, page: 3 });
    expect(new Set(state.organizationIds)).toEqual(new Set([4]));
  });

  it('explains that email needs an organization when the user has none', () => {
    state.organizations.data = page([]);
    renderLog({ status: 'failed', page: 2 });

    expect(screen.getByRole('heading', { name: 'Emails' })).toBeInTheDocument();
    expect(screen.getByText('No organization yet')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Go to organizations' })).toHaveAttribute(
      'href',
      '/console/organizations'
    );
    expect(screen.queryByRole('button', { name: 'Send email' })).toBeNull();
    expect(screen.queryByRole('table')).toBeNull();
    expect(state.organizationIds).toEqual([]);
  });

  it('shows loading and a retryable error for the organization list', () => {
    state.organizations.data = undefined;
    state.organizations.isPending = true;
    const view = renderLog();
    expect(screen.getByRole('heading', { name: 'Emails' })).toBeInTheDocument();
    expect(screen.queryByRole('table')).toBeNull();
    view.unmount();

    state.organizations.isPending = false;
    state.organizations.error = new ApiError('boom', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE);
    renderLog();
    expect(screen.getByText('Your organizations could not be loaded.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(state.organizations.refetch).toHaveBeenCalled();
    expect(state.organizationIds).toEqual([]);
  });

  it('leads back to the organizations when access to the organization was lost', () => {
    state.mailboxes.data = undefined;
    state.mailboxes.isSuccess = false;
    state.mailboxes.error = new ApiError('gone', ApiErrorCode.ORGANIZATION_NOT_FOUND, 404);
    renderLog();

    expect(screen.getByText('No access to this organization')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Go to organizations' })).toHaveAttribute(
      'href',
      '/console/organizations'
    );
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.getByRole('button', { name: 'Send email' })).toBeDisabled();
  });

  it('gives a member the whole page: reading, sending, marking, and deleting', () => {
    state.organizations.data = page([organization(1, 'Acme', 'member')]);
    renderLog();

    expect(screen.getByRole('button', { name: 'Send email' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));
    expect(state.markRead.mutate).toHaveBeenCalledWith({ mailboxId: 1, messageId: 31 });
    expect(screen.getByRole('button', { name: 'Reply' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Mark unread' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });

  it('shows the counterpart, the status, the subject, the mailbox, and the time of each row', () => {
    renderLog();
    const rows = within(screen.getByRole('table')).getAllByRole('row');
    expect(
      within(rows[0])
        .getAllByRole('columnheader')
        .map(cell => cell.textContent)
    ).toEqual(['To / From', 'Status', 'Subject', 'Mailbox', 'Sent / Received']);
    expect(rows).toHaveLength(5);

    // Inbound rows show the sender; an unread one is marked.
    const unread = rows[1];
    expect(unread).toHaveAttribute('data-unread', 'true');
    expect(within(unread).getByRole('button')).toHaveAccessibleName('Received from Bob Unread');
    expect(within(unread).getByText('Received')).toHaveAttribute('data-status', 'received');
    expect(within(unread).getByText('Unread question')).toBeInTheDocument();
    expect(within(unread).getByText('hello@mail.example.com')).toBeInTheDocument();

    // Outbound rows show the recipients; a failure shows its code.
    const failed = rows[2];
    expect(failed).not.toHaveAttribute('data-unread');
    expect(within(failed).getByRole('button')).toHaveAccessibleName(
      'Sent to carol@example.com, dave@example.com'
    );
    expect(within(failed).getByText('Failed')).toHaveAttribute('data-status', 'failed');
    expect(within(failed).getByText('provider.rejected')).toBeInTheDocument();
    expect(within(failed).getByText('sales@mail.example.com')).toBeInTheDocument();

    const read = rows[3];
    expect(read).not.toHaveAttribute('data-unread');
    expect(within(read).getByRole('button')).toHaveAccessibleName('Received from eve@example.com');
    expect(within(read).queryByText('Unread')).toBeNull();

    const queued = rows[4];
    expect(within(queued).getByText('Queued')).toHaveAttribute('data-status', 'queued');
    expect(within(queued).getByText('(no subject)')).toBeInTheDocument();

    // Relative time on screen, the absolute time on hover.
    const time = unread.querySelector('time');
    expect(time).toHaveAttribute('datetime', '2026-11-16T10:00:00Z');
    expect(time?.textContent).toMatch(/ago|now|yesterday/u);
    expect(time?.getAttribute('title')).toMatch(/2026/u);
    expect(screen.getByText('1–4 of 4')).toBeInTheDocument();
  });

  it('shows the delivery summary of a sent message in place of its status', () => {
    const outbound = { direction: 'outbound', status: 'sent', read: true };
    state.log.data = page([
      entry({ ...outbound, id: 45, to: ['a@example.com'], last_event: 'bounced' }),
      entry({ ...outbound, id: 44, to: ['b@example.com'], last_event: 'delivered' }),
      entry({ ...outbound, id: 43, to: ['c@example.com'], last_event: 'deferred' }),
      entry({ ...outbound, id: 42, to: ['d@example.com'], last_event: 'accepted' }),
      entry({ ...outbound, id: 41, to: ['e@example.com'], last_event: 'complained' }),
      // No event yet: the status stays.
      entry({ ...outbound, id: 40, to: ['f@example.com'], last_event: '' }),
      // The provider never took it: "Failed" and its code win over any event.
      entry({
        ...outbound,
        id: 39,
        to: ['g@example.com'],
        status: 'failed',
        failure_code: 'provider.rejected',
        last_event: 'rejected',
      }),
      entry({ id: 38 }),
    ]);
    renderLog();
    const rows = within(screen.getByRole('table')).getAllByRole('row').slice(1);
    const badge = (row: HTMLElement) => row.querySelector('[data-slot="badge"]');

    expect(rows.map(row => badge(row)?.textContent)).toEqual([
      'Bounced',
      'Delivered',
      'Delayed',
      'Accepted',
      'Complained',
      'Sent',
      'Failed',
      'Received',
    ]);
    expect(rows.map(row => badge(row)?.getAttribute('data-event'))).toEqual([
      'bounced',
      'delivered',
      'deferred',
      'accepted',
      'complained',
      null,
      null,
      null,
    ]);
    // Tones: destructive, success, warning, neutral.
    expect(badge(rows[0])).toHaveClass('bg-error-subtle', 'text-error');
    expect(badge(rows[1])).toHaveClass('bg-success-subtle', 'text-success');
    expect(badge(rows[2])).toHaveClass('bg-warning-subtle', 'text-warning');
    expect(badge(rows[3])).toHaveClass('bg-muted', 'text-muted-foreground');
    expect(badge(rows[4])).toHaveClass('bg-error-subtle', 'text-error');

    expect(badge(rows[6])).toHaveAttribute('data-status', 'failed');
    expect(within(rows[6]).getByText('provider.rejected')).toBeInTheDocument();
    expect(within(rows[6]).queryByText('Rejected')).toBeNull();
  });

  it('pages through the log and keeps the filters', () => {
    state.log.data = page([entry({ id: 31 })], { current_page: 2, last_page: 3, total: 41 });
    renderLog({ direction: 'inbound', page: 2 });

    expect(screen.getByText('Page 2 of 3')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(state.logCalls.at(-1)).toEqual({ filters: { direction: 'inbound' }, page: 3 });
    expect(window.location.search).toBe('?org=1&direction=inbound&page=3');
    fireEvent.click(screen.getByRole('button', { name: 'Previous' }));
    expect(state.logCalls.at(-1)).toEqual({ filters: { direction: 'inbound' }, page: 1 });
  });

  it('tells an empty log from a filter that matches nothing', () => {
    state.log.data = page([]);
    const view = renderLog();
    expect(screen.getByText('No emails yet')).toBeInTheDocument();
    expect(screen.getByText('Messages you send or receive appear here.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Clear filters' })).toBeNull();
    view.unmount();

    renderLog({ status: 'failed', q: 'nothing', page: 2 });
    expect(screen.getByText('No emails match these filters')).toBeInTheDocument();
    expect(screen.getByLabelText('Search emails')).toHaveValue('nothing');
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(state.logCalls.at(-1)).toEqual({ filters: {}, page: 1 });
    expect(screen.getByLabelText('Search emails')).toHaveValue('');
    expect(window.location.search).toBe('?org=1');
  });

  it('points to the mailboxes page when there is no mailbox to send from', () => {
    state.mailboxes.data = [];
    state.log.data = page([]);
    renderLog();

    expect(screen.getByRole('button', { name: 'Send email' })).toBeDisabled();
    // The link keeps the organization.
    expect(screen.getByRole('link', { name: 'Go to mailboxes' })).toHaveAttribute(
      'href',
      '/console/mailboxes?org=1'
    );
  });

  it('shows loading and a retryable error for the list', () => {
    state.log.data = undefined;
    state.log.isPending = true;
    const view = renderLog();
    expect(screen.queryByRole('table')).toBeNull();
    view.unmount();

    state.log.isPending = false;
    state.log.error = new ApiError('boom', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE);
    renderLog();
    expect(screen.getByText('The mailbox service is temporarily unavailable.')).toBeInTheDocument();
    expect(screen.queryByText('boom')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(state.log.refetch).toHaveBeenCalled();
  });

  it('opens a row through its own mailbox and marks only unread inbound mail read', () => {
    renderLog();

    fireEvent.click(screen.getByRole('button', { name: /eve@example\.com/u }));
    expect(state.detailCalls.at(-1)).toEqual({ mailboxId: 1, messageId: 29 });
    expect(state.markRead.mutate).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Back to list' }));

    fireEvent.click(screen.getByRole('button', { name: /carol@example\.com/u }));
    expect(state.detailCalls.at(-1)).toEqual({ mailboxId: 2, messageId: 30 });
    expect(state.markRead.mutate).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Back to list' }));

    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));
    expect(state.detailCalls.at(-1)).toEqual({ mailboxId: 1, messageId: 31 });
    expect(state.markRead.mutate).toHaveBeenCalledTimes(1);
    expect(state.markRead.mutate).toHaveBeenCalledWith({ mailboxId: 1, messageId: 31 });
    // The list and its filters give way to the message.
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.getByRole('heading', { name: 'Unread question' })).toBeInTheDocument();
  });

  it('shows the headers, the mailbox, and the status with its failure code', () => {
    state.detail.data = message({
      id: 30,
      mailbox_id: 2,
      direction: 'outbound',
      status: 'failed',
      failure_code: 'provider.rejected',
      from: { address: 'sales@mail.example.com', name: 'Sales' },
      to: ['carol@example.com', 'dave@example.com'],
      cc: ['erin@example.com'],
      subject: 'Bounced offer',
    });
    renderLog();
    fireEvent.click(screen.getByRole('button', { name: /carol@example\.com/u }));

    expect(screen.getByText('Sales <sales@mail.example.com>')).toBeInTheDocument();
    expect(screen.getByText('carol@example.com, dave@example.com')).toBeInTheDocument();
    expect(screen.getByText('erin@example.com')).toBeInTheDocument();
    expect(screen.getByText('sales@mail.example.com')).toBeInTheDocument();
    expect(screen.getByText('Failed')).toHaveAttribute('data-status', 'failed');
    expect(screen.getByText('provider.rejected')).toBeInTheDocument();
    // Only received mail can be answered or marked unread.
    expect(screen.queryByRole('button', { name: 'Reply' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Mark unread' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });

  it('lists the delivery events of a sent message, oldest first', () => {
    state.detail.data = message({
      id: 30,
      mailbox_id: 2,
      direction: 'outbound',
      status: 'sent',
      last_event: 'bounced',
      to: ['carol@example.com', 'dave@example.com'],
      events: [
        { type: 'accepted', recipient: '', detail: '', occurred_at: '2026-11-16T10:00:00Z' },
        {
          type: 'delivered',
          recipient: 'carol@example.com',
          detail: '',
          occurred_at: '2026-11-16T10:00:02Z',
        },
        {
          type: 'deferred',
          recipient: 'dave@example.com',
          detail: '4.2.2 mailbox_full',
          occurred_at: '2026-11-16T10:00:03Z',
        },
        {
          type: 'bounced',
          recipient: 'dave@example.com',
          detail: '5.1.1 permanent_failure',
          occurred_at: '2026-11-16T10:05:00Z',
        },
      ],
    });
    renderLog();
    fireEvent.click(screen.getByRole('button', { name: /carol@example\.com/u }));

    // The header shows the summary, not "Sent".
    const section = screen.getByRole('region', { name: 'Delivery' });
    const headerBadge = screen.getAllByText('Bounced').find(element => !section.contains(element));
    expect(headerBadge).toHaveAttribute('data-event', 'bounced');
    expect(headerBadge).toHaveAttribute('data-status', 'sent');
    expect(screen.queryByText('Sent')).toBeNull();

    const rows = within(section).getAllByRole('listitem');
    expect(
      rows.map(row => [
        row.querySelector('[data-slot="badge"]')?.textContent,
        row.querySelector('span:not([data-slot])')?.textContent ?? null,
        row.querySelector('code')?.textContent ?? null,
      ])
    ).toEqual([
      ['Accepted', null, null],
      ['Delivered', 'carol@example.com', null],
      ['Delayed', 'dave@example.com', '4.2.2 mailbox_full'],
      ['Bounced', 'dave@example.com', '5.1.1 permanent_failure'],
    ]);
    expect(rows[3].querySelector('[data-slot="badge"]')).toHaveClass('text-error');
    expect(rows[3].querySelector('code')).toHaveClass('font-mono', 'text-muted-foreground');
    // Relative time on screen, the absolute time on hover.
    const time = rows[3].querySelector('time');
    expect(time).toHaveAttribute('datetime', '2026-11-16T10:05:00Z');
    expect(time?.getAttribute('title')).toMatch(/2026/u);
    expect(time?.textContent).not.toBe('');
    // The timeline sits between the header fields and the content tabs.
    const tabs = screen.getByRole('tablist');
    expect(section.compareDocumentPosition(tabs) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(
      screen.getByText('Status').compareDocumentPosition(section) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  it('says when a sent message has no delivery events and omits the section for inbound mail', () => {
    state.detail.data = message({ id: 30, mailbox_id: 2, direction: 'outbound', status: 'sent' });
    const view = renderLog();
    fireEvent.click(screen.getByRole('button', { name: /carol@example\.com/u }));

    const section = screen.getByRole('region', { name: 'Delivery' });
    expect(
      within(section).getByText('No delivery events have been reported yet.')
    ).toBeInTheDocument();
    expect(within(section).queryByRole('list')).toBeNull();
    expect(screen.getByText('Sent')).toHaveAttribute('data-status', 'sent');
    view.unmount();

    state.detail.data = message();
    renderLog();
    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));
    expect(screen.getByRole('heading', { name: 'Unread question' })).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Delivery' })).toBeNull();
    expect(screen.queryByText('Delivery')).toBeNull();
    expect(screen.queryByText('No delivery events have been reported yet.')).toBeNull();
  });

  it('previews HTML only in a fully sandboxed frame and shows the source as text', () => {
    const html = '<p id="from-sender">Hi &amp; bye</p><script>window.pwned = true</script>';
    state.detail.data = message({ html_body: html });
    const { container } = renderLog();
    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));

    const tabs = screen.getAllByRole('tab');
    expect(tabs.map(tab => tab.textContent)).toEqual(['Preview', 'Plain text', 'HTML']);
    expect(screen.getByRole('tab', { name: 'Preview' })).toHaveAttribute('aria-selected', 'true');

    const frame = screen.getByTitle('Message content');
    expect(frame.tagName).toBe('IFRAME');
    expect(frame.hasAttribute('sandbox')).toBe(true);
    expect(frame.getAttribute('sandbox')).toBe('');
    expect(frame.getAttribute('srcdoc')).toContain(html);
    expect(frame.getAttribute('srcdoc')).toContain("default-src 'none'");
    expect(frame.hasAttribute('src')).toBe(false);
    expect(frame.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(container.querySelectorAll('iframe')).toHaveLength(1);

    selectTab('Plain text');
    expect(screen.getByRole('tab', { name: 'Plain text' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByText('Plain question')).toBeInTheDocument();
    expect(container.querySelector('iframe')).toBeNull();

    selectTab('HTML');
    const source = screen.getByLabelText('HTML source');
    expect(source.tagName).toBe('PRE');
    // The source is one text node: the markup is shown, not parsed.
    expect(source.textContent).toBe(html);
    expect(source.children).toHaveLength(0);
    expect(source.innerHTML).toContain('&lt;p id="from-sender"&gt;Hi &amp;amp; bye&lt;/p&gt;');
    expect(source.isContentEditable).toBe(false);
    expect(container.querySelector('iframe')).toBeNull();

    // Nothing from the sender ever becomes part of the application document.
    expect(document.querySelector('#from-sender')).toBeNull();
    expect(document.querySelector('script')).toBeNull();
  });

  it('opens on plain text and disables the HTML tabs when there is no HTML', () => {
    state.detail.data = message({ html_body: '' });
    const { container } = renderLog();
    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));

    expect(screen.getByRole('tab', { name: 'Plain text' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByRole('tab', { name: 'Preview' })).toBeDisabled();
    expect(screen.getByRole('tab', { name: 'HTML' })).toBeDisabled();
    expect(screen.getByRole('tab', { name: 'Plain text' })).toBeEnabled();
    expect(screen.getByText('Plain question')).toBeInTheDocument();
    expect(container.querySelector('iframe')).toBeNull();

    selectTab('Preview');
    expect(screen.getByRole('tab', { name: 'Plain text' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  it('disables the plain text tab when there is only HTML, and every tab when empty', () => {
    state.detail.data = message({ text_body: '' });
    const view = renderLog();
    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));
    expect(screen.getByRole('tab', { name: 'Preview' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: 'Plain text' })).toBeDisabled();
    expect(screen.getByRole('tab', { name: 'HTML' })).toBeEnabled();
    view.unmount();

    state.detail.data = message({ text_body: '', html_body: '' });
    renderLog();
    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));
    for (const tab of screen.getAllByRole('tab')) expect(tab).toBeDisabled();
    expect(screen.getByText('This message has no content.')).toBeInTheDocument();
  });

  it('marks unread and deletes from the detail, then returns to the list', async () => {
    state.update.mutateAsync.mockResolvedValue(message());
    state.removeMessage.mutateAsync.mockResolvedValue(undefined);
    renderLog();
    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));

    fireEvent.click(screen.getByRole('button', { name: 'Mark unread' }));
    await waitFor(() =>
      expect(state.update.mutateAsync).toHaveBeenCalledWith({ messageId: 31, read: false })
    );
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));
    // Deleting moves the message to the trash at once, with an undo instead of a confirmation.
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() =>
      expect(state.update.mutateAsync).toHaveBeenCalledWith({ messageId: 31, placement: 'trash' })
    );
    expect(state.removeMessage.mutateAsync).not.toHaveBeenCalled();
    expect(toast.success).toHaveBeenCalledWith(
      'Moved to the trash',
      expect.objectContaining({ action: expect.objectContaining({ label: 'Undo' }) })
    );
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument());
  });

  it('offers no reply or delete on a message of a view-only mailbox', () => {
    state.mailboxes.data = [
      {
        ...mailbox(1, 'hello'),
        access: {
          view: true,
          send: false,
          manage_members: false,
          administer: false,
          see_history: false,
        },
      },
      mailbox(2, 'sales'),
    ];
    renderLog();
    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));

    expect(screen.getByRole('button', { name: 'Mark unread' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reply' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
  });

  it('offers no reply or delete on a message of a mailbox the user no longer has', () => {
    state.mailboxes.data = [mailbox(2, 'sales')];
    renderLog();
    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));

    expect(screen.queryByRole('button', { name: 'Reply' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
  });

  it('replies from the mailbox that received the message, without a From picker', async () => {
    state.send.mutateAsync.mockResolvedValue(message({ direction: 'outbound', status: 'sent' }));
    state.log.data = page([
      entry({ id: 31, mailbox_id: 2, mailbox_address: 'sales@mail.example.com', read: true }),
    ]);
    state.detail.data = message({ mailbox_id: 2 });
    renderLog();
    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));
    fireEvent.click(screen.getByRole('button', { name: 'Reply' }));
    const dialog = await screen.findByRole('dialog');

    expect(within(dialog).queryByLabelText('From')).toBeNull();
    expect(dialog).toHaveTextContent('Sent from sales@mail.example.com.');
    expect(within(dialog).getByLabelText('To')).toHaveValue('bob@example.com');
    expect(within(dialog).getByLabelText('Subject')).toHaveValue('Re: Unread question');
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Answer' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        to: ['bob@example.com'],
        subject: 'Re: Unread question',
        text: 'Answer',
        in_reply_to: 'question-1@example.com',
      })
    );
    expect(state.sendCalls.at(-1)).toBe(2);
  });

  it('quotes the original plain-text body when replying and hides Reply all for one recipient', async () => {
    state.send.mutateAsync.mockResolvedValue(message({ direction: 'outbound', status: 'sent' }));
    state.detail.data = message({
      text_body: 'First line\n\nSecond line',
    });
    renderLog();
    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));

    expect(screen.queryByRole('button', { name: 'Reply all' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Forward' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Reply' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByRole('heading', { name: 'Reply' })).toBeInTheDocument();

    const bodyField = within(dialog).getByLabelText('Message') as HTMLTextAreaElement;
    expect(bodyField.value).toMatch(
      /^\n\nOn .*, Bob <bob@example\.com> wrote:\n> First line\n>\n> Second line$/u
    );

    fireEvent.change(bodyField, { target: { value: `Sure thing!${bodyField.value}` } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        to: ['bob@example.com'],
        subject: 'Re: Unread question',
        text: expect.stringMatching(
          /^Sure thing!\n\nOn .*, Bob <bob@example\.com> wrote:\n> First line\n>\n> Second line$/u
        ),
        in_reply_to: 'question-1@example.com',
      })
    );
  });

  it('replies to all unique recipients while excluding the current mailbox case-insensitively', async () => {
    state.send.mutateAsync.mockResolvedValue(message({ direction: 'outbound', status: 'sent' }));
    state.log.data = page([
      entry({
        id: 31,
        mailbox_id: 1,
        mailbox_address: 'hello@mail.example.com',
        to: ['HELLO@mail.example.com', 'Carol@example.com'],
        cc: ['dave@example.com', 'carol@example.com', 'bob@example.com'],
        read: true,
      }),
    ]);
    state.detail.data = message({
      from: { address: 'Bob@example.com', name: 'Bob' },
      to: ['HELLO@mail.example.com', 'Carol@example.com'],
      cc: ['dave@example.com', 'carol@example.com', 'bob@example.com'],
      subject: 'Re: Team sync',
      text_body: 'Are we meeting today?',
    });
    renderLog();
    fireEvent.click(screen.getByRole('button', { name: /Bob/u }));

    fireEvent.click(screen.getByRole('button', { name: 'Reply all' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByRole('heading', { name: 'Reply' })).toBeInTheDocument();
    expect(within(dialog).getByLabelText('To')).toHaveValue('bob@example.com');
    expect(within(dialog).getByLabelText('Cc (optional)')).toHaveValue(
      'carol@example.com, dave@example.com'
    );
    expect(within(dialog).getByLabelText('Subject')).toHaveValue('Re: Team sync');

    const bodyField = within(dialog).getByLabelText('Message') as HTMLTextAreaElement;
    expect(bodyField.value).toContain('> Are we meeting today?');

    fireEvent.change(bodyField, { target: { value: `Yes, at 3pm.${bodyField.value}` } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        to: ['bob@example.com'],
        cc: ['carol@example.com', 'dave@example.com'],
        subject: 'Re: Team sync',
        text: expect.stringContaining('Yes, at 3pm.'),
        in_reply_to: 'question-1@example.com',
      })
    );
  });

  it('forwards inbound and outbound messages with a Fwd: subject and forwarded header block', async () => {
    state.send.mutateAsync.mockResolvedValue(message({ direction: 'outbound', status: 'sent' }));
    state.detail.data = message({
      id: 30,
      mailbox_id: 2,
      direction: 'outbound',
      status: 'sent',
      from: { address: 'sales@mail.example.com', name: 'Sales' },
      to: ['carol@example.com', 'dave@example.com'],
      cc: ['erin@example.com'],
      subject: 'Bounced offer',
      text_body: 'Special pricing inside.',
    });
    renderLog();
    fireEvent.click(screen.getByRole('button', { name: /carol@example\.com/u }));

    expect(screen.queryByRole('button', { name: 'Reply' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Reply all' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Forward' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByRole('heading', { name: 'Forward message' })).toBeInTheDocument();
    expect(within(dialog).getByLabelText('To')).toHaveValue('');
    expect(within(dialog).getByLabelText('Cc (optional)')).toHaveValue('');
    expect(within(dialog).getByLabelText('Subject')).toHaveValue('Fwd: Bounced offer');

    const bodyField = within(dialog).getByLabelText('Message') as HTMLTextAreaElement;
    expect(bodyField.value).toContain('---------- Forwarded message ----------');
    expect(bodyField.value).toContain('From: Sales <sales@mail.example.com>');
    expect(bodyField.value).toContain('Subject: Bounced offer');
    expect(bodyField.value).toContain('To: carol@example.com, dave@example.com');
    expect(bodyField.value).toContain('Cc: erin@example.com');
    expect(bodyField.value).toContain('\n\nSpecial pricing inside.');

    fireEvent.change(within(dialog).getByLabelText('To'), {
      target: { value: 'frank@example.com' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        to: ['frank@example.com'],
        subject: 'Fwd: Bounced offer',
        text: expect.stringContaining('---------- Forwarded message ----------'),
      })
    );
    expect(state.sendCalls.at(-1)).toBe(2);
  });

  it('builds quoted reply, reply-all, and forward drafts without duplicating prefixes', () => {
    const sample = message({
      from: { address: 'bob@example.com', name: '' },
      to: ['Hello@Mail.Example.com', 'carol@example.com'],
      cc: ['CAROL@example.com'],
      subject: 'Fwd: Quarterly report',
      text_body: 'Line 1\r\nLine 2',
    });
    const replyCopy = {
      prefixSubject: (subject: string) => `Re: ${subject}`,
      quotedHeader: ({ date, sender }: { date: string; sender: string }) =>
        `On ${date}, ${sender} wrote:`,
      formatDate: () => 'Nov 16, 2026',
    };

    const quoted = quotedReplyDraft(sample, replyCopy);
    expect(quoted).toEqual({
      to: 'bob@example.com',
      cc: '',
      subject: 'Re: Fwd: Quarterly report',
      body: '\n\nOn Nov 16, 2026, bob@example.com wrote:\n> Line 1\n> Line 2',
      inReplyTo: 'question-1@example.com',
      reply: true,
      mode: 'reply',
    });

    const all = replyAllDraft(sample, 'hello@mail.example.com', replyCopy);
    expect(all.to).toBe('bob@example.com');
    expect(all.cc).toBe('carol@example.com');

    const forwarded = forwardDraft(sample, {
      prefixSubject: subject => `Fwd: ${subject}`,
      forwardedHeader: '---------- Forwarded message ----------',
      formatDate: () => 'Nov 16, 2026',
      labels: { from: 'From', date: 'Date', subject: 'Subject', to: 'To', cc: 'Cc' },
    });
    expect(forwarded.subject).toBe('Fwd: Quarterly report');
    expect(forwarded.inReplyTo).toBe('');
    expect(forwarded.reply).toBe(false);
    expect(forwarded.mode).toBe('forward');
    expect(forwarded.body).toBe(
      [
        '',
        '',
        '---------- Forwarded message ----------',
        'From: bob@example.com',
        'Date: Nov 16, 2026',
        'Subject: Fwd: Quarterly report',
        'To: Hello@Mail.Example.com, carol@example.com',
        'Cc: CAROL@example.com',
        '',
        'Line 1',
        'Line 2',
      ].join('\n')
    );
  });

  it('opens the full-page composer with a From picker over every mailbox', async () => {
    state.send.mutateAsync.mockResolvedValue(message({ direction: 'outbound', status: 'sent' }));
    renderLog({ page: 2 });

    fireEvent.click(screen.getByRole('button', { name: 'Send email' }));
    const dialog = await screen.findByRole('region', { name: 'New message' });
    expect(within(dialog).getByRole('heading', { name: 'New message' })).toBeInTheDocument();
    const from = within(dialog).getByLabelText('From') as HTMLSelectElement;
    expect(from.tagName).toBe('SELECT');
    expect([...from.options].map(option => option.text)).toEqual([
      'hello@mail.example.com',
      'sales@mail.example.com',
    ]);
    expect(from).toHaveValue('1');
    expect(state.sendCalls.at(-1)).toBe(1);

    fireEvent.change(from, { target: { value: '2' } });
    expect(state.sendCalls.at(-1)).toBe(2);
    fireEvent.change(within(dialog).getByLabelText('To'), { target: { value: 'bob@example.com' } });
    fireEvent.change(within(dialog).getByLabelText('Subject'), { target: { value: 'Hello' } });
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Body' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        to: ['bob@example.com'],
        subject: 'Hello',
        text: 'Body',
      })
    );
    expect(toast.success).toHaveBeenCalledWith('Message sent');
    await waitFor(() => expect(screen.queryByRole('region', { name: 'New message' })).toBeNull());
    // The new message is at the top of the first page.
    expect(state.logCalls.at(-1)).toEqual({ filters: {}, page: 1 });
  });

  it('preselects the filtered mailbox as the sender and keeps a failed send visible', async () => {
    state.send.mutateAsync.mockRejectedValue(
      new ApiError('provider said no', ApiErrorCode.MAILBOX_DELIVERY_FAILED, 502)
    );
    renderLog({ mailboxId: 2, page: 1 });

    fireEvent.click(screen.getByRole('button', { name: 'Send email' }));
    const dialog = await screen.findByRole('region', { name: 'New message' });
    expect(within(dialog).getByLabelText('From')).toHaveValue('2');
    fireEvent.change(within(dialog).getByLabelText('To'), { target: { value: 'bob@example.com' } });
    fireEvent.change(within(dialog).getByLabelText('Subject'), { target: { value: 'Hello' } });
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Body' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    const alert = await within(dialog).findByRole('alert');
    expect(alert).toHaveTextContent(/NOT delivered/u);
    expect(screen.queryByText(/provider said no/u)).toBeNull();
    expect(toast.success).not.toHaveBeenCalled();
    expect(screen.getByRole('region', { name: 'New message' })).toBeInTheDocument();
  });
});

describe('mail domain console', () => {
  it('says a Cloudflare domain delivers and receives', () => {
    state.domains.data = [domain({ provider: 'cloudflare', inbound_enabled: true })];
    renderDomain();

    expect(screen.getByRole('heading', { name: 'mail.example.com' })).toBeInTheDocument();
    const provider = screen.getByTestId('mail-domain-provider');
    expect(provider).toHaveTextContent('Cloudflare — delivering');
    expect(provider).toHaveAttribute('data-state', 'on');
    const inbound = screen.getByTestId('mail-domain-inbound');
    expect(inbound).toHaveTextContent('Enabled');
    expect(inbound).toHaveAttribute('data-state', 'on');
    expect(screen.getByRole('heading', { name: 'Domain' })).toBeInTheDocument();
    expect(screen.getByTestId('mail-organization-name')).toHaveTextContent('Acme');
    // The limits are those of the organization.
    expect(screen.getByText('50 per organization')).toBeInTheDocument();
    expect(screen.getByText('200 messages per organization in any 24 hours')).toBeInTheDocument();
    expect(new Set(state.organizationIds)).toEqual(new Set([1]));
    expect(stepStates()).toEqual({
      routing: 'manual',
      rule: 'manual',
      sending: 'done',
      inbound: 'done',
      config: 'done',
    });
  });

  it('warns that the log provider does not deliver and that inbound mail is off', () => {
    state.domains.data = [domain({ provider: 'log', inbound_enabled: false })];
    renderDomain();

    const provider = screen.getByTestId('mail-domain-provider');
    expect(provider).toHaveTextContent('Log — not delivering; development only');
    expect(provider).toHaveAttribute('data-state', 'off');
    expect(screen.getByText(/Nothing reaches a recipient/u)).toBeInTheDocument();
    const inbound = screen.getByTestId('mail-domain-inbound');
    expect(inbound).toHaveTextContent('Disabled');
    expect(inbound).toHaveAttribute('data-state', 'off');
    expect(stepStates()).toEqual({
      routing: 'manual',
      rule: 'manual',
      sending: 'pending',
      inbound: 'pending',
      config: 'manual',
    });
  });

  it('derives sending and receiving independently', () => {
    state.domains.data = [domain({ provider: 'cloudflare', inbound_enabled: false })];
    const view = renderDomain();
    expect(stepStates()).toMatchObject({ sending: 'done', inbound: 'pending', config: 'manual' });
    expect(screen.getByTestId('mail-domain-inbound')).toHaveTextContent('Disabled');
    view.unmount();

    state.domains.data = [domain({ provider: 'log', inbound_enabled: true })];
    renderDomain();
    expect(stepStates()).toMatchObject({ sending: 'pending', inbound: 'done', config: 'manual' });
    expect(screen.getByTestId('mail-domain-inbound')).toHaveTextContent('Enabled');
  });

  it('names the checklist steps and the API settings with the real domain', () => {
    renderDomain();

    expect(screen.getByText('Enable Email Routing for mail.example.com')).toBeInTheDocument();
    expect(screen.getByText('Enable Email Sending for mail.example.com')).toBeInTheDocument();
    expect(
      screen.getByText(/catch-all address that sends mail for mail\.example\.com/u)
    ).toBeInTheDocument();
    for (const setting of [
      'MAILBOX_PROVIDER=cloudflare',
      'MAILBOX_CLOUDFLARE_ACCOUNT_ID',
      'MAILBOX_CLOUDFLARE_API_TOKEN',
      'MAILBOX_INBOUND_SECRET',
    ]) {
      expect(screen.getByText(setting)).toBeInTheDocument();
    }
  });

  it('shows an API key example from an owned address and links to the API keys', () => {
    const view = renderDomain();
    const example = screen.getByLabelText('Example request');
    expect(example).toHaveTextContent('POST https://YOUR_API_HOST/v1/outbound-mail-messages');
    expect(example).toHaveTextContent('X-API-Key:');
    expect(example).toHaveTextContent('Organization-Id: 1');
    expect(example).toHaveTextContent('Idempotency-Key:');
    expect(example).toHaveTextContent('"from": "hello@mail.example.com"');
    expect(screen.getByRole('link', { name: 'Manage API keys' })).toHaveAttribute(
      'href',
      '/console/settings'
    );
    view.unmount();

    // Without a mailbox the example still uses the configured domain.
    state.mailboxes.data = [];
    state.domains.data = [domain({ domain: 'post.example.org' })];
    renderDomain();
    expect(screen.getByLabelText('Example request')).toHaveTextContent(
      '"from": "hello@post.example.org"'
    );
  });

  it('shows loading, a retryable error, and a server without a domain', () => {
    state.domains.data = undefined;
    state.domains.error = new ApiError('boom', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE);
    const view = renderDomain();
    expect(screen.getByText('The mailbox service is temporarily unavailable.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(state.domains.refetch).toHaveBeenCalled();
    view.unmount();

    state.domains.error = null;
    state.domains.data = [];
    renderDomain();
    expect(screen.getByText('No mail domain')).toBeInTheDocument();
    expect(screen.queryByTestId('mail-domain-provider')).toBeNull();
  });

  it('shows the domain of the chosen organization and the states without one', () => {
    state.organizations.data = page([
      organization(1, 'Acme', 'owner'),
      organization(2, 'Globex', 'member'),
    ]);
    window.history.replaceState(null, '', '/console/admin/domains?org=2');
    const view = renderDomain();
    expect(screen.getByLabelText('Organization')).toHaveValue('2');
    expect(new Set(state.organizationIds)).toEqual(new Set([2]));
    expect(screen.getByLabelText('Example request')).toHaveTextContent('Organization-Id: 2');
    view.unmount();

    state.domains.data = undefined;
    state.domains.error = new ApiError('gone', ApiErrorCode.ORGANIZATION_NOT_FOUND, 404);
    const lost = renderDomain();
    expect(screen.getByText('No access to this organization')).toBeInTheDocument();
    lost.unmount();

    state.organizations.data = page([]);
    state.organizationIds = [];
    renderDomain();
    expect(screen.getByText('No organization yet')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Go to organizations' })).toHaveAttribute(
      'href',
      '/console/organizations'
    );
    expect(state.organizationIds).toEqual([]);
  });
});

/** Radix tabs activate on mouse down, as a real pointer does. */
function selectTab(name: string) {
  fireEvent.mouseDown(screen.getByRole('tab', { name }), { button: 0 });
}

function stepStates(): Record<string, string | null> {
  return Object.fromEntries(
    [...document.querySelectorAll('[data-step]')].map(step => [
      step.getAttribute('data-step'),
      step.getAttribute('data-state'),
    ])
  );
}

function domain(overrides: Record<string, unknown> = {}) {
  return {
    domain: 'mail.example.com',
    kind: 'shared',
    max_mailboxes: 50,
    provider: 'cloudflare',
    inbound_enabled: true,
    daily_send_limit: 200,
    daily_broadcast_limit: 200,
    daily_transactional_limit: 200,
    transactional_enabled: true,
    transactional_recipient_limit_per_minute: 3,
    transactional_key_limit_per_second: 20,
    simulator_domain: 'test.zmail.im',
    broadcasts_enabled: true,
    custom_domains_enabled: false,
    ...overrides,
  };
}

function organization(id: number, name: string, role: 'owner' | 'admin' | 'member') {
  return { id, name, slug: `org-${id}`, role, created_at: now(), updated_at: now() };
}

function mailbox(id: number, localPart: string) {
  return {
    id,
    address: `${localPart}@mail.example.com`,
    display_name: '',
    type: 'shared',
    owner_user_id: null,
    awaiting_handover: false,
    sealed_before: null,
    webhooks_enabled: false,
    access: { view: true, send: true, manage_members: true, administer: true, see_history: true },
    unread_count: 0,
    created_at: now(),
  };
}

function entry(overrides: Record<string, unknown> = {}) {
  return {
    id: 31,
    mailbox_id: 1,
    mailbox_address: 'hello@mail.example.com',
    direction: 'inbound',
    status: 'received',
    from: { address: 'bob@example.com', name: 'Bob' },
    to: ['hello@mail.example.com'],
    cc: [],
    subject: 'Unread question',
    read: false,
    failure_code: '',
    queued_reason: null,
    last_event: '',
    attachment_count: 0,
    template_id: null,
    created_at: now(),
    ...overrides,
  };
}

function message(overrides: Partial<MailMessage> = {}): MailMessage {
  const { mailbox_address: _unused, ...summary } = entry();
  void _unused;
  return {
    ...summary,
    direction: 'inbound',
    status: 'received',
    last_event: '',
    // A message carries its placement, labels, star, and thread; the log entry it opens from does not.
    placement: 'default',
    folder_id: null,
    label_ids: [],
    starred: false,
    thread_id: 31,
    trashed_at: null,
    text_body: 'Plain question',
    html_body: '<p>Formatted question</p>',
    message_id: 'question-1@example.com',
    in_reply_to: '',
    events: [],
    attachments: [],
    ...overrides,
  };
}

function page(items: unknown[], meta: Record<string, number> = {}) {
  return {
    items,
    meta: {
      current_page: 1,
      per_page: 20,
      total: items.length,
      last_page: 1,
      from: items.length ? 1 : 0,
      to: items.length,
      ...meta,
    },
    links: { first: '', last: '', prev: null, next: null },
  };
}

function now() {
  return '2026-11-16T10:00:00Z';
}

function wrap(children: React.ReactNode) {
  return (
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      {children}
    </NextIntlClientProvider>
  );
}

function renderLog(view: EmailLogView = { page: 1 }) {
  return render(wrap(<EmailLog initialView={view} />));
}

function renderDomain() {
  return render(wrap(<MailDomainStatus />));
}
