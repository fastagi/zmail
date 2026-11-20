import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { DeveloperConsole } from '@/features/developer';
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
  return {
    search: '',
    replace: vi.fn(),
    organizations: query(),
    domains: query(),
    templates: query(),
    template: query(),
    templateKinds: [] as unknown[],
    save: { isPending: false, mutateAsync: vi.fn() },
  };
});

vi.mock('sonner', () => ({ toast }));
// The webhook feature is on, so the console shows all five tabs.
vi.mock('@/config/features', async importOriginal => {
  const original = await importOriginal<typeof import('@/config/features')>();
  return {
    ...original,
    isWebFeatureEnabled: (feature: Parameters<typeof original.isWebFeatureEnabled>[0]) =>
      feature === 'webhook' || original.isWebFeatureEnabled(feature),
  };
});
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: state.replace }),
  usePathname: () => '/console/developers',
  useSearchParams: () => new URLSearchParams(state.search),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => state.organizations,
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useMailboxDomains: () => state.domains,
}));
vi.mock('@/features/mailbox/hooks/use-mail-templates', () => ({
  useMailTemplates: (_organizationId: number, kind?: string) => {
    state.templateKinds.push(kind);
    return state.templates;
  },
  useMailTemplate: () => state.template,
  useSaveMailTemplate: () => state.save,
}));

// The quick start and the templates tab read the send log and the caller's keys; both stay empty here.
vi.mock('@/features/developer/services/developer-service', () => ({
  developerService: {
    log: vi.fn(async () => ({ items: [], meta: { next_cursor: null, total_24h: 0 } })),
    logEntry: vi.fn(),
    testSend: vi.fn(),
  },
}));
vi.mock('@/features/api-key/services/api-key-service', () => ({
  apiKeyService: {
    list: vi.fn(async () => ({
      items: [],
      meta: { current_page: 1, per_page: 100, total: 0, last_page: 1, from: 0, to: 0 },
      links: { first: '', last: '', prev: null, next: null },
    })),
    create: vi.fn(),
    revoke: vi.fn(),
  },
}));

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

