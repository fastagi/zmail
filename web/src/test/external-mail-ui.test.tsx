import { StrictMode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ConnectedMailboxesDialog } from '@/features/mailbox/components/connected-mailboxes-dialog';
import { ExternalConnectDialog } from '@/features/mailbox/components/external-connect-dialog';
import {
  ExternalMailCallback,
  resetSubmittedStates,
} from '@/features/mailbox/components/external-mail-callback';
import type {
  ExternalMailConnection,
  ExternalMailConnector,
} from '@/features/mailbox/external-mail-schemas';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';
import enMailbox from '@/i18n/modules/mailbox/en-US';
import zhMailbox from '@/i18n/modules/mailbox/zh-Hans';

const service = vi.hoisted(() => ({
  connectors: vi.fn(),
  connections: vi.fn(),
  start: vi.fn(),
  complete: vi.fn(),
  disconnect: vi.fn(),
}));
const navigate = vi.hoisted(() => vi.fn());
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
vi.mock('@/features/mailbox/utils/external-mail', async importOriginal => ({
  ...(await importOriginal<typeof import('@/features/mailbox/utils/external-mail')>()),
  navigateToProvider: navigate,
}));

const gmail: ExternalMailConnector = {
  id: 'gmail',
  auth_kind: 'oauth2',
  capabilities: { read: true },
};
const outlook: ExternalMailConnector = {
  id: 'outlook',
  auth_kind: 'oauth2',
  capabilities: { read: true },
};
const imap: ExternalMailConnector = {
  id: 'imap',
  auth_kind: 'app_password',
  capabilities: { read: true },
};

const storageKey = (state: string) => `zmail.externalMailAuthorization.${state}`;

let queryClient: QueryClient;

