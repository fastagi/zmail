import { useState } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ConnectedMailboxesDialog } from '@/features/mailbox/components/connected-mailboxes-dialog';
import { ExternalConnectDialog } from '@/features/mailbox/components/external-connect-dialog';
import type {
  ExternalMailConnection,
  ExternalMailConnector,
  ExternalMailSyncRun,
} from '@/features/mailbox/external-mail-schemas';
import { mailboxKeys } from '@/features/mailbox/hooks/mailbox-keys';
import { ApiErrorCode, ClientErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';
import enMailbox from '@/i18n/modules/mailbox/en-US';
import zhMailbox from '@/i18n/modules/mailbox/zh-Hans';
import enNav from '@/i18n/modules/nav/en-US';
import zhNav from '@/i18n/modules/nav/zh-Hans';

const service = vi.hoisted(() => ({
  connectors: vi.fn(),
  connections: vi.fn(),
  start: vi.fn(),
  complete: vi.fn(),
  disconnect: vi.fn(),
  bindCredentials: vi.fn(),
  syncRuns: vi.fn(),
  purge: vi.fn(),
}));
const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
const router = vi.hoisted(() => ({ replace: vi.fn(), push: vi.fn() }));

vi.mock('sonner', () => ({ toast }));
vi.mock('next/navigation', () => ({
  useRouter: () => router,
  usePathname: () => window.location.pathname,
  useSearchParams: () => new URLSearchParams(window.location.search),
}));
vi.mock('@/features/mailbox/services/external-mail-service', async importOriginal => ({
  ...(await importOriginal<typeof import('@/features/mailbox/services/external-mail-service')>()),
  externalMailService: service,
}));

const SECRET = 'xq7-app-PASS-9z';

const gmail: ExternalMailConnector = { id: 'gmail', auth_kind: 'oauth2', capabilities: { read: true } };
const imap: ExternalMailConnector = { id: 'imap', auth_kind: 'password', capabilities: { read: true } };

let queryClient: QueryClient;

beforeEach(() => {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  for (const fn of Object.values(service)) fn.mockReset();
  toast.success.mockReset();
  toast.error.mockReset();
  router.push.mockReset();
  window.sessionStorage.clear();
  window.localStorage.clear();
});

describe('app password connect step', () => {
  it('offers the password connector with the same statement and what an app password is', async () => {
    renderWith(<ConnectDialog />);
    const chooser = screen.getByRole('dialog', { name: 'Add an external mailbox' });
    const button = within(chooser).getByRole('button', { name: 'IMAP' });
    expect(button).toBeEnabled();
    expect(button).not.toHaveTextContent('Coming soon');

    fireEvent.click(button);
    const consent = screen.getByRole('dialog', { name: 'Before you connect IMAP' });
    expect(consent).toHaveTextContent('Read-only.');
    expect(consent).toHaveTextContent('Only you can see this mail.');
    expect(consent).toHaveTextContent('The connection is removed when you leave the organization.');
    expect(consent).toHaveTextContent('You need an app password, not your account password');
    expect(consent).toHaveTextContent('Where to find an app password');
    // The OAuth backfill choice moves to the form.
    expect(within(consent).queryByRole('radio')).not.toBeInTheDocument();
    expect(service.start).not.toHaveBeenCalled();

    fireEvent.click(within(consent).getByRole('button', { name: 'Continue' }));
    const form = await screen.findByRole('dialog', { name: 'Connect with an app password' });
    const port = await within(form).findByLabelText('Port');
    expect(port).toHaveValue('993');
    expect(port).toHaveAttribute('readonly');
    expect(port).toHaveAccessibleDescription('Only port 993 (IMAP over TLS) is accepted.');
    expect(within(form).getByLabelText('App password')).toHaveAttribute('type', 'password');
    expect(within(form).getByRole('radio', { name: '14 days' })).toBeChecked();

    fireEvent.click(within(form).getByRole('button', { name: 'Back' }));
    expect(screen.getByRole('dialog', { name: 'Before you connect IMAP' })).toBeInTheDocument();
  });

  it('validates the form before sending anything', async () => {
    renderWith(<ConnectDialog />);
    const form = await openForm();
    fireEvent.click(within(form).getByRole('button', { name: 'Connect' }));
    expect(within(form).getByText('Enter the IMAP server.')).toBeInTheDocument();
    expect(within(form).getByText('Enter the username.')).toBeInTheDocument();
    expect(within(form).getByText('Enter the app password.')).toBeInTheDocument();
    expect(within(form).getByLabelText('IMAP server')).toHaveFocus();
    expect(within(form).getByLabelText('IMAP server')).toHaveAttribute('aria-invalid', 'true');

    fireEvent.change(within(form).getByLabelText('IMAP server'), {
      target: { value: 'https://imap.example.com:993' },
    });
    fireEvent.click(within(form).getByRole('button', { name: 'Connect' }));
    expect(
      within(form).getByText(
        'Enter a server name such as imap.example.com, without https:// or a port.'
      )
    ).toBeInTheDocument();
    expect(service.bindCredentials).not.toHaveBeenCalled();
  });

  it('binds, opens the new mailbox, and keeps the password nowhere', async () => {
    service.bindCredentials.mockResolvedValue({
      outcome: 'created',
      connection: connectionOf({ id: 9, provider: 'imap', mailbox_id: 77 }),
    });
    const onOpenChange = vi.fn();
    renderWith(<ConnectDialog onOpenChange={onOpenChange} />);
    const form = await openForm();
    fillForm(form);
    fireEvent.click(within(form).getByRole('radio', { name: '30 days' }));
    fireEvent.click(within(form).getByRole('button', { name: 'Connect' }));

    // Cleared from component state the moment it is submitted.
    expect(within(form).getByLabelText('App password')).toHaveValue('');
    await waitFor(() =>
      expect(service.bindCredentials).toHaveBeenCalledWith(7, {
        provider: 'imap',
        host: 'imap.example.com',
        port: 993,
        username: 'me@example.com',
        password: SECRET,
        backfill_days: 30,
      })
    );
    await waitFor(() =>
      expect(router.push).toHaveBeenCalledWith('/console/mailboxes?org=7&box=77')
    );
    expect(toast.success).toHaveBeenCalledWith('Connected. Syncing the last 30 days of mail.');
    expect(onOpenChange).toHaveBeenCalledWith(false);

    expectSecretNowhere();
  });

  it('says it reconnected when the server restores the member’s own account', async () => {
    service.bindCredentials.mockResolvedValue({
      outcome: 'restored',
      connection: connectionOf({ id: 9, provider: 'imap', mailbox_id: 77 }),
    });
    renderWith(<ConnectDialog />);
    const form = await openForm();
    fillForm(form);
    fireEvent.click(within(form).getByRole('button', { name: 'Connect' }));
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Reconnected. Mail is syncing again.')
    );
  });

  it('still closes and opens the mailbox when the dialog remounts before the answer', async () => {
    let answer: (value: unknown) => void = () => {};
    service.bindCredentials.mockReturnValue(new Promise(resolve => (answer = resolve)));
    const onOpenChange = vi.fn();
    const view = renderWith(<ConnectDialog onOpenChange={onOpenChange} />);
    const form = await openForm();
    fillForm(form);
    fireEvent.click(within(form).getByRole('button', { name: 'Connect' }));
    await waitFor(() => expect(service.bindCredentials).toHaveBeenCalled());
    // The inbox re-renders its dialogs elsewhere once the new mailbox is listed.
    view.rerender(
      <QueryClientProvider client={queryClient}>
        <NextIntlClientProvider
          locale="en-US"
          messages={messages as unknown as AbstractIntlMessages}
        >
          <ConnectDialog key="remounted" onOpenChange={onOpenChange} />
        </NextIntlClientProvider>
      </QueryClientProvider>
    );
    answer({
      outcome: 'created',
      connection: connectionOf({ id: 9, provider: 'imap', mailbox_id: 77 }),
    });
    await waitFor(() =>
      expect(router.push).toHaveBeenCalledWith('/console/mailboxes?org=7&box=77')
    );
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  const failures: [string, ApiError, string][] = [
    [
      'unreachable server',
      apiError(ApiErrorCode.MAILBOX_EXTERNAL_SERVER_UNREACHABLE, 422),
      'The mail server could not be reached over TLS on port 993.',
    ],
    [
      'refused sign-in',
      apiError(ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_FAILED, 422),
      'The server refused the username or app password.',
    ],
    [
      'account taken',
      apiError(ApiErrorCode.MAILBOX_EXTERNAL_ACCOUNT_TAKEN, 409),
      'Another member of your organization already connected this account.',
    ],
    [
      'connection limit',
      apiError(ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_LIMIT, 409),
      'You have connected the most accounts allowed (10).',
    ],
    [
      'rate limited with a retry time',
      apiError(ApiErrorCode.COMMON_RATE_LIMITED, 429, 125),
      'Too many attempts. Try again in 3 minutes.',
    ],
    [
      'rate limited without a retry time',
      apiError(ApiErrorCode.COMMON_RATE_LIMITED, 429),
      'Too many attempts. Try again later.',
    ],
    [
      'browser timeout',
      apiError(ClientErrorCode.TIMEOUT, 0),
      'The account may still have connected: check your connected mailboxes before trying again.',
    ],
    [
      'upstream timeout',
      apiError(ApiErrorCode.COMMON_TIMEOUT, 504),
      'The account may still have connected: check your connected mailboxes before trying again.',
    ],
    [
      'login of another account on a restore',
      apiError(ApiErrorCode.MAILBOX_EXTERNAL_ACCOUNT_MISMATCH, 409),
      'These details sign in to a different account than the one you are restoring.',
    ],
    [
      'invalid values',
      apiError(ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_INVALID, 422),
      'These details cannot be used.',
    ],
    [
      'connector gone',
      apiError(ApiErrorCode.MAILBOX_EXTERNAL_PROVIDER_UNAVAILABLE, 404),
      'This connector is no longer available in your organization.',
    ],
    [
      'anything else',
      apiError(ApiErrorCode.COMMON_INTERNAL, 500),
      'The mailbox could not be connected. Try again.',
    ],
  ];
  it.each(failures)('explains a %s inline', async (_name, error, text) => {
    service.bindCredentials.mockRejectedValue(error);
    renderWith(<ConnectDialog />);
    const form = await openForm();
    fillForm(form);
    fireEvent.click(within(form).getByRole('button', { name: 'Connect' }));
    const alert = await within(form).findByRole('alert');
    expect(alert).toHaveTextContent(text);
    expect(router.push).not.toHaveBeenCalled();
    // The member types the password again; nothing kept it.
    expect(within(form).getByLabelText('App password')).toHaveValue('');
    expect(within(form).getByLabelText('IMAP server')).toHaveValue(' imap.example.com ');
    expectSecretNowhere();
  });

  it('forgets a typed password when the dialog closes', async () => {
    renderWith(<ConnectDialog />);
    const form = await openForm();
    fillForm(form);
    fireEvent.keyDown(form, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: 'Open' }));
    expect(screen.getByRole('dialog', { name: 'Add an external mailbox' })).toBeInTheDocument();
    expect(document.body.innerHTML).not.toContain(SECRET);
    expect(service.bindCredentials).not.toHaveBeenCalled();
  });

  it('opens straight on the form to restore an account, and names the connection', async () => {
    service.bindCredentials.mockResolvedValue({
      outcome: 'restored',
      connection: connectionOf({ id: 4, provider: 'imap', mailbox_id: 77 }),
    });
    renderWith(
      <ConnectDialog preset={{ provider: 'imap', address: 'me@example.com', connectionId: 4 }} />
    );
    const form = await screen.findByRole('dialog', { name: 'Connect with an app password' });
    await within(form).findByLabelText('IMAP server');
    expect(form).toHaveTextContent('Sign in again with the same login to restore me@example.com.');
    fillForm(form);
    fireEvent.click(within(form).getByRole('button', { name: 'Connect' }));
    await waitFor(() =>
      expect(service.bindCredentials).toHaveBeenCalledWith(
        7,
        expect.objectContaining({ connection_id: 4, provider: 'imap' })
      )
    );
  });

  it('opens on the form even while the connector list is unknown', async () => {
    renderWith(
      <ExternalConnectDialog
        open
        onOpenChange={() => {}}
        organizationId={7}
        connectors={[]}
        preset={{ provider: 'imap', address: 'me@example.com', connectionId: 4 }}
      />
    );
    const form = await screen.findByRole('dialog', { name: 'Connect with an app password' });
    expect(await within(form).findByLabelText('App password')).toBeInTheDocument();
  });

  it('fails at once while offline and never sends or keeps the password', async () => {
    const online = vi.spyOn(window.navigator, 'onLine', 'get').mockReturnValue(false);
    try {
      renderWith(<ConnectDialog />);
      const form = await openForm();
      fillForm(form);
      fireEvent.click(within(form).getByRole('button', { name: 'Connect' }));
      expect(await within(form).findByRole('alert')).toHaveTextContent(
        'You are offline. Connect to the internet and try again.'
      );
      expect(service.bindCredentials).not.toHaveBeenCalled();
      expect(queryClient.getMutationCache().getAll()).toHaveLength(0);
      expectSecretNowhere();
    } finally {
      online.mockRestore();
    }
  });

  it('says it is still connecting while a slow server answers', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      service.bindCredentials.mockReturnValue(new Promise(() => {}));
      renderWith(<ConnectDialog />);
      const form = await openForm();
      fillForm(form);
      fireEvent.click(within(form).getByRole('button', { name: 'Connect' }));
      expect(within(form).queryByText(/Still connecting/u)).toBeNull();
      await act(async () => {
        vi.advanceTimersByTime(9_000);
      });
      expect(within(form).getByRole('status')).toHaveTextContent(
        'Still connecting. A slow mail server can take up to a minute to answer.'
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it('renders the form in Simplified Chinese', async () => {
    renderWith(<ConnectDialog />, 'zh-Hans');
    fireEvent.click(screen.getByRole('button', { name: 'IMAP' }));
    fireEvent.click(screen.getByRole('button', { name: '继续' }));
    const form = await screen.findByRole('dialog', { name: '使用应用专用密码连接' });
    expect(await within(form).findByLabelText('IMAP 服务器')).toBeInTheDocument();
    expect(within(form).getByLabelText('应用专用密码')).toBeInTheDocument();
  });
});

describe('sync status in the connected mailboxes dialog', () => {
  const cases: [string, ExternalMailSyncRun[], string[]][] = [
    ['no runs', [], ['Waiting for the first sync']],
    [
      'ok, backfilling',
      [run({ outcome: 'ok', more: true })],
      ['up to date', 'Last successful sync', 'Still copying older mail'],
    ],
    ['ok, done', [run({ outcome: 'ok' })], ['up to date', 'Last successful sync']],
    [
      'auth failed',
      [run({ outcome: 'auth_failed', error_code: 'external.auth_failed' }), run({ id: 1 })],
      ['the account refused access', 'Last successful sync'],
    ],
    [
      'fetch failed',
      [run({ outcome: 'fetch_failed', error_code: 'external.fetch_failed' })],
      ['the provider did not answer, retrying soon', 'No successful sync yet'],
    ],
    [
      'fetch timed out',
      [run({ outcome: 'fetch_failed', timed_out: true, error_code: 'external.fetch_failed' })],
      ['the provider took too long, retrying soon'],
    ],
    [
      'cursor expired',
      [run({ outcome: 'cursor_expired' })],
      ['starting over within the chosen period', 'Still copying older mail'],
    ],
    [
      'provider unavailable',
      [run({ outcome: 'provider_unavailable', error_code: 'external.provider_unavailable' })],
      ['this provider is turned off for now'],
    ],
    ['dropped', [run({ outcome: 'dropped' })], ['stopped because the account was disconnected']],
  ];
  it.each(cases)('shows %s in plain words', async (_name, runs, texts) => {
    service.syncRuns.mockResolvedValue(runs);
    renderWith(
      <ConnectedMailboxesDialog
        open
        onOpenChange={() => {}}
        organizationId={7}
        connections={[connectionOf({ id: 1 })]}
        error={false}
        onAdd={null}
      />
    );
    const row = rowOf(1);
    for (const text of texts) {
      await waitFor(() => expect(row).toHaveTextContent(text));
    }
    if (!texts.includes('Still copying older mail')) {
      expect(row).not.toHaveTextContent('Still copying older mail');
    }
    expect(service.syncRuns).toHaveBeenCalledWith(7, 1);
  });

  it('reconnects an app password connection through the connect form', async () => {
    service.syncRuns.mockResolvedValue([]);
    const onReconnectWithPassword = vi.fn();
    renderWith(
      <ConnectedMailboxesDialog
        open
        onOpenChange={() => {}}
        organizationId={7}
        connections={[
          connectionOf({
            id: 3,
            provider: 'imap',
            account_address: 'me@example.com',
            status: 'needs_reconnect',
          }),
        ]}
        connectors={[gmail, imap]}
        error={false}
        onAdd={null}
        onReconnectWithPassword={onReconnectWithPassword}
      />
    );
    fireEvent.click(within(rowOf(3)).getByRole('button', { name: 'Reconnect' }));
    expect(onReconnectWithPassword).toHaveBeenCalledWith({
      provider: 'imap',
      address: 'me@example.com',
      connectionId: 3,
    });
    expect(service.start).not.toHaveBeenCalled();
  });

  it('never falls back to an OAuth redirect for a password account while the list is unknown', async () => {
    service.syncRuns.mockResolvedValue([]);
    const onReconnectWithPassword = vi.fn();
    renderWith(
      <ConnectedMailboxesDialog
        open
        onOpenChange={() => {}}
        organizationId={7}
        connections={[
          connectionOf({ id: 3, provider: 'imap', account_address: 'me@example.com', status: 'needs_reconnect' }),
        ]}
        connectors={undefined}
        error={false}
        onAdd={null}
        onReconnectWithPassword={onReconnectWithPassword}
      />
    );
    fireEvent.click(within(rowOf(3)).getByRole('button', { name: 'Reconnect' }));
    expect(onReconnectWithPassword).toHaveBeenCalled();
    expect(service.start).not.toHaveBeenCalled();
  });

  it('says a connection cannot be reconnected once its connector is off', async () => {
    service.syncRuns.mockResolvedValue([]);
    renderWith(
      <ConnectedMailboxesDialog
        open
        onOpenChange={() => {}}
        organizationId={7}
        connections={[
          connectionOf({ id: 3, provider: 'imap', account_address: 'me@example.com', status: 'needs_reconnect' }),
          connectionOf({ id: 4, provider: 'outlook', account_address: 'o@outlook.com', status: 'revoked' }),
        ]}
        connectors={[gmail]}
        error={false}
        onAdd={null}
        onReconnectWithPassword={vi.fn()}
      />
    );
    for (const id of [3, 4]) {
      expect(within(rowOf(id)).queryByRole('button', { name: 'Reconnect' })).toBeNull();
      expect(rowOf(id)).toHaveTextContent(
        'This provider is no longer enabled in your organization, so it cannot be reconnected.'
      );
    }
    expect(within(rowOf(4)).getByRole('button', { name: 'Delete synced mail now' })).toBeEnabled();
  });

  it('reads the mailbox, its agent grants, and the unified inbox again after a disconnect', async () => {
    service.syncRuns.mockResolvedValue([]);
    service.disconnect.mockResolvedValue(undefined);
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries');
    renderWith(
      <ConnectedMailboxesDialog
        open
        onOpenChange={() => {}}
        organizationId={7}
        connections={[connectionOf({ id: 1, mailbox_id: 41 })]}
        error={false}
        onAdd={null}
      />
    );
    fireEvent.click(within(rowOf(1)).getByRole('button', { name: 'Disconnect' }));
    fireEvent.click(within(within(rowOf(1)).getByRole('group')).getByRole('button', { name: 'Disconnect' }));
    await waitFor(() => expect(service.disconnect).toHaveBeenCalledWith(7, 1));
    await waitFor(() => expect(invalidatedKeys(invalidate)).toEqual(expect.arrayContaining(connectedMailboxKeys(41))));
  });
});

describe('deleting a revoked connection’s mail', () => {
  function renderRevoked() {
    service.syncRuns.mockResolvedValue([]);
    renderWith(
      <ConnectedMailboxesDialog
        open
        onOpenChange={() => {}}
        organizationId={7}
        connections={[
          connectionOf({ id: 1 }),
          connectionOf({ id: 2, account_address: 'old@gmail.com', status: 'revoked' }),
        ]}
        error={false}
        onAdd={null}
      />
    );
  }

  it('is offered only for a revoked connection, after a confirmation', async () => {
    service.purge.mockResolvedValue({ deleted_messages: 120, completed: true });
    renderRevoked();
    expect(
      within(rowOf(1)).queryByRole('button', { name: 'Delete synced mail now' })
    ).not.toBeInTheDocument();
    const row = rowOf(2);
    fireEvent.click(within(row).getByRole('button', { name: 'Delete synced mail now' }));
    const confirm = within(row).getByRole('group', {
      name: 'Delete the mail synced from old@gmail.com?',
    });
    expect(confirm).toHaveFocus();
    expect(confirm).toHaveAccessibleDescription(/This cannot be undone\./u);
    fireEvent.click(within(confirm).getByRole('button', { name: 'Cancel' }));
    expect(service.purge).not.toHaveBeenCalled();
    expect(within(row).getByRole('button', { name: 'Delete synced mail now' })).toHaveFocus();

    fireEvent.click(within(row).getByRole('button', { name: 'Delete synced mail now' }));
    fireEvent.click(within(row).getByRole('button', { name: 'Delete for good' }));
    await waitFor(() => expect(service.purge).toHaveBeenCalledWith(7, 2));
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Deleted 120 messages synced from old@gmail.com')
    );
  });

  it('reads the deleted mailbox, its counts, and its agent grants again after a purge', async () => {
    service.purge.mockResolvedValue({ deleted_messages: 3, completed: true });
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries');
    renderRevoked();
    fireEvent.click(within(rowOf(2)).getByRole('button', { name: 'Delete synced mail now' }));
    fireEvent.click(within(rowOf(2)).getByRole('button', { name: 'Delete for good' }));
    await waitFor(() => expect(service.purge).toHaveBeenCalledWith(7, 2));
    await waitFor(() =>
      expect(invalidatedKeys(invalidate)).toEqual(expect.arrayContaining(connectedMailboxKeys(41)))
    );
  });

  it('says when the worker finishes the rest', async () => {
    service.purge.mockResolvedValue({ deleted_messages: 5000, completed: false });
    renderRevoked();
    fireEvent.click(within(rowOf(2)).getByRole('button', { name: 'Delete synced mail now' }));
    fireEvent.click(within(rowOf(2)).getByRole('button', { name: 'Delete for good' }));
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith(
        'Deleted 5,000 messages from old@gmail.com; the rest is deleted shortly'
      )
    );
  });

  it('explains a 409: the account was connected again', async () => {
    service.purge.mockRejectedValue(
      apiError(ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_NOT_REVOKED, 409)
    );
    renderRevoked();
    fireEvent.click(within(rowOf(2)).getByRole('button', { name: 'Delete synced mail now' }));
    fireEvent.click(within(rowOf(2)).getByRole('button', { name: 'Delete for good' }));
    expect(await within(rowOf(2)).findByRole('alert')).toHaveTextContent(
      'This account is connected again, so its mail is kept. Disconnect it first.'
    );
    expect(within(rowOf(2)).queryByRole('group')).not.toBeInTheDocument();
    expect(toast.success).not.toHaveBeenCalled();
  });

  it('keeps the confirmation open on another failure', async () => {
    service.purge.mockRejectedValue(apiError(ApiErrorCode.COMMON_INTERNAL, 500));
    renderRevoked();
    fireEvent.click(within(rowOf(2)).getByRole('button', { name: 'Delete synced mail now' }));
    fireEvent.click(within(rowOf(2)).getByRole('button', { name: 'Delete for good' }));
    const confirm = within(rowOf(2)).getByRole('group');
    expect(await within(confirm).findByRole('alert')).toHaveTextContent(
      'The synced mail could not be deleted. Try again.'
    );
  });
});

