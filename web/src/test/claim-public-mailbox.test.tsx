import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { AuthStoreContext, createAuthStore } from '@/features/auth/store/auth-store';
import type { AuthUser } from '@/features/auth/types';
import { ClaimPublicMailboxCard } from '@/features/mailbox/components/claim-public-mailbox-card';
import { CopyAddressButton } from '@/features/mailbox/components/copy-address-button';
import {
  extractSuggestedLocalPart,
  publicMailboxService,
  suggestHandleFromUser,
  type PublicMailboxAvailability,
  type PublicMailboxOptions,
} from '@/features/mailbox/services/public-mailbox-service';
import {
  DEFAULT_WORKSPACE_MODE,
  useWorkspaceModeStore,
} from '@/features/mailbox/stores/workspace-mode';
import type { Mailbox } from '@/features/mailbox/types';
import { TeamSetupCard } from '@/features/organization/components/team-setup-card';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const nav = vi.hoisted(() => ({
  push: vi.fn(),
  replace: vi.fn(),
}));

const toastMock = vi.hoisted(() =>
  Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn(), dismiss: vi.fn() })
);

const orgMutationState = vi.hoisted(() => ({
  isPending: false,
  error: null as unknown,
  mutate: vi.fn(),
  mutateAsync: vi.fn(),
  reset: vi.fn(),
}));

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: nav.push, replace: nav.replace, prefetch: vi.fn() }),
  usePathname: () => '/console/mailboxes',
  useSearchParams: () => new URLSearchParams(),
}));

vi.mock('sonner', () => ({ toast: toastMock }));

vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useCreateOrganization: () => orgMutationState,
}));

function baseOptions(overrides: Partial<PublicMailboxOptions> = {}): PublicMailboxOptions {
  return {
    enabled: true,
    domains: ['zmail.im'],
    min_local_length: 4,
    limit_per_user: 1,
    claimed_count: 0,
    claimed_address: null,
    claimed_organization_id: null,
    claimed_mailbox_id: null,
    daily_send_limit_new: 10,
    daily_send_limit: 30,
    max_recipients_new: 5,
    max_recipients: 20,
    ...overrides,
  };
}

function sampleMailbox(overrides: Partial<Mailbox> = {}): Mailbox {
  return {
    id: 42,
    address: 'alice.chen@zmail.im',
    display_name: 'Alice Chen',
    type: 'personal',
    origin: 'zmail',
    owner_user_id: 7,
    awaiting_handover: false,
    sealed_before: null,
    webhooks_enabled: false,
    access: {
      view: true,
      send: true,
      manage_members: false,
      administer: false,
      see_history: true,
      organize: true,
    },
    unread_count: 0,
    created_at: '2026-11-26T00:00:00Z',
    role_id: null,
    ...overrides,
  };
}

function sampleUser(overrides: Partial<AuthUser> = {}): AuthUser {
  return {
    id: '7',
    username: 'alice',
    email: 'Alice.Chen@gmail.com',
    name: 'Alice Chen',
    ...overrides,
  } as AuthUser;
}

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
}

function renderWithProviders(
  children: React.ReactNode,
  { user = sampleUser(), queryClient = createTestQueryClient() }: { user?: AuthUser | null; queryClient?: QueryClient } = {}
) {
  const authStore = createAuthStore(
    user ? { status: 'authenticated', user } : { status: 'unauthenticated' }
  );
  return {
    queryClient,
    ...render(
      <QueryClientProvider client={queryClient}>
        <AuthStoreContext.Provider value={authStore}>
          <NextIntlClientProvider
            locale="en-US"
            messages={messages as unknown as AbstractIntlMessages}
          >
            {children}
          </NextIntlClientProvider>
        </AuthStoreContext.Provider>
      </QueryClientProvider>
    ),
  };
}

