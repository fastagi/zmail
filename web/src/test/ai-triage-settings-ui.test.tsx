import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { AiTriageAdmin } from '@/features/mailbox/components/admin/ai-triage-admin';
import {
  MailboxTriageSwitch,
  mayToggleMailboxTriage,
} from '@/features/mailbox/components/mailbox-triage-switch';
import type { MailTriageSetting } from '@/features/mailbox/triage-schemas';
import type { Mailbox } from '@/features/mailbox/types';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

/**
 * The AI triage settings: the organization page in the admin area and the mailbox switch of the
 * access panel, with the real hooks and query cache; only the HTTP service and the organization
 * list are faked.
 */
const service = vi.hoisted(() => ({
  setting: vi.fn(),
  saveSetting: vi.fn(),
  setMailboxTriage: vi.fn(),
  overrideTriage: vi.fn(),
  replyDraft: vi.fn(),
}));
const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));

vi.mock('sonner', () => ({ toast }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => '/console/admin/ai-triage',
  useSearchParams: () => new URLSearchParams('org=1'),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => {
  const now = '2026-11-16T10:00:00Z';
  return {
    useOrganizations: () => ({
      data: {
        items: [
          { id: 1, name: 'Acme', slug: 'acme', role: 'owner', created_at: now, updated_at: now },
        ],
        meta: { current_page: 1, per_page: 25, total: 1, last_page: 1, from: 1, to: 1 },
        links: { first: '', last: '', prev: null, next: null },
      },
      isPending: false,
      error: null,
      refetch: vi.fn(),
    }),
  };
});
vi.mock('@/features/mailbox/services/triage-service', () => ({ triageService: service }));

function setting(overrides: Partial<MailTriageSetting> = {}): MailTriageSetting {
  return {
    available: true,
    enabled: false,
    daily_message_cap: 500,
    draft_enabled: false,
    instance_daily_cap: 1000,
    updated_at: null,
    ...overrides,
  };
}

const noAccess = {
  view: false,
  send: false,
  manage_members: false,
  administer: false,
  see_history: false,
  organize: false,
};

function mailbox(overrides: Partial<Mailbox> = {}): Mailbox {
  return {
    id: 7,
    address: 'support@acme.test',
    display_name: 'Support',
    type: 'shared',
    owner_user_id: null,
    awaiting_handover: false,
    sealed_before: null,
    webhooks_enabled: false,
    ai_triage_enabled: false,
    access: { ...noAccess, view: true, send: true, manage_members: true, see_history: true },
    unread_count: 0,
    created_at: '2026-11-16T10:00:00Z',
    role_id: null,
    ...overrides,
  } as Mailbox;
}

function renderWithProviders(node: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        {node}
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

beforeEach(() => {
  for (const fn of Object.values(service)) fn.mockReset();
  toast.success.mockReset();
  toast.error.mockReset();
});

describe('organization AI triage page', () => {
  it('says the feature is unavailable when the instance has no AI provider', async () => {
    service.setting.mockResolvedValue(setting({ available: false, instance_daily_cap: 0 }));
    renderWithProviders(<AiTriageAdmin />);

    expect(
      await screen.findByText(/no AI provider is configured for this Zmail instance/i)
    ).toBeInTheDocument();
    expect(screen.queryByRole('switch')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
    expect(service.setting).toHaveBeenCalledWith(1);
  });

  it('shows the switches off by default with the data notice', async () => {
    service.setting.mockResolvedValue(setting());
    renderWithProviders(<AiTriageAdmin />);

    const triage = await screen.findByRole('switch', { name: 'Turn on AI triage' });
    expect(triage).not.toBeChecked();
    expect(screen.getByRole('switch', { name: 'Reply drafts' })).not.toBeChecked();
    expect(screen.getByRole('spinbutton', { name: 'Daily message cap' })).toHaveValue(500);
    expect(screen.getByRole('note')).toHaveTextContent(/is sent to the AI provider/i);
    expect(screen.getByText(/never been saved/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
  });

  it('shows the saved switches on', async () => {
    service.setting.mockResolvedValue(
      setting({
        enabled: true,
        draft_enabled: true,
        daily_message_cap: 200,
        updated_at: '2026-11-18T08:00:00Z',
      })
    );
    renderWithProviders(<AiTriageAdmin />);

    expect(await screen.findByRole('switch', { name: 'Turn on AI triage' })).toBeChecked();
    expect(screen.getByRole('switch', { name: 'Reply drafts' })).toBeChecked();
    expect(screen.getByRole('spinbutton', { name: 'Daily message cap' })).toHaveValue(200);
    expect(screen.queryByText(/never been saved/i)).not.toBeInTheDocument();
  });

  it('saves every field and reports success', async () => {
    service.setting.mockResolvedValue(setting());
    service.saveSetting.mockImplementation(async (_organizationId: number, input: object) =>
      setting({ ...input, updated_at: '2026-11-20T08:00:00Z' })
    );
    renderWithProviders(<AiTriageAdmin />);

    fireEvent.click(await screen.findByRole('switch', { name: 'Turn on AI triage' }));
    fireEvent.click(screen.getByRole('switch', { name: 'Reply drafts' }));
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Daily message cap' }), {
      target: { value: '250' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(service.saveSetting).toHaveBeenCalledWith(1, {
        enabled: true,
        daily_message_cap: 250,
        draft_enabled: true,
      })
    );
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('AI triage settings saved'));
  });

  it('refuses a cap outside the allowed range without calling the API', async () => {
    service.setting.mockResolvedValue(setting());
    renderWithProviders(<AiTriageAdmin />);

    const cap = await screen.findByRole('spinbutton', { name: 'Daily message cap' });
    fireEvent.change(cap, { target: { value: '0' } });
    expect(screen.getByText(/whole number from 1 to 100,000/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    expect(service.saveSetting).not.toHaveBeenCalled();
  });

  it('explains a refusal because the provider went away', async () => {
    service.setting.mockResolvedValue(setting());
    service.saveSetting.mockRejectedValue(
      new ApiError('unavailable', ApiErrorCode.MAILBOX_TRIAGE_UNAVAILABLE)
    );
    renderWithProviders(<AiTriageAdmin />);

    fireEvent.click(await screen.findByRole('switch', { name: 'Turn on AI triage' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'AI triage cannot be turned on: no AI provider is configured for this Zmail instance.'
    );
  });

  it('explains a refusal for a member without the right to change it', async () => {
    service.setting.mockResolvedValue(setting());
    service.saveSetting.mockRejectedValue(new ApiError('denied', ApiErrorCode.PERMISSION_DENIED));
    renderWithProviders(<AiTriageAdmin />);

    fireEvent.click(await screen.findByRole('switch', { name: 'Turn on AI triage' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(/permission/i);
  });

  it('offers a retry when the switches cannot be loaded', async () => {
    service.setting.mockRejectedValueOnce(
      new ApiError('down', ApiErrorCode.COMMON_SERVICE_UNAVAILABLE)
    );
    service.setting.mockResolvedValue(setting());
    renderWithProviders(<AiTriageAdmin />);

    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('switch', { name: 'Turn on AI triage' })).toBeInTheDocument();
  });
});

describe('mailbox AI triage switch', () => {
  const run = vi.fn(async (change: () => Promise<unknown>) => {
    await change();
    return { ok: true as const };
  });

  beforeEach(() => {
    run.mockClear();
  });

  it('belongs to the personal owner and to shared mailbox managers only', () => {
    const owner = { ...noAccess, view: true, send: true, see_history: true, organize: true };
    expect(
      mayToggleMailboxTriage(mailbox({ type: 'personal', owner_user_id: 3, access: owner }))
    ).toBe(true);
    // An organization owner or admin sees a personal mailbox's history but is not its owner.
    expect(
      mayToggleMailboxTriage(
        mailbox({
          type: 'personal',
          owner_user_id: 3,
          access: { ...noAccess, administer: true, see_history: true },
        })
      )
    ).toBe(false);
    expect(
      mayToggleMailboxTriage(
        mailbox({ type: 'personal', owner_user_id: null, awaiting_handover: true, access: owner })
      )
    ).toBe(false);
    expect(mayToggleMailboxTriage(mailbox())).toBe(true);
    expect(
      mayToggleMailboxTriage(mailbox({ access: { ...noAccess, view: true, send: true } }))
    ).toBe(false);
  });

  it('is hidden while the organization switch is off', async () => {
    service.setting.mockResolvedValue(setting({ enabled: false }));
    renderWithProviders(<MailboxTriageSwitch organizationId={1} mailbox={mailbox()} run={run} />);

    await waitFor(() => expect(service.setting).toHaveBeenCalledWith(1));
    expect(screen.queryByRole('switch')).not.toBeInTheDocument();
  });

  it('is hidden when the instance has no AI provider', async () => {
    service.setting.mockResolvedValue(setting({ available: false, enabled: true }));
    renderWithProviders(<MailboxTriageSwitch organizationId={1} mailbox={mailbox()} run={run} />);

    await waitFor(() => expect(service.setting).toHaveBeenCalled());
    expect(screen.queryByRole('switch')).not.toBeInTheDocument();
  });

  it('is hidden and reads nothing for a caller who may not change it', () => {
    renderWithProviders(
      <MailboxTriageSwitch
        organizationId={1}
        mailbox={mailbox({ access: { ...noAccess, view: true } })}
        run={run}
      />
    );
    expect(screen.queryByRole('switch')).not.toBeInTheDocument();
    expect(service.setting).not.toHaveBeenCalled();
  });

  it('turns the mailbox on when the organization has triage on', async () => {
    service.setting.mockResolvedValue(setting({ enabled: true }));
    service.setMailboxTriage.mockResolvedValue(mailbox({ ai_triage_enabled: true }));
    renderWithProviders(<MailboxTriageSwitch organizationId={1} mailbox={mailbox()} run={run} />);

    const toggle = await screen.findByRole('switch', { name: 'AI triage for this mailbox' });
    expect(toggle).not.toBeChecked();
    expect(screen.getByText(/sent to the AI provider/i)).toBeInTheDocument();
    fireEvent.click(toggle);

    await waitFor(() => expect(service.setMailboxTriage).toHaveBeenCalledWith(1, 7, true));
    expect(run).toHaveBeenCalledWith(expect.any(Function), 'AI triage turned on for this mailbox');
  });

  it('turns a personal mailbox off for its owner', async () => {
    service.setting.mockResolvedValue(setting({ enabled: true }));
    service.setMailboxTriage.mockResolvedValue(mailbox({ ai_triage_enabled: false }));
    const personal = mailbox({
      type: 'personal',
      owner_user_id: 3,
      ai_triage_enabled: true,
      access: { ...noAccess, view: true, send: true, see_history: true, organize: true },
    });
    renderWithProviders(<MailboxTriageSwitch organizationId={1} mailbox={personal} run={run} />);

    const toggle = await screen.findByRole('switch', { name: 'AI triage for this mailbox' });
    expect(toggle).toBeChecked();
    fireEvent.click(toggle);

    await waitFor(() => expect(service.setMailboxTriage).toHaveBeenCalledWith(1, 7, false));
  });
});