describe('copy', () => {
  it('has every new key in both locales', () => {
    for (const section of ['password', 'sync', 'purge', 'directory'] as const) {
      const english = flatten(enMailbox.external[section]);
      const chinese = flatten(zhMailbox.external[section]);
      expect(Object.keys(chinese).sort(), section).toEqual(Object.keys(english).sort());
    }
    expect(zhNav.adminExternalConnections).not.toBe('');
    expect(Object.keys(zhNav).sort()).toEqual(Object.keys(enNav).sort());
  });
});

function ConnectDialog({
  onOpenChange,
  preset = null,
}: {
  onOpenChange?: (open: boolean) => void;
  preset?: { provider: string; address: string; connectionId: number } | null;
}) {
  const [open, setOpen] = useState(true);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open
      </button>
      <ExternalConnectDialog
        open={open}
        onOpenChange={next => {
          setOpen(next);
          onOpenChange?.(next);
        }}
        organizationId={7}
        connectors={[gmail, imap]}
        preset={preset}
      />
    </>
  );
}

async function openForm(): Promise<HTMLElement> {
  fireEvent.click(screen.getByRole('button', { name: 'IMAP' }));
  fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
  const form = await screen.findByRole('dialog', { name: 'Connect with an app password' });
  // The form loads on demand.
  await within(form).findByLabelText('IMAP server');
  return form;
}

