import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { MemberWelcomeSetting } from '@/features/mailbox/member-welcome-schemas';
import { mockMemberWelcomeStore } from '@/features/mailbox/server/mock-member-welcome-store';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const welcome = vi.hoisted(() => ({ setting: vi.fn(), update: vi.fn(), preview: vi.fn() }));
const toast = vi.hoisted(() => ({ success: vi.fn(), warning: vi.fn(), error: vi.fn() }));

vi.mock('sonner', () => ({ toast }));
vi.mock('@/features/mailbox/services/member-welcome-service', () => ({
  memberWelcomeService: welcome,
}));

const { MemberWelcomePanel } =
  await import('@/features/mailbox/components/admin/member-welcome-panel');

describe('member welcome mock store', () => {
  beforeEach(() => mockMemberWelcomeStore.reset());

  it('starts on, in Chinese, with the standard text', () => {
    const setting = mockMemberWelcomeStore.setting(1);
    expect(setting).toMatchObject({ enabled: true, language: 'zh-Hans', customized: false });
    expect(setting.subject).toBe(setting.defaults['zh-Hans'].subject);
  });

  it('welcomes a mailbox once and sends nothing while turned off', () => {
    expect(mockMemberWelcomeStore.welcome(1, 11)).toBe('queued');
    expect(mockMemberWelcomeStore.welcome(1, 11)).toBe('already_sent');
    const standard = mockMemberWelcomeStore.setting(2).defaults['en-US'];
    const saved = mockMemberWelcomeStore.update(2, {
      enabled: false,
      language: 'en-US',
      ...standard,
    });
    expect(saved.ok && saved.value).toMatchObject({ enabled: false, customized: false });
    expect(mockMemberWelcomeStore.welcome(2, 12)).toBe('disabled');
  });

  it('refuses unknown variables and previews without passwords', () => {
    expect(
      mockMemberWelcomeStore.update(1, {
        enabled: true,
        language: 'en-US',
        subject: 'Hi {{password}}',
        body: '',
      })
    ).toEqual({ ok: false, fields: { subject: ['uses the unknown variable password'] } });
    const preview = mockMemberWelcomeStore.preview('Acme', {
      language: 'en-US',
      subject: 'Welcome to {{organization_name}}',
      body: 'Hi {{member_name}}, sign in at {{sign_in_url}}',
    });
    expect(preview.ok && preview.value.subject).toBe('Welcome to Acme');
    expect(preview.ok && preview.value.text).toContain('Hi Alex Chen, sign in at');
    expect(preview.ok && preview.value.text).toContain('Outgoing mail (SMTP)');
    expect(preview.ok && preview.value.text.toLowerCase()).not.toContain('password:');
  });
});

function setting(extra: Partial<MemberWelcomeSetting> = {}): MemberWelcomeSetting {
  const defaults = mockMemberWelcomeStore.setting(1).defaults;
  return {
    enabled: true,
    language: 'en-US',
    subject: defaults['en-US'].subject,
    body: defaults['en-US'].body,
    customized: false,
    variables: ['member_name', 'mailbox_address', 'organization_name', 'sign_in_url'],
    defaults,
    updated_at: null,
    ...extra,
  };
}

function renderPanel() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <MemberWelcomePanel organizationId={7} />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

describe('member welcome panel', () => {
  beforeEach(() => {
    for (const fn of Object.values(welcome)) fn.mockReset();
    for (const fn of Object.values(toast)) fn.mockReset();
  });

  it('turns the welcome email off and saves edited copy', async () => {
    welcome.setting.mockResolvedValue(setting());
    welcome.update.mockImplementation(async (_id: number, input: Partial<MemberWelcomeSetting>) =>
      setting({ ...input, customized: true, updated_at: '2026-11-23T10:00:00Z' })
    );
    renderPanel();

    const toggle = await screen.findByRole('switch', {
      name: 'Send a welcome email to new members',
    });
    fireEvent.click(toggle);
    fireEvent.change(screen.getByLabelText('Subject'), { target: { value: 'Hello' } });
    fireEvent.click(screen.getByRole('button', { name: 'Member name' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Welcome email settings saved'));
    expect(welcome.update).toHaveBeenCalledWith(7, {
      enabled: false,
      language: 'en-US',
      subject: 'Hello',
      body: expect.stringContaining('{{member_name}}'),
    });
  });

  it('previews the copy as typed and shows field errors', async () => {
    welcome.setting.mockResolvedValue(setting());
    welcome.preview.mockResolvedValueOnce({
      subject: 'Welcome to Acme on Zmail',
      text: 'Hi Alex Chen,\n\nSet up a mail app',
      html: '<p>Hi</p>',
    });
    welcome.update.mockRejectedValueOnce(
      new ApiError('invalid', ApiErrorCode.COMMON_VALIDATION_FAILED, 422, undefined, {
        body: ['uses the unknown variable password'],
      })
    );
    renderPanel();

    fireEvent.click(await screen.findByRole('button', { name: 'Preview' }));
    const region = await screen.findByRole('region', { name: 'Preview with sample details' });
    expect(region).toHaveTextContent('Welcome to Acme on Zmail');
    expect(region).toHaveTextContent('Set up a mail app');

    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(
      await screen.findByText(
        'Fix the highlighted text. Only the details offered above can be inserted.'
      )
    ).toBeInTheDocument();
    expect(
      screen.getByText('Use only the details offered above, and keep it within the length limit.')
    ).toBeInTheDocument();
  });

  it('offers a retry when the setting cannot be read', async () => {
    welcome.setting.mockRejectedValueOnce(new Error('down')).mockResolvedValueOnce(setting());
    renderPanel();
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }));
    expect(
      await screen.findByRole('switch', { name: 'Send a welcome email to new members' })
    ).toBeChecked();
  });
});
