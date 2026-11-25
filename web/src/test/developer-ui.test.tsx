import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { DeveloperConsole } from '@/features/developer';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import {
  CLIENT_MESSAGE_NAMESPACES,
  developerMessageNamespaces,
} from '@/i18n/client-message-namespaces';
import { selectMessageNamespaces } from '@/i18n/message-selection';
import { messages } from '@/i18n/modules';

const state = vi.hoisted(() => {
  const query = () => ({
    data: undefined as unknown,
    isPending: false,
    isSuccess: true,
    error: null as unknown,
    refetch: vi.fn(),
  });
  return {
    search: '',
    webhookEnabled: false,
    replace: vi.fn(),
    organizations: query(),
    domains: query(),
    templates: query(),
    template: query(),
  };
});

const developer = vi.hoisted(() => ({
  log: vi.fn(),
  logEntry: vi.fn(),
  testSend: vi.fn(),
}));
const apiKeys = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  revoke: vi.fn(),
}));

vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: state.replace }),
  usePathname: () => '/console/developers',
  useSearchParams: () => new URLSearchParams(state.search),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => state.organizations,
}));
vi.mock('@/config/features', async importOriginal => {
  const original = await importOriginal<typeof import('@/config/features')>();
  return {
    ...original,
    isWebFeatureEnabled: (feature: Parameters<typeof original.isWebFeatureEnabled>[0]) =>
      feature === 'webhook' ? state.webhookEnabled : original.isWebFeatureEnabled(feature),
  };
});
vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useMailboxDomains: () => state.domains,
}));
vi.mock('@/features/mailbox/hooks/use-mail-templates', () => ({
  useMailTemplates: () => state.templates,
  useMailTemplate: () => state.template,
  useSaveMailTemplate: () => ({ isPending: false, mutateAsync: vi.fn() }),
}));
vi.mock('@/features/developer/services/developer-service', () => ({
  developerService: developer,
}));
vi.mock('@/features/api-key/services/api-key-service', () => ({ apiKeyService: apiKeys }));

const FULL_ADDRESS = 'lilei@example.com';