function fillForm(form: HTMLElement) {
  fireEvent.change(within(form).getByLabelText('IMAP server'), {
    target: { value: ' imap.example.com ' },
  });
  fireEvent.change(within(form).getByLabelText('Username'), {
    target: { value: 'me@example.com' },
  });
  fireEvent.change(within(form).getByLabelText('App password'), { target: { value: SECRET } });
}

/** The password is in no query or mutation, no storage, and not on the page. */
function expectSecretNowhere() {
  const queries = queryClient
    .getQueryCache()
    .getAll()
    .map(query => ({ key: query.queryKey, data: query.state.data }));
  const mutations = queryClient
    .getMutationCache()
    .getAll()
    .map(mutation => ({ key: mutation.options.mutationKey, state: mutation.state }));
  expect(JSON.stringify(queries)).not.toContain(SECRET);
  expect(JSON.stringify(mutations)).not.toContain(SECRET);
  const stored = [window.localStorage, window.sessionStorage].flatMap(storage =>
    Array.from({ length: storage.length }, (_, index) => {
      const key = storage.key(index) ?? '';
      return `${key}=${storage.getItem(key) ?? ''}`;
    })
  );
  expect(stored.join('\n')).not.toContain(SECRET);
  expect(document.body.innerHTML).not.toContain(SECRET);
  for (const input of Array.from(document.querySelectorAll('input'))) {
    expect(input.value).not.toBe(SECRET);
  }
}