describe('public mailbox claiming UI', () => {
  beforeEach(() => {
    nav.push.mockReset();
    nav.replace.mockReset();
    toastMock.success.mockReset();
    toastMock.error.mockReset();
    orgMutationState.isPending = false;
    orgMutationState.error = null;
    orgMutationState.mutate.mockReset();
    orgMutationState.mutateAsync.mockReset();
    orgMutationState.reset.mockReset();
    useWorkspaceModeStore.getState().setMode(DEFAULT_WORKSPACE_MODE);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    useWorkspaceModeStore.getState().setMode(DEFAULT_WORKSPACE_MODE);
  });

  it('sanitizes user email prefix and extracts suggested local parts', () => {
    expect(suggestHandleFromUser({ email: 'Alice.Chen+tag@gmail.com' })).toBe('alice.chen+tag'.replace(/[^a-z0-9._-]+/gu, ''));
    expect(suggestHandleFromUser({ email: '  ..Bob_01..@example.com' })).toBe('bob_01');
    expect(suggestHandleFromUser({ email: null, username: 'dev-user' })).toBe('dev-user');
    expect(extractSuggestedLocalPart('alice.chen01@zmail.im')).toBe('alice.chen01');
    expect(extractSuggestedLocalPart('alice.mail')).toBe('alice.mail');
  });

  it('pre-fills handle from current user email and shows live availability', async () => {
    const availabilitySpy = vi
      .spyOn(publicMailboxService, 'availability')
      .mockResolvedValue({
        address: 'alice.chen@zmail.im',
        local_part: 'alice.chen',
        domain: 'zmail.im',
        status: 'available',
        suggestions: [],
      });

    renderWithProviders(
      <ClaimPublicMailboxCard options={baseOptions()} debounceMs={0} />
    );

    const input = screen.getByLabelText('Choose your address');
    expect(input).toHaveValue('alice.chen');

    expect(await screen.findByText('alice.chen@zmail.im is available')).toBeInTheDocument();
    expect(availabilitySpy).toHaveBeenCalledWith('alice.chen', 'zmail.im');
    expect(
      screen.getByText(/Free forever to receive · 10 sends\/day for new accounts \(30\/day after 7 days\)/u)
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Claim & open inbox' })).toBeEnabled();
  });

  it('shows friendly taken message and applies 1-click suggestion chip', async () => {
    const availabilitySpy = vi
      .spyOn(publicMailboxService, 'availability')
      .mockImplementation(async (localPart: string): Promise<PublicMailboxAvailability> => {
        if (localPart === 'takenname') {
          return {
            address: 'takenname@zmail.im',
            local_part: 'takenname',
            domain: 'zmail.im',
            status: 'taken',
            suggestions: ['takenname01', 'takenname.mail@zmail.im'],
          };
        }
        return {
          address: `${localPart}@zmail.im`,
          local_part: localPart,
          domain: 'zmail.im',
          status: 'available',
          suggestions: [],
        };
      });

    renderWithProviders(
      <ClaimPublicMailboxCard options={baseOptions()} initialHandle="takenname" debounceMs={0} />
    );

    expect(await screen.findByText('takenname@zmail.im is already taken')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Claim & open inbox' })).toBeDisabled();

    const suggestionChip = screen.getByRole('button', { name: 'takenname01@zmail.im' });
    fireEvent.click(suggestionChip);

    expect(screen.getByLabelText('Choose your address')).toHaveValue('takenname01');
    expect(await screen.findByText('takenname01@zmail.im is available')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Claim & open inbox' })).toBeEnabled();
    expect(availabilitySpy).toHaveBeenCalledWith('takenname01', 'zmail.im');
  });

  it('creates Personal org then claims and navigates to inbox during onboarding', async () => {
    vi.spyOn(publicMailboxService, 'availability').mockResolvedValue({
      address: 'alice.chen@zmail.im',
      local_part: 'alice.chen',
      domain: 'zmail.im',
      status: 'available',
      suggestions: [],
    });
    orgMutationState.mutateAsync.mockResolvedValue({
      id: 19,
      name: 'Personal',
      slug: 'personal',
      role: 'owner',
    });
    const claimSpy = vi
      .spyOn(publicMailboxService, 'claim')
      .mockResolvedValue(sampleMailbox());

    renderWithProviders(
      <ClaimPublicMailboxCard options={baseOptions()} debounceMs={0} />
    );

    await screen.findByText('alice.chen@zmail.im is available');
    fireEvent.click(screen.getByRole('button', { name: 'Claim & open inbox' }));

    await waitFor(() => {
      expect(orgMutationState.mutateAsync).toHaveBeenCalledWith({ name: 'Personal' });
      expect(claimSpy).toHaveBeenCalledWith(19, {
        local_part: 'alice.chen',
        domain: 'zmail.im',
        display_name: 'Alice Chen',
      });
    });

    expect(useWorkspaceModeStore.getState().mode).toBe('personal');
    expect(toastMock.success).toHaveBeenCalledWith('Claimed alice.chen@zmail.im');
    expect(nav.push).toHaveBeenCalledWith('/console/mailboxes?org=19');
  });

  it('claims directly inside an existing organization and calls onClaimed', async () => {
    vi.spyOn(publicMailboxService, 'availability').mockResolvedValue({
      address: 'alice.chen@zmail.im',
      local_part: 'alice.chen',
      domain: 'zmail.im',
      status: 'available',
      suggestions: [],
    });
    const claimed = sampleMailbox({ id: 88 });
    const claimSpy = vi.spyOn(publicMailboxService, 'claim').mockResolvedValue(claimed);
    const onClaimed = vi.fn();

    renderWithProviders(
      <ClaimPublicMailboxCard
        options={baseOptions()}
        organizationId={12}
        debounceMs={0}
        onClaimed={onClaimed}
      />
    );

    await screen.findByText('alice.chen@zmail.im is available');
    fireEvent.click(screen.getByRole('button', { name: 'Claim free address' }));

    await waitFor(() => {
      expect(claimSpy).toHaveBeenCalledWith(12, {
        local_part: 'alice.chen',
        domain: 'zmail.im',
        display_name: 'Alice Chen',
      });
    });
    expect(orgMutationState.mutateAsync).not.toHaveBeenCalled();
    expect(onClaimed).toHaveBeenCalledWith(claimed, 12);
    expect(toastMock.success).toHaveBeenCalledWith('Claimed alice.chen@zmail.im');
  });

  it('shows localized error when claim fails with address taken', async () => {
    vi.spyOn(publicMailboxService, 'availability').mockResolvedValue({
      address: 'alice.chen@zmail.im',
      local_part: 'alice.chen',
      domain: 'zmail.im',
      status: 'available',
      suggestions: [],
    });
    vi.spyOn(publicMailboxService, 'claim').mockRejectedValue(
      new ApiError('Address taken', ApiErrorCode.MAILBOX_ADDRESS_TAKEN)
    );

    renderWithProviders(
      <ClaimPublicMailboxCard options={baseOptions()} organizationId={12} debounceMs={0} />
    );

    await screen.findByText('alice.chen@zmail.im is available');
    fireEvent.click(screen.getByRole('button', { name: 'Claim free address' }));

    expect(
      await screen.findByText('That address was just taken. Pick another name or suggestion.')
    ).toBeInTheDocument();
  });

  it('renders already-claimed state with copy address and open inbox button', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });

    renderWithProviders(
      <ClaimPublicMailboxCard
        options={baseOptions({
          claimed_count: 1,
          claimed_address: 'alice.chen@zmail.im',
          claimed_organization_id: 23,
          claimed_mailbox_id: 42,
        })}
      />
    );

    expect(screen.getByText('You already claimed alice.chen@zmail.im')).toBeInTheDocument();
    expect(screen.queryByLabelText('Choose your address')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Copy alice.chen@zmail.im' }));
    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith('alice.chen@zmail.im');
      expect(toastMock.success).toHaveBeenCalledWith('Copied alice.chen@zmail.im');
    });

    fireEvent.click(screen.getByRole('button', { name: 'Open inbox' }));
    expect(nav.push).toHaveBeenCalledWith('/console/mailboxes?org=23');
  });

  it('integrates ClaimPublicMailboxCard inside TeamSetupCard when options are enabled', async () => {
    vi.spyOn(publicMailboxService, 'options').mockResolvedValue(baseOptions());
    vi.spyOn(publicMailboxService, 'availability').mockResolvedValue({
      address: 'alice.chen@zmail.im',
      local_part: 'alice.chen',
      domain: 'zmail.im',
      status: 'available',
      suggestions: [],
    });

    renderWithProviders(<TeamSetupCard />);

    expect(await screen.findByText('Claim your free @zmail.im email')).toBeInTheDocument();
    expect(screen.getByLabelText('Team name')).toBeInTheDocument();
  });

  it('copies an address with feedback using CopyAddressButton', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });

    render(
      <CopyAddressButton
        address="alice@zmail.im"
        label="Copy alice@zmail.im"
        copiedLabel="Copied alice@zmail.im"
        failedLabel="Failed to copy"
      />
    );

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Copy alice@zmail.im' }));
    });
    expect(writeText).toHaveBeenCalledWith('alice@zmail.im');
    expect(toastMock.success).toHaveBeenCalledWith('Copied alice@zmail.im');
  });
});