beforeEach(() => {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  for (const fn of Object.values(service)) fn.mockReset();
  navigate.mockReset();
  toast.success.mockReset();
  toast.error.mockReset();
  router.replace.mockReset();
  window.sessionStorage.clear();
  window.localStorage.clear();
  window.history.replaceState(null, '', '/');
  resetSubmittedStates();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('connect dialog', () => {
  it('offers one button per connector, with unsupported kinds coming soon', () => {
    renderWith(
      <ExternalConnectDialog
        open
        onOpenChange={() => {}}
        organizationId={7}
        connectors={[gmail, outlook, imap]}
      />
    );

    const dialog = screen.getByRole('dialog', { name: 'Add an external mailbox' });
    const buttons = within(dialog)
      .getAllByRole('button')
      .filter(button => button.hasAttribute('data-connector'));
    expect(buttons.map(button => button.getAttribute('data-connector'))).toEqual([
      'gmail',
      'outlook',
      'imap',
    ]);
    expect(within(dialog).getByRole('button', { name: 'Gmail' })).toBeEnabled();
    expect(within(dialog).getByRole('button', { name: 'Outlook' })).toBeEnabled();
    const soon = within(dialog).getByRole('button', { name: /IMAP/u });
    expect(soon).toBeDisabled();
    expect(soon).toHaveTextContent('Coming soon');
  });

  it('explains what connecting means, then starts and navigates to the provider', async () => {
    const authorizationUrl =
      'https://accounts.example.com/o/oauth2/auth?state=st4te-1&code_challenge=x';
    service.start.mockResolvedValue({
      authorization_url: authorizationUrl,
      expires_at: '2026-11-21T10:10:00Z',
    });
    renderWith(
      <ExternalConnectDialog
        open
        onOpenChange={() => {}}
        organizationId={7}
        connectors={[gmail, outlook]}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Outlook' }));
    const dialog = screen.getByRole('dialog', { name: 'Before you connect Outlook' });
    expect(dialog).toHaveTextContent('Read-only.');
    expect(dialog).toHaveTextContent(
      'Only you can see this mail. Organization owners and admins cannot read it.'
    );
    expect(dialog).toHaveTextContent('The connection is removed when you leave the organization.');
    const fourteen = within(dialog).getByRole('radio', { name: '14 days' });
    expect(fourteen).toBeChecked();
    expect(within(dialog).getByRole('radio', { name: '30 days' })).not.toBeChecked();
    fireEvent.click(within(dialog).getByRole('radio', { name: '90 days' }));

    fireEvent.click(within(dialog).getByRole('button', { name: 'Continue to Outlook' }));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith(authorizationUrl));
    expect(service.start).toHaveBeenCalledWith(7, { provider: 'outlook', backfill_days: 90 });
    // The organization survives the round trip in session storage, never in the provider URL.
    expect(JSON.parse(window.sessionStorage.getItem(storageKey('st4te-1')) ?? 'null')).toEqual({
      organizationId: 7,
      provider: 'outlook',
      backfillDays: 90,
      connectionId: null,
    });
    expect(authorizationUrl).not.toContain('7');
    // The button stays busy while the browser leaves.
    expect(within(dialog).getByRole('button', { name: /Opening Outlook/u })).toBeDisabled();
  });

  it('goes back to the providers and reports a failed start', async () => {
    service.start.mockRejectedValue(
      new ApiError('gone', ApiErrorCode.MAILBOX_EXTERNAL_PROVIDER_UNAVAILABLE, 404)
    );
    renderWith(
      <ExternalConnectDialog open onOpenChange={() => {}} organizationId={7} connectors={[gmail]} />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Gmail' }));
    fireEvent.click(screen.getByRole('button', { name: 'Continue to Gmail' }));
    expect(
      await screen.findByText('This provider is no longer available. Choose another one.')
    ).toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(screen.getByRole('dialog', { name: 'Add an external mailbox' })).toBeInTheDocument();
  });
});

describe('callback page', () => {
  function arrive(query: string, pending: Record<string, unknown> | null) {
    window.history.replaceState(null, '', `/console/mailboxes/connected/callback?${query}`);
    if (pending) {
      window.sessionStorage.setItem(storageKey('abc'), JSON.stringify(pending));
    }
  }
  const newBinding = { organizationId: 7, provider: 'gmail', backfillDays: 30, connectionId: null };
  const reconnecting = { organizationId: 7, provider: 'gmail', backfillDays: null, connectionId: 3 };

  it('201: completes once, then opens the new mailbox with a syncing notice', async () => {
    arrive('state=abc&code=xyz', newBinding);
    service.complete.mockResolvedValue({
      outcome: 'created',
      connection: connectionOf({ id: 3, mailbox_id: 41 }),
    });
    renderWith(
      <StrictMode>
        <ExternalMailCallback />
      </StrictMode>
    );

    expect(screen.getByText('Finishing the connection…')).toBeInTheDocument();
    await waitFor(() =>
      expect(router.replace).toHaveBeenCalledWith('/console/mailboxes?org=7&box=41')
    );
    expect(service.complete).toHaveBeenCalledTimes(1);
    expect(service.complete).toHaveBeenCalledWith(7, { state: 'abc', code: 'xyz' });
    expect(toast.success).toHaveBeenCalledWith('Connected. Syncing the last 30 days of mail.');
    // The pending sign-in is used up.
    expect(window.sessionStorage.getItem(storageKey('abc'))).toBeNull();
  });

  it('200: says it reconnected', async () => {
    arrive('state=abc&code=xyz', reconnecting);
    service.complete.mockResolvedValue({
      outcome: 'restored',
      connection: connectionOf({ id: 3, mailbox_id: 41 }),
    });
    renderWith(<ExternalMailCallback />);

    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Reconnected. Mail is syncing again.'));
    expect(router.replace).toHaveBeenCalledWith('/console/mailboxes?org=7&box=41');
  });

  it.each([
    [
      ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_NOT_FOUND,
      404,
      'This sign-in expired or was already used. Start again from your inbox.',
    ],
    [
      ApiErrorCode.MAILBOX_EXTERNAL_ACCOUNT_MISMATCH,
      409,
      'You signed in to a different account than the one you are reconnecting. Sign in to the original account.',
    ],
    [
      ApiErrorCode.MAILBOX_EXTERNAL_ACCOUNT_TAKEN,
      409,
      'Another member of your organization already connected this account.',
    ],
    [
      ApiErrorCode.MAILBOX_EXTERNAL_CONNECTION_LIMIT,
      409,
      'You have connected the most accounts allowed (10). Disconnect one first.',
    ],
    [
      ApiErrorCode.MAILBOX_EXTERNAL_AUTHORIZATION_FAILED,
      422,
      'The provider did not accept the sign-in. Try again.',
    ],
  ])('%s: explains it and offers another account', async (code, status, text) => {
    arrive('state=abc&code=xyz', newBinding);
    service.complete.mockRejectedValue(new ApiError('refused', code, status));
    service.start.mockResolvedValue({
      authorization_url: 'https://accounts.example.com/auth?state=next',
      expires_at: '2026-11-21T10:10:00Z',
    });
    renderWith(<ExternalMailCallback />);

    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(service.complete).toHaveBeenCalledTimes(1);
    expect(router.replace).not.toHaveBeenCalled();
    expect(screen.getByRole('link', { name: 'Back to inbox' })).toHaveAttribute(
      'href',
      '/console/mailboxes?org=7'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Try another account' }));
    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith('https://accounts.example.com/auth?state=next')
    );
    expect(service.start).toHaveBeenCalledWith(7, { provider: 'gmail', backfill_days: 30 });
  });

  it('a reconnect tries again with the same connection', async () => {
    arrive('state=abc&code=xyz', reconnecting);
    service.complete.mockRejectedValue(
      new ApiError('refused', ApiErrorCode.MAILBOX_EXTERNAL_ACCOUNT_MISMATCH, 409)
    );
    service.start.mockResolvedValue({
      authorization_url: 'https://accounts.example.com/auth?state=next',
      expires_at: '2026-11-21T10:10:00Z',
    });
    renderWith(<ExternalMailCallback />);

    fireEvent.click(await screen.findByRole('button', { name: 'Try another account' }));
    await waitFor(() => expect(service.start).toHaveBeenCalledWith(7, { connection_id: 3 }));
  });

  it('a provider error or a missing state never calls the API', async () => {
    arrive('error=access_denied&state=abc', newBinding);
    const { unmount } = renderWith(<ExternalMailCallback />);
    expect(
      screen.getByText('The sign-in was cancelled, so nothing was connected.')
    ).toBeInTheDocument();
    unmount();

    // Without the session entry the organization is unknown, so nothing is sent.
    window.sessionStorage.clear();
    arrive('state=abc&code=xyz', null);
    const second = renderWith(<ExternalMailCallback />);
    expect(
      screen.getByText('This sign-in expired or was already used. Start again from your inbox.')
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Try another account' })).not.toBeInTheDocument();
    second.unmount();

    arrive('code=xyz', null);
    renderWith(<ExternalMailCallback />);
    expect(screen.getByText(/missing the details the provider sends back/u)).toBeInTheDocument();
    await act(async () => {});
    expect(service.complete).not.toHaveBeenCalled();
  });
});

describe('connected mailboxes dialog', () => {
  it('lists connections and disconnects after an inline confirmation', async () => {
    service.disconnect.mockResolvedValue(undefined);
    const onAdd = vi.fn();
    renderWith(
      <ConnectedMailboxesDialog
        open
        onOpenChange={() => {}}
        organizationId={7}
        connections={[
          connectionOf({ id: 1, account_address: 'me@gmail.com' }),
          connectionOf({
            id: 2,
            provider: 'outlook',
            account_address: 'old@outlook.com',
            status: 'needs_reconnect',
            last_synced_at: null,
          }),
        ]}
        error={false}
        onAdd={onAdd}
      />
    );

    const first = rowOf(1);
    expect(first).toHaveTextContent('me@gmail.com');
    expect(first).toHaveTextContent('Syncing');
    expect(first).toHaveTextContent('Last synced');
    expect(within(first).queryByRole('button', { name: 'Reconnect' })).not.toBeInTheDocument();
    const second = rowOf(2);
    expect(second).toHaveTextContent('Needs reconnecting');
    expect(second).toHaveTextContent('Not synced yet');
    expect(within(second).getByRole('button', { name: 'Reconnect' })).toBeInTheDocument();

    fireEvent.click(within(first).getByRole('button', { name: 'Disconnect' }));
    const confirm = within(first).getByRole('group', { name: 'Disconnect me@gmail.com?' });
    expect(confirm).toHaveFocus();
    expect(confirm).toHaveTextContent(
      'Syncing stops now. Mail already synced is kept for 30 days.'
    );
    fireEvent.click(within(confirm).getByRole('button', { name: 'Cancel' }));
    expect(within(first).queryByRole('group')).not.toBeInTheDocument();
    expect(service.disconnect).not.toHaveBeenCalled();
    expect(within(first).getByRole('button', { name: 'Disconnect' })).toHaveFocus();

    fireEvent.click(within(first).getByRole('button', { name: 'Disconnect' }));
    fireEvent.click(
      within(within(first).getByRole('group')).getByRole('button', { name: 'Disconnect' })
    );
    await waitFor(() => expect(service.disconnect).toHaveBeenCalledWith(7, 1));
    expect(toast.success).toHaveBeenCalledWith('Disconnected me@gmail.com');

    fireEvent.click(screen.getByRole('button', { name: 'Add external mailbox' }));
    expect(onAdd).toHaveBeenCalled();
  });

  it('reconnect starts a sign-in for that connection', async () => {
    service.start.mockResolvedValue({
      authorization_url: 'https://login.example.com/authorize?state=r1',
      expires_at: '2026-11-21T10:10:00Z',
    });
    renderWith(
      <ConnectedMailboxesDialog
        open
        onOpenChange={() => {}}
        organizationId={7}
        connections={[connectionOf({ id: 2, provider: 'outlook', status: 'revoked' })]}
        error={false}
        onAdd={null}
      />
    );

    expect(within(rowOf(2)).queryByRole('button', { name: 'Disconnect' })).not.toBeInTheDocument();
    fireEvent.click(within(rowOf(2)).getByRole('button', { name: 'Reconnect' }));
    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith('https://login.example.com/authorize?state=r1')
    );
    expect(service.start).toHaveBeenCalledWith(7, { connection_id: 2 });
    expect(JSON.parse(window.sessionStorage.getItem(storageKey('r1')) ?? 'null')).toMatchObject({
      organizationId: 7,
      connectionId: 2,
      backfillDays: null,
    });
    expect(screen.queryByRole('button', { name: 'Add external mailbox' })).not.toBeInTheDocument();
  });
});

describe('external mail copy', () => {
  it('has every English key in Simplified Chinese, translated', () => {
    const english = flatten(enMailbox.external);
    const chinese = flatten(zhMailbox.external);
    expect(Object.keys(chinese).sort()).toEqual(Object.keys(english).sort());
    for (const [key, value] of Object.entries(chinese)) {
      expect(value, key).not.toBe('');
      // Pure placeholder patterns carry no words to translate.
      if (/[\p{L}]/u.test(value.replace(/\{[^}]+\}/gu, ''))) {
        expect(value, key).toMatch(/\p{Script=Han}/u);
      }
    }
  });

  it('renders the consent step in Simplified Chinese', () => {
    renderWith(
      <ExternalConnectDialog open onOpenChange={() => {}} organizationId={7} connectors={[gmail]} />,
      'zh-Hans'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Gmail' }));
    const dialog = screen.getByRole('dialog', { name: '接入 Gmail 之前' });
    expect(dialog).toHaveTextContent('只有你能看到这些邮件，组织的所有者和管理员都看不到。');
    expect(within(dialog).getByRole('radio', { name: '30 天' })).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: '前往 Gmail' })).toBeInTheDocument();
  });
});

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
  const all = messages as unknown as Record<string, AbstractIntlMessages>;
  return render(
    <QueryClientProvider client={queryClient}>
      <NextIntlClientProvider
        locale={locale}
        messages={(locale === 'en-US' ? all : zhMessages()) as AbstractIntlMessages}
      >
        {children}
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

function zhMessages(): Record<string, unknown> {
  return {
    ...(messages as unknown as Record<string, unknown>),
    mailbox: zhMailbox,
  };
}