function invalidatedKeys(spy: { mock: { calls: unknown[][] } }): string[] {
  return spy.mock.calls.map(([filters]) =>
    JSON.stringify((filters as { queryKey?: unknown } | undefined)?.queryKey)
  );
}

/** The mailbox key covers its messages, counts, and agent grants (`[..., 'agent-grants']`). */
function connectedMailboxKeys(mailboxId: number): string[] {
  return [
    JSON.stringify(mailboxKeys.mailbox(7, mailboxId)),
    JSON.stringify(mailboxKeys.inboxes(7)),
    JSON.stringify(mailboxKeys.allInboxCounts(7)),
    JSON.stringify(mailboxKeys.list(7)),
  ];
}

function apiError(code: string, status: number, retryAfterSeconds?: number): ApiError {
  return new ApiError('refused', code as ApiError['errorCode'], status, 'req-1', undefined, retryAfterSeconds);
}

function run(overrides: Partial<ExternalMailSyncRun>): ExternalMailSyncRun {
  return {
    id: 2,
    started_at: '2026-11-20T08:00:00Z',
    finished_at: '2026-11-20T08:00:01Z',
    outcome: 'ok',
    fetched_count: 25,
    stored_count: 24,
    more: false,
    error_code: null,
    timed_out: false,
    throttled: false,
    ...overrides,
  };
}