describe('developer console', () => {
  beforeEach(() => {
    toast.success.mockReset();
    state.search = 'org=1';
    state.replace.mockReset();
    state.organizations.data = organizations('owner');
    state.domains.data = [
      domain({ domain: 'mail.example.com', kind: 'shared' }),
      domain({ domain: 'acme.example', kind: 'organization' }),
    ];
    state.templates.data = [summary()];
    state.template.data = template();
    state.templateKinds = [];
    state.save.isPending = false;
    state.save.mutateAsync.mockReset();
  });

  it('developer console keeps tab and mode in the URL', () => {
    state.search = 'org=1&tab=bogus&mode=nope';
    const view = renderConsole();
    const tablist = screen.getByRole('tablist');
    expect(
      within(tablist)
        .getAllByRole('tab')
        .map(tab => tab.textContent)
    ).toEqual(['Quick start', 'Templates', 'Send log', 'Limits & protection', 'Webhooks']);
    // Unknown values fall back to the first tab and live mode.
    expect(screen.getByRole('tab', { name: 'Quick start' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByRole('button', { name: 'Live' })).toHaveAttribute('aria-pressed', 'true');

    selectTab('Templates');
    expect(state.replace).toHaveBeenLastCalledWith(
      '/console/developers?org=1&tab=templates&mode=live',
      { scroll: false }
    );
    fireEvent.click(screen.getByRole('button', { name: 'Test' }));
    expect(state.replace).toHaveBeenLastCalledWith(
      '/console/developers?org=1&tab=start&mode=test',
      { scroll: false }
    );

    // A reload of the shared URL shows the same tab and mode.
    state.search = 'org=1&tab=templates&mode=test';
    view.rerender(wrap());
    expect(screen.getByRole('tab', { name: 'Templates' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('button', { name: 'Test' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('shows the not-enabled state when the instance has transactional mail off', () => {
    state.domains.data = [domain({ transactional_enabled: false })];
    renderConsole();
    expect(screen.getByText('Transactional mail is not enabled')).toBeInTheDocument();
    expect(screen.queryByRole('tablist')).toBeNull();
  });

  it('member sees transactional templates read-only', () => {
    state.organizations.data = organizations('member');
    state.search = 'org=1&tab=templates';
    const { container } = renderConsole();

    expect(state.templateKinds.every(kind => kind === 'transactional')).toBe(true);
    expect(state.templateKinds.length).toBeGreaterThan(0);
    const list = screen.getByRole('list', { name: 'Transactional templates' });
    expect(within(list).getByText('Signup code')).toBeInTheDocument();
    expect(within(list).getByText('signup-code')).toBeInTheDocument();
    expect(within(list).getByText('Contains secrets')).toBeInTheDocument();

    expect(screen.getByText('{{app|Acme}} code: {{code}}')).toBeInTheDocument();
    expect(screen.getByText('Your code is {{code}}.')).toBeInTheDocument();
    expect(screen.getByText('{{code}} · secret')).toBeInTheDocument();
    expect(screen.getByText('{{app}}')).toBeInTheDocument();
    // The preview renders the sample values in the sandboxed frame.
    const frame = container.querySelector('iframe');
    expect(frame?.getAttribute('sandbox')).toBe('');
    expect(frame?.getAttribute('srcdoc')).toContain('Your Acme code is <b>482913</b>');

    expect(screen.queryByRole('button', { name: 'New template' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull();
    expect(
      screen.getByText('Only owners and admins can change transactional templates.')
    ).toBeInTheDocument();
  });

  it('admin edits a transactional template and secret fallback is disabled', async () => {
    state.search = 'org=1&tab=templates';
    state.save.mutateAsync.mockResolvedValue(template({ name: 'Sign-up code' }));
    renderConsole();
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const dialog = screen.getByRole('dialog');

    expect(within(dialog).getByLabelText('Name')).toHaveValue('Signup code');
    expect(within(dialog).getByLabelText('Key')).toHaveValue('signup-code');
    expect(within(dialog).getByLabelText('Sender local part')).toHaveValue('no-reply');
    expect(within(dialog).getByLabelText('Sender domain')).toHaveValue('acme.example');
    // A secret is always required and never has a fallback.
    expect(within(dialog).getByLabelText('Fallback for code')).toBeDisabled();
    expect(within(dialog).getByRole('checkbox', { name: 'Secret: code' })).toBeChecked();
    expect(within(dialog).getByRole('checkbox', { name: 'Required: code' })).toBeDisabled();
    expect(within(dialog).getByLabelText('Fallback for app')).toBeEnabled();
    expect(within(dialog).getByLabelText('Fallback for app')).toHaveValue('Acme');

    // Marking app secret drops its fallback from every placeholder.
    fireEvent.click(within(dialog).getByRole('checkbox', { name: 'Secret: app' }));
    expect(within(dialog).getByLabelText('Fallback for app')).toBeDisabled();
    expect(within(dialog).getByLabelText('Subject')).toHaveValue('{{app}} code: {{code}}');

    fireEvent.change(within(dialog).getByLabelText('Name'), {
      target: { value: 'Sign-up code' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(state.save.mutateAsync).toHaveBeenCalled());
    const [{ input, templateId }] = state.save.mutateAsync.mock.calls[0] as [
      { input: Record<string, unknown>; templateId?: number },
    ];
    expect(templateId).toBe(7);
    expect(input).toMatchObject({
      name: 'Sign-up code',
      kind: 'transactional',
      key: 'signup-code',
      from_address: 'no-reply@acme.example',
      from_name: 'Acme',
      subject: '{{app}} code: {{code}}',
      variable_definitions: [
        { name: 'app', secret: true, required: true },
        { name: 'code', secret: true, required: true },
      ],
    });
    expect(toast.success).toHaveBeenCalledWith('Template saved');
  });

  it('key taken shows inline error', async () => {
    state.search = 'org=1&tab=templates';
    state.save.mutateAsync.mockRejectedValue(
      new ApiError('taken', ApiErrorCode.MAILBOX_TEMPLATE_KEY_TAKEN, 409)
    );
    renderConsole();
    fireEvent.click(screen.getByRole('button', { name: 'New template' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'Welcome' } });
    fireEvent.change(within(dialog).getByLabelText('Key'), { target: { value: 'signup-code' } });
    fireEvent.change(within(dialog).getByLabelText('Sender local part'), {
      target: { value: 'hello' },
    });
    fireEvent.change(within(dialog).getByLabelText('Subject'), {
      target: { value: 'Welcome {{name|friend}}' },
    });
    fireEvent.change(within(dialog).getByLabelText('Plain text body'), {
      target: { value: 'Hi {{name|friend}}' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    const key = within(dialog).getByLabelText('Key');
    await waitFor(() => expect(key).toHaveAttribute('aria-invalid', 'true'));
    expect(within(dialog).getByText('Another template already uses this key.')).toBeInTheDocument();
    const [{ input, templateId }] = state.save.mutateAsync.mock.calls[0] as [
      { input: Record<string, unknown>; templateId?: number },
    ];
    expect(templateId).toBeUndefined();
    expect(input).toMatchObject({
      kind: 'transactional',
      from_address: 'hello@acme.example',
      variable_definitions: [{ name: 'name', secret: false, required: false }],
    });
  });

  it('shows an invalid template as a form error', async () => {
    state.search = 'org=1&tab=templates';
    state.save.mutateAsync.mockRejectedValue(
      new ApiError('invalid', ApiErrorCode.MAILBOX_TEMPLATE_INVALID, 422)
    );
    renderConsole();
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      messages.mailbox.errors.templateInvalid
    );
  });
});

function selectTab(name: string) {
  fireEvent.mouseDown(screen.getByRole('tab', { name }), { button: 0 });
}

function organizations(role: string) {
  const now = '2026-11-16T10:00:00Z';
  const items = [{ id: 1, name: 'Acme', slug: 'acme', role, created_at: now, updated_at: now }];
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

function wrap() {
  return (
    <QueryClientProvider client={queryClient}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <DeveloperConsole />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

function renderConsole() {
  return render(wrap());
}
