import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type {
  MemberWelcomeMail,
  MemberWelcomeSetting,
} from '@/features/mailbox/member-welcome-schemas';
import { mockMemberWelcomeStore } from '@/features/mailbox/server/mock-member-welcome-store';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { messages } from '@/i18n/modules';

const welcome = vi.hoisted(() => ({
  setting: vi.fn(),
  update: vi.fn(),
  preview: vi.fn(),
  list: vi.fn(),
  resend: vi.fn(),
}));
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

  it('resends at most once a minute and five times a day', () => {
    const candidate = {
      mailbox_id: 21,
      address: 'ann@acme.example',
      display_name: 'Ann',
      owner_user_id: 5,
      invitation_pending: false,
    };
    const start = Date.now();
    expect(mockMemberWelcomeStore.list(3, [candidate])[0]).toMatchObject({
      status: 'not_sent',
      resend_count: 0,
      queued_at: null,
    });
    const first = mockMemberWelcomeStore.resend(3, candidate, start);
    expect(first.ok && first.value).toMatchObject({ status: 'sent', resend_count: 1 });
    expect(mockMemberWelcomeStore.resend(3, candidate, start + 30_000)).toEqual({
      ok: false,
      error: 'limited',
      retryAfterSeconds: 30,
    });
    for (let n = 2; n <= 5; n += 1) {
      expect(mockMemberWelcomeStore.resend(3, candidate, start + n * 61_000).ok).toBe(true);
    }
    const sixth = mockMemberWelcomeStore.resend(3, candidate, start + 7 * 61_000);
    expect(sixth.ok).toBe(false);
    expect(mockMemberWelcomeStore.resend(3, candidate, start + 24 * 3_600_000).ok).toBe(true);
    mockMemberWelcomeStore.update(3, { enabled: false, language: 'en-US', subject: '', body: '' });
    expect(mockMemberWelcomeStore.resend(3, candidate, start + 25 * 3_600_000)).toEqual({
      ok: false,
      error: 'disabled',
    });
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

function mail(extra: Partial<MemberWelcomeMail> = {}): MemberWelcomeMail {
  return {
    mailbox_id: 41,
    address: 'ann@acme.example',
    display_name: 'Ann',
    owner_user_id: 5,
    invitation_pending: false,
    status: 'sent',
    failure_code: null,
    queued_at: '2026-11-23T08:00:00Z',
    sent_at: '2026-11-23T08:00:02Z',
    resend_count: 0,
    ...extra,
  };
}

describe('member welcome panel', () => {
  beforeEach(() => {
    for (const fn of Object.values(welcome)) fn.mockReset();
    for (const fn of Object.values(toast)) fn.mockReset();
    welcome.list.mockResolvedValue([]);
  });

  it('lists each mailbox with its latest welcome and resends one', async () => {
    welcome.setting.mockResolvedValue(setting());
    welcome.list.mockResolvedValue([
      mail({
        status: 'failed',
        failure_code: 'transport.permanent_bounce',
        sent_at: null,
        resend_count: 2,
      }),
      mail({
        mailbox_id: 42,
        address: 'bo@acme.example',
        display_name: '',
        owner_user_id: null,
        invitation_pending: true,
        status: 'not_sent',
        queued_at: null,
        sent_at: null,
      }),
    ]);
    welcome.resend.mockResolvedValueOnce(
      mail({ status: 'queued', sent_at: null, resend_count: 3 })
    );
    renderPanel();

    const list = await screen.findByRole('region', { name: 'Who got a welcome email' });
    expect(await within(list).findByText('ann@acme.example')).toBeInTheDocument();
    expect(within(list).getByText('Failed')).toBeInTheDocument();
    expect(within(list).getByText(/The receiving server rejected it\./)).toBeInTheDocument();
    expect(within(list).getByText('Ann · Resent 2 times')).toBeInTheDocument();
    expect(within(list).getByText('Not sent')).toBeInTheDocument();
    expect(within(list).getByText('Invited, not joined yet')).toBeInTheDocument();
    expect(
      within(list).getByRole('button', { name: 'Resend the welcome email for bo@acme.example' })
    ).toHaveTextContent('Send welcome email');

    fireEvent.click(
      within(list).getByRole('button', { name: 'Resend the welcome email for ann@acme.example' })
    );
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Welcome email for ann@acme.example sent again.')
    );
    expect(welcome.resend).toHaveBeenCalledWith(7, 41);
  });

  it('says why a resend was refused', async () => {
    welcome.setting.mockResolvedValue(setting());
    welcome.list.mockResolvedValue([mail()]);
    welcome.resend.mockRejectedValueOnce(
      new ApiError('limited', ApiErrorCode.MAILBOX_WELCOME_RESEND_LIMITED, 429)
    );
    renderPanel();
    fireEvent.click(
      await screen.findByRole('button', { name: 'Resend the welcome email for ann@acme.example' })
    );
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        'This welcome email was sent moments ago or already 5 times today. Wait a little and try again.'
      )
    );
  });

  it('warns when the recipient is suppressed', async () => {
    welcome.setting.mockResolvedValue(setting());
    welcome.list.mockResolvedValue([mail()]);
    welcome.resend.mockResolvedValueOnce(mail({ status: 'suppressed', sent_at: null }));
    renderPanel();
    fireEvent.click(
      await screen.findByRole('button', { name: 'Resend the welcome email for ann@acme.example' })
    );
    await waitFor(() => expect(toast.warning).toHaveBeenCalled());
    expect(toast.success).not.toHaveBeenCalled();
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