function rowOf(id: number): HTMLElement {
  const row = document.querySelector(`[data-connection="${id}"]`);
  if (!(row instanceof HTMLElement)) throw new Error(`no row ${id}`);
  return row;
}

function connectionOf(overrides: Partial<ExternalMailConnection>): ExternalMailConnection {
  return {
    id: 1,
    provider: 'gmail',
    account_address: 'me@gmail.com',
    mailbox_id: 41,
    status: 'active',
    last_synced_at: '2026-11-20T08:00:00Z',
    last_error_code: null,
    created_at: '2026-11-19T08:00:00Z',
    ...overrides,
  };
}

function flatten(value: unknown, prefix = ''): Record<string, string> {
  if (typeof value === 'string') return { [prefix]: value };
  return Object.entries(value as Record<string, unknown>).reduce<Record<string, string>>(
    (all, [key, child]) => ({ ...all, ...flatten(child, prefix ? `${prefix}.${key}` : key) }),
    {}
  );
}

function renderWith(children: React.ReactNode, locale: 'en-US' | 'zh-Hans' = 'en-US') {
  const all = messages as unknown as Record<string, unknown>;
  const localized = locale === 'en-US' ? all : { ...all, mailbox: zhMailbox, nav: zhNav };
  return render(
    <QueryClientProvider client={queryClient}>
      <NextIntlClientProvider locale={locale} messages={localized as AbstractIntlMessages}>
        {children}
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}