describe('developer quick start and send log', () => {
  const clipboard = { writeText: vi.fn() };

  beforeEach(() => {
    state.search = 'org=1';
    state.webhookEnabled = false;
    state.replace.mockReset();
    state.organizations.data = organizations();
    state.domains.data = [
      domain({ domain: 'mail.example.com', kind: 'shared' }),
      domain({ domain: 'acme.example', kind: 'organization' }),
    ];
    state.templates.data = [summary()];
    state.template.data = template();
    developer.log.mockReset().mockResolvedValue(page([]));
    developer.logEntry.mockReset();
    developer.testSend.mockReset();
    apiKeys.list.mockReset().mockResolvedValue(keyPage([]));
    apiKeys.create.mockReset();
    clipboard.writeText.mockReset().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: clipboard, configurable: true });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('samples call this site, name no env variable, and copy (#144)', async () => {
    state.search = 'org=1&tab=start&mode=test';
    const { container } = renderConsole();

    const samples = await screen.findByLabelText('Code samples');
    expect(samples).toHaveTextContent(`${window.location.origin}/v1/emails`);
    expect(samples).toHaveTextContent('Authorization: Bearer');
    expect(samples).toHaveTextContent('noreply@acme.example');
    expect(container.textContent).not.toContain('api.example.com');
    expect(container.textContent).not.toContain('NEXT_PUBLIC_');

    fireEvent.click(screen.getByRole('button', { name: 'Copy code' }));
    await waitFor(() =>
      expect(clipboard.writeText).toHaveBeenCalledWith(
        expect.stringContaining(`${window.location.origin}/v1/emails`)
      )
    );

    fireEvent.click(screen.getByRole('button', { name: 'Template' }));
    expect(samples).toHaveTextContent('signup-code');
  });

  it('creating a key shows it once then only the prefix', async () => {
    state.search = 'org=1&tab=start&mode=test';
    const plaintext = 'fastbuild_test_ab12cd_SECRETVALUE9z8y';
    apiKeys.create.mockResolvedValue({
      api_key: apiKey({ key_prefix: 'fastbuild_test_ab12cd', mode: 'test' }),
      plaintext_key: plaintext,
    });
    const { container } = renderConsole();

    const steps = await screen.findByRole('list', { name: 'Setup steps' });
    expect(within(steps).getByText('Sending from acme.example.')).toBeInTheDocument();
    expect(within(steps).getAllByText('To do')).toHaveLength(2);
    expect(screen.getByText(/leave the organization it stops working/u)).toBeInTheDocument();

    fireEvent.click(await screen.findByRole('button', { name: 'Create a test key' }));
    await waitFor(() => expect(apiKeys.create).toHaveBeenCalled());
    expect(apiKeys.create.mock.calls[0]?.[0]).toEqual({
      name: 'Transactional test key',
      scopes: ['mail:transactional'],
      mode: 'test',
    });

    // Created but not yet revealed: only the prefix and the last four characters.
    const key = screen.getByTestId('quick-start-key');
    await waitFor(() => expect(key).toHaveTextContent('fastbuild_test_ab12cd••••9z8y'));
    expect(container.textContent).not.toContain(plaintext);
    // The create mutation's result held the plaintext too; it is dropped as soon as the page has it.
    expect(
      JSON.stringify(queryClient.getMutationCache().getAll().map(mutation => mutation.state))
    ).not.toContain(plaintext);

    fireEvent.click(screen.getByRole('button', { name: 'Show and copy' }));
    await waitFor(() => expect(key).toHaveTextContent(plaintext));
    expect(clipboard.writeText).toHaveBeenCalledWith(plaintext);
    expect(await screen.findByText('Key copied to the clipboard.')).toHaveAttribute(
      'aria-live',
      'polite'
    );
    expect(screen.getByLabelText('Code samples')).toHaveTextContent(plaintext);

    fireEvent.click(screen.getByRole('button', { name: 'I saved it' }));
    await waitFor(() => expect(container.textContent).not.toContain(plaintext));
    expect(key).toHaveTextContent('fastbuild_test_ab12cd••••9z8y');
    expect(screen.getByLabelText('Code samples')).toHaveTextContent('YOUR_API_KEY');
    expect(screen.queryByRole('button', { name: 'Show and copy' })).toBeNull();
    // The create mutation's result held the plaintext too; it is dropped with the page state.
    await waitFor(() =>
      expect(
        JSON.stringify(queryClient.getMutationCache().getAll().map(mutation => mutation.state))
      ).not.toContain(plaintext)
    );
  });

  it('switching the key mode drops a created key, plaintext included', async () => {
    state.search = 'org=1&tab=start&mode=test';
    const plaintext = 'fastbuild_test_ab12cd_SECRETVALUE9z8y';
    apiKeys.create.mockResolvedValue({
      api_key: apiKey({ key_prefix: 'fastbuild_test_ab12cd', mode: 'test' }),
      plaintext_key: plaintext,
    });
    const { container, rerender } = renderConsole();
    fireEvent.click(await screen.findByRole('button', { name: 'Create a test key' }));
    await waitFor(() =>
      expect(screen.getByTestId('quick-start-key')).toHaveTextContent('fastbuild_test_ab12cd••••9z8y')
    );

    state.search = 'org=1&tab=start&mode=live';
    rerender(wrap(queryClient));
    await screen.findByRole('button', { name: 'Create a live key' });
    state.search = 'org=1&tab=start&mode=test';
    rerender(wrap(queryClient));

    expect(await screen.findByRole('button', { name: 'Create a test key' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Show and copy' })).toBeNull();
    expect(screen.getByTestId('quick-start-key')).not.toHaveTextContent('9z8y');
    expect(container.textContent).not.toContain(plaintext);
  });

  it('counts only an own key with exactly the transactional scope in the current mode', async () => {
    state.search = 'org=1&tab=start&mode=live';
    apiKeys.list.mockResolvedValue(
      keyPage([
        apiKey({
          id: 1,
          key_prefix: 'fastbuild_wide',
          scopes: ['mail:transactional', 'mail:send'],
        }),
        apiKey({ id: 2, key_prefix: 'fastbuild_testmode', mode: 'test' }),
        apiKey({ id: 3, key_prefix: 'fastbuild_good' }),
      ])
    );
    developer.log.mockResolvedValue(page([entry()]));
    renderConsole();
    const steps = await screen.findByRole('list', { name: 'Setup steps' });
    await waitFor(() =>
      expect(
        within(steps).getByText('Your live key fastbuild_good has the scope mail:transactional.')
      ).toBeInTheDocument()
    );
    await waitFor(() => expect(within(steps).getAllByText('Done')).toHaveLength(3));
    expect(developer.log).toHaveBeenCalledWith(1, { mode: 'live' }, { perPage: 1 });
  });

  it('test mode test send says nothing was delivered', async () => {
    state.search = 'org=1&tab=start&mode=test';
    developer.testSend.mockResolvedValue(
      message({ mode: 'test', simulated: true, status: 'sent', last_event: 'delivered' })
    );
    renderConsole();
    fireEvent.click(await screen.findByRole('button', { name: 'Send a test' }));
    expect(
      await screen.findByText(
        'Test mode: rendered successfully, not actually sent. Nothing was delivered.'
      )
    ).toBeInTheDocument();
    expect(developer.testSend).toHaveBeenCalledWith(1, { template: 'signup-code', mode: 'test' });
    expect(screen.queryByText(/Delivered by the receiving server/u)).toBeNull();
  });

  it('live test send follows the message until its delivery event', async () => {
    state.search = 'org=1&tab=start&mode=live';
    developer.testSend.mockResolvedValue(message({ status: 'queued' }));
    developer.logEntry.mockResolvedValue(
      detail({
        status: 'sent',
        last_event: 'delivered',
        events: [
          { type: 'accepted', detail: '', occurred_at: '2026-11-19T14:22:09Z' },
          { type: 'delivered', detail: '', occurred_at: '2026-11-19T14:22:11Z' },
        ],
      })
    );
    renderConsole();
    fireEvent.click(await screen.findByRole('button', { name: 'Send a test' }));
    expect(
      await screen.findByText(
        `Sent to ${FULL_ADDRESS} · Delivered · Latest event: Delivered by the receiving server`
      )
    ).toBeInTheDocument();
    expect(developer.logEntry).toHaveBeenCalledWith(1, 7012);
    // A delivered message is settled: polling stops instead of running for 30 seconds.
    const calls = developer.logEntry.mock.calls.length;
    await new Promise(resolve => setTimeout(resolve, 2_500));
    expect(developer.logEntry.mock.calls.length).toBe(calls);
  });

  it('a rate-limited test send shows the retry seconds', async () => {
    state.search = 'org=1&tab=start&mode=live';
    developer.testSend.mockRejectedValue(
      new ApiError('slow down', ApiErrorCode.COMMON_RATE_LIMITED, 429, 'req-1', undefined, 12)
    );
    renderConsole();
    fireEvent.click(await screen.findByRole('button', { name: 'Send a test' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Too many test sends. Please try again in 12 s.'
    );
  });

  it('log chips map to outcome and mode query', async () => {
    state.search = 'org=1&tab=logs&mode=live';
    const view = renderConsole();
    const chips = await screen.findByRole('group', { name: 'Filter by outcome' });
    expect(within(chips).getByRole('button', { name: 'All' })).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    await waitFor(() =>
      expect(developer.log).toHaveBeenLastCalledWith(1, { mode: 'live' }, { perPage: 50 })
    );

    fireEvent.click(within(chips).getByRole('button', { name: 'Delivered' }));
    expect(state.replace).toHaveBeenLastCalledWith(
      '/console/developers?org=1&tab=logs&mode=live&outcome=delivered',
      { scroll: false }
    );
    fireEvent.click(within(chips).getByRole('button', { name: 'Bounced · Blocked' }));
    expect(state.replace).toHaveBeenLastCalledWith(
      '/console/developers?org=1&tab=logs&mode=live&outcome=problem',
      { scroll: false }
    );

    state.search = 'org=1&tab=logs&mode=live&outcome=problem&template=signup-code';
    view.rerender(wrap());
    await waitFor(() =>
      expect(developer.log).toHaveBeenLastCalledWith(
        1,
        { mode: 'live', outcome: 'problem', template: 'signup-code' },
        { perPage: 50 }
      )
    );
    expect(screen.getByLabelText('Template')).toHaveTextContent('signup-code');

    fireEvent.click(
      within(screen.getByRole('group', { name: 'Filter by outcome' })).getByRole('button', {
        name: 'Test',
      })
    );
    expect(state.replace).toHaveBeenLastCalledWith(
      '/console/developers?org=1&tab=logs&mode=test&template=signup-code',
      { scroll: false }
    );
    state.search = 'org=1&tab=logs&mode=test&template=signup-code';
    view.rerender(wrap());
    await waitFor(() =>
      expect(developer.log).toHaveBeenLastCalledWith(
        1,
        { mode: 'test', template: 'signup-code' },
        { perPage: 50 }
      )
    );
    expect(
      within(screen.getByRole('group', { name: 'Filter by outcome' })).getByRole('button', {
        name: 'Test',
      })
    ).toHaveAttribute('aria-pressed', 'true');

    // The exact recipient search is sent trimmed and lowercased, and stays out of the URL.
    state.replace.mockReset();
    fireEvent.change(screen.getByLabelText('Exact recipient'), {
      target: { value: '  LiLei@Example.COM ' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await waitFor(() =>
      expect(developer.log).toHaveBeenLastCalledWith(
        1,
        { mode: 'test', template: 'signup-code', to: FULL_ADDRESS },
        { perPage: 50 }
      )
    );
    expect(state.replace).not.toHaveBeenCalled();
  });

  it('log row never shows a full address', async () => {
    state.search = 'org=1&tab=logs&mode=live';
    developer.log.mockResolvedValue(
      page(
        [
          entry(),
          entry({
            id: 7011,
            status: 'suppressed',
            last_event: 'rejected',
            to_masked: 'ab••@bad-domain.example',
            idempotency_key: null,
          }),
        ],
        { total_24h: 1204 }
      )
    );
    developer.logEntry.mockResolvedValue(
      detail({
        events: [
          { type: 'accepted', detail: '', occurred_at: '2026-11-19T14:22:09Z' },
          { type: 'delivered', detail: '', occurred_at: '2026-11-19T14:22:11Z' },
        ],
      })
    );
    const { container } = renderConsole();
    const table = await screen.findByRole('table', { name: 'Transactional messages' });
    expect(within(table).getByText('li••@example.com')).toBeInTheDocument();
    expect(within(table).getAllByText('Acme verification code: ••••••')).toHaveLength(2);
    expect(within(table).getByText('Delivered')).toBeInTheDocument();
    expect(within(table).getByText('Blocked')).toBeInTheDocument();
    expect(within(table).getByText('signup-u_8812')).toBeInTheDocument();
    expect(screen.getByText("Kept 30 days · never in any mailbox's Sent")).toBeInTheDocument();
    expect(screen.getByText('1,204 in the last 24 hours')).toBeInTheDocument();

    fireEvent.click(
      within(table).getByRole('button', { name: 'Show signup-code to li••@example.com' })
    );
    const panel = await screen.findByRole('region', { name: 'signup-code → li••@example.com' });
    expect(await within(panel).findByText('Delivered by the receiving server')).toBeInTheDocument();
    expect(
      within(panel).getByText(
        'Secret values are masked: you can only see whether it was sent and when it was delivered.'
      )
    ).toBeInTheDocument();
    expect(developer.logEntry).toHaveBeenCalledWith(1, 7012);
    expect(container.textContent).not.toContain(FULL_ADDRESS);
    expect(container.textContent).not.toContain('482913');
  });

  it('older button requests next_cursor', async () => {
    state.search = 'org=1&tab=logs&mode=live';
    developer.log
      .mockResolvedValueOnce(page([entry()], { next_cursor: '7012' }))
      .mockResolvedValueOnce(page([entry({ id: 7001, idempotency_key: 'older-key' })]));
    renderConsole();
    fireEvent.click(await screen.findByRole('button', { name: 'Older' }));
    expect(await screen.findByText('older-key')).toBeInTheDocument();
    expect(developer.log).toHaveBeenLastCalledWith(
      1,
      { mode: 'live' },
      { cursor: '7012', perPage: 50 }
    );
    expect(screen.queryByRole('button', { name: 'Older' })).toBeNull();
  });

  it('templates tab shows the 24 h count of the selected template only', async () => {
    state.search = 'org=1&tab=templates&mode=live';
    developer.log.mockResolvedValue(page([], { total_24h: 862 }));
    renderConsole();
    expect(await screen.findByText('862 sent in the last 24 hours')).toBeInTheDocument();
    expect(developer.log).toHaveBeenCalledTimes(1);
    expect(developer.log).toHaveBeenCalledWith(
      1,
      { mode: 'live', template: 'signup-code' },
      { perPage: 1 }
    );
  });

  it('disabled instance shows the not-enabled state', async () => {
    state.domains.data = [domain({ transactional_enabled: false })];
    const view = renderConsole();
    expect(screen.getByText('Transactional mail is not enabled')).toBeInTheDocument();
    // Plain sending with an API key still works, so the empty state points there.
    expect(screen.getByRole('link', { name: 'Send with an API key' })).toHaveAttribute(
      'href',
      '/console/settings?tab=api'
    );
    expect(screen.queryByRole('tablist')).toBeNull();
    expect(developer.log).not.toHaveBeenCalled();
    view.unmount();

    // Switched off after the page loaded: the test send says so instead of a generic failure.
    state.domains.data = [domain({ kind: 'organization', domain: 'acme.example' })];
    state.search = 'org=1&tab=start&mode=live';
    developer.testSend.mockRejectedValue(
      new ApiError('off', ApiErrorCode.MAILBOX_TRANSACTIONAL_DISABLED, 503)
    );
    renderConsole();
    fireEvent.click(await screen.findByRole('button', { name: 'Send a test' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Transactional mail is not enabled on this instance.'
    );
  });
});

describe('developer console webhook feature gate', () => {
  beforeEach(() => {
    state.organizations.data = organizations();
    state.domains.data = [domain({ domain: 'acme.example', kind: 'organization' })];
    state.templates.data = [summary()];
    state.template.data = template();
    developer.log.mockReset().mockResolvedValue(page([]));
    apiKeys.list.mockReset().mockResolvedValue(keyPage([]));
  });

  it('hides the Webhooks tab and falls back to the quick start while the feature is off', async () => {
    state.webhookEnabled = false;
    state.search = 'org=1&tab=webhooks&mode=live';
    const missing = renderWithLayoutMessages(false);

    const tablist = await screen.findByRole('tablist');
    expect(within(tablist).queryByRole('tab', { name: 'Webhooks' })).toBeNull();
    expect(within(tablist).getByRole('tab', { name: 'Quick start' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(missing).toEqual([]);
  });

  it('shows the Webhooks tab with the webhook messages while the feature is on', async () => {
    state.webhookEnabled = true;
    state.search = 'org=1&tab=start&mode=live';
    expect(developerMessageNamespaces(true)).toContain('webhook');
    expect(developerMessageNamespaces(false)).not.toContain('webhook');
    const missing = renderWithLayoutMessages(true);

    const tablist = await screen.findByRole('tablist');
    expect(within(tablist).getByRole('tab', { name: 'Webhooks' })).toBeInTheDocument();
    expect(missing).toEqual([]);
  });
});

/**
 * Renders the console with exactly the namespaces the route serializes (the global ones plus the
 * developer layout's selection) and returns the message keys it failed to find.
 */
function renderWithLayoutMessages(webhookEnabled: boolean): string[] {
  const missing: string[] = [];
  const selected = selectMessageNamespaces(messages, [
    ...CLIENT_MESSAGE_NAMESPACES.global,
    ...CLIENT_MESSAGE_NAMESPACES.console,
    ...developerMessageNamespaces(webhookEnabled),
  ]);
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <NextIntlClientProvider
        locale="en-US"
        messages={selected as unknown as AbstractIntlMessages}
        onError={error => missing.push(error.message)}
      >
        <DeveloperConsole />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
  return missing;
}

function organizations() {
  const now = '2026-11-16T10:00:00Z';
  const items = [
    { id: 1, name: 'Acme', slug: 'acme', role: 'owner', created_at: now, updated_at: now },
  ];
  return {
    items,
    meta: { current_page: 1, per_page: 20, total: 1, last_page: 1, from: 1, to: 1 },
    links: { first: '', last: '', prev: null, next: null },
  };
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
    transactional_recipient_limit_per_minute: 3,
    transactional_key_limit_per_second: 20,
    simulator_domain: 'test.zmail.im',
    broadcasts_enabled: true,
    custom_domains_enabled: true,
    transactional_enabled: true,
    ...overrides,
  };
}

function summary(overrides: Record<string, unknown> = {}) {
  return {
    id: 7,
    name: 'Signup code',
    subject: '{{app|Acme}} code: {{code}}',
    variables: ['app', 'code'],
    created_at: '2026-11-16T10:00:00Z',
    updated_at: '2026-11-16T10:00:00Z',
    kind: 'transactional',
    key: 'signup-code',
    from: { address: 'no-reply@acme.example', name: 'Acme' },
    variable_definitions: [
      { name: 'app', secret: false, required: false },
      { name: 'code', secret: true, required: true },
    ],
    ...overrides,
  };
}

function template(overrides: Record<string, unknown> = {}) {
  return {
    ...summary(),
    html_body: '<p>Your {{app|Acme}} code is <b>{{code}}</b></p>',
    text_body: 'Your code is {{code}}.',
    sample_variables: { code: '482913' },
    design: null,
    ...overrides,
  };
}

function apiKey(overrides: Record<string, unknown> = {}) {
  return {
    id: 3,
    user_id: 9,
    name: 'Transactional live key',
    key_prefix: 'fastbuild_ab12cd',
    mode: 'live',
    scopes: ['mail:transactional'],
    created_at: '2026-11-16T10:00:00Z',
    updated_at: '2026-11-16T10:00:00Z',
    ...overrides,
  };
}

function keyPage(items: unknown[]) {
  return {
    items,
    meta: { current_page: 1, per_page: 100, total: items.length, last_page: 1, from: 1, to: 1 },
    links: { first: '', last: '', prev: null, next: null },
  };
}

function entry(overrides: Record<string, unknown> = {}) {
  return {
    id: 7012,
    mode: 'live',
    simulated: false,
    template: 'signup-code',
    to_masked: 'li••@example.com',
    subject: 'Acme verification code: ••••••',
    status: 'sent',
    queued_reason: null,
    failure_code: null,
    last_event: 'delivered',
    idempotency_key: 'signup-u_8812',
    created_at: '2026-11-19T14:22:08Z',
    sent_at: '2026-11-19T14:22:09Z',
    ...overrides,
  };
}

function detail(overrides: Record<string, unknown> = {}) {
  return {
    ...entry(),
    from: { address: 'no-reply@acme.example', name: 'Acme' },
    variables: { code: '••••••', app: 'Acme' },
    events: [],
    ...overrides,
  };
}

function message(overrides: Record<string, unknown> = {}) {
  return {
    ...detail({ status: 'queued', last_event: null, sent_at: null, idempotency_key: null }),
    to: FULL_ADDRESS,
    attempts: 0,
    ...overrides,
  };
}

function page(items: unknown[], meta: Record<string, unknown> = {}) {
  return { items, meta: { next_cursor: null, total_24h: items.length, ...meta } };
}

function wrap(client = queryClient) {
  return (
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <DeveloperConsole />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

let queryClient: QueryClient;

function renderConsole() {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(wrap(queryClient));
}
