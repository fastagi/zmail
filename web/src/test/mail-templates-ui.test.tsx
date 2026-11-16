import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { MailTemplates } from '@/features/mailbox/components/mail-templates';
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
  const mutation = () => ({ isPending: false, mutateAsync: vi.fn() });
  return {
    organizations: query(),
    mailboxes: query(),
    templates: query(),
    template: query(),
    templateCalls: [] as (number | null)[],
    save: mutation(),
    remove: mutation(),
    test: mutation(),
  };
});

vi.mock('sonner', () => ({ toast }));
vi.mock('next/navigation', () => ({
  useRouter: () => ({ replace: vi.fn() }),
  usePathname: () => '/console/mail-templates',
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock('@/features/organization/hooks/use-organizations', () => ({
  useOrganizations: () => state.organizations,
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useMailboxes: () => state.mailboxes,
}));
vi.mock('@/features/mailbox/hooks/use-mail-templates', () => ({
  useMailTemplates: () => state.templates,
  useMailTemplate: (_organizationId: number, templateId: number | null) => {
    state.templateCalls.push(templateId);
    return state.template;
  },
  useSaveMailTemplate: () => state.save,
  useDeleteMailTemplate: () => state.remove,
  useSendMailTemplateTest: () => state.test,
}));

describe('mail templates console', () => {
  beforeEach(() => {
    toast.success.mockReset();
    toast.error.mockReset();
    state.organizations.data = page([
      { id: 1, name: 'Acme', slug: 'acme', role: 'owner', created_at: now(), updated_at: now() },
    ]);
    state.mailboxes.data = [
      {
        id: 2,
        address: 'hello@mail.example.com',
        display_name: '',
        unread_count: 0,
        created_at: now(),
      },
    ];
    state.templates.data = [summary()];
    state.template.data = template();
    state.templateCalls = [];
    for (const mutation of [state.save, state.remove, state.test]) {
      mutation.isPending = false;
      mutation.mutateAsync.mockReset();
    }
  });

  it('lists templates with their variables and offers to create one', () => {
    renderTemplates();
    expect(screen.getByRole('button', { name: 'Edit Welcome' })).toBeInTheDocument();
    expect(screen.getByText('Welcome, {{first_name|friend}}')).toBeInTheDocument();
    expect(screen.getByText('code')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'New template' })).toBeInTheDocument();
  });

  it('shows an empty state with a create action', () => {
    state.templates.data = [];
    renderTemplates();
    expect(screen.getByText('No templates yet')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'New template' }));
    expect(screen.getByRole('group', { name: 'Start from' })).toBeInTheDocument();
  });

  it('starts from a starter, previews it with sample values, and saves only used samples', async () => {
    state.save.mutateAsync.mockResolvedValue(template({ id: 9, name: 'Onboarding' }));
    const { container } = renderTemplates();
    fireEvent.click(screen.getByRole('button', { name: 'New template' }));
    fireEvent.click(screen.getByRole('button', { name: 'Welcome' }));

    expect(screen.getByLabelText('Subject')).toHaveValue(
      'Welcome to {{company|our team}}, {{first_name|there}}'
    );
    // The preview fills the samples and the sandboxed frame shows the rendered HTML.
    expect(screen.getByText('Welcome to Acme, Ann')).toBeInTheDocument();
    const frame = container.querySelector('iframe');
    expect(frame?.getAttribute('sandbox')).toBe('');
    expect(frame?.getAttribute('srcdoc')).toContain('Welcome, Ann!');
    expect(screen.getByLabelText('action_url')).toHaveValue('https://example.com/start');

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Onboarding' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(state.save.mutateAsync).toHaveBeenCalled());
    const [{ input, templateId }] = state.save.mutateAsync.mock.calls[0] as [
      { input: Record<string, unknown>; templateId?: number },
    ];
    expect(templateId).toBeUndefined();
    expect(input).toMatchObject({
      name: 'Onboarding',
      subject: 'Welcome to {{company|our team}}, {{first_name|there}}',
      sample_variables: {
        first_name: 'Ann',
        company: 'Acme',
        action_url: 'https://example.com/start',
      },
    });
    expect(toast.success).toHaveBeenCalledWith('Template created');
  });

  it('warns about missing values and malformed placeholders while editing', () => {
    renderTemplates();
    fireEvent.click(screen.getByRole('button', { name: 'Edit Welcome' }));
    expect(state.templateCalls).toContain(4);

    fireEvent.change(screen.getByLabelText('code'), { target: { value: '' } });
    expect(
      screen.getByText('No value and no fallback: code. A real send must give them.')
    ).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('HTML body'), {
      target: { value: '<p>{{first name}}</p>' },
    });
    expect(screen.getByText(/A placeholder is malformed/u)).toBeInTheDocument();
  });

  it('explains a name that is already taken', async () => {
    state.save.mutateAsync.mockRejectedValue(
      new ApiError('taken', ApiErrorCode.MAILBOX_TEMPLATE_NAME_TAKEN, 409)
    );
    renderTemplates();
    fireEvent.click(screen.getByRole('button', { name: 'Edit Welcome' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Other' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Another template already has this name.')).toBeInTheDocument();
    expect(state.save.mutateAsync).toHaveBeenCalledWith(expect.objectContaining({ templateId: 4 }));
  });

  it('sends a test from a mailbox to the given addresses', async () => {
    state.test.mutateAsync.mockResolvedValue({});
    renderTemplates();
    fireEvent.click(screen.getByRole('button', { name: 'Edit Welcome' }));
    fireEvent.click(screen.getByRole('button', { name: 'Send test' }));
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByLabelText('From')).toHaveValue('2');

    fireEvent.change(within(dialog).getByLabelText('To'), { target: { value: 'not an address' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send test' }));
    expect(
      await within(dialog).findByText(/Enter 1 to 50 plain email addresses/u)
    ).toBeInTheDocument();

    fireEvent.change(within(dialog).getByLabelText('To'), {
      target: { value: 'QA@example.com, dev@example.com' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send test' }));
    await waitFor(() =>
      expect(state.test.mutateAsync).toHaveBeenCalledWith({
        templateId: 4,
        input: { mailbox_id: 2, to: ['qa@example.com', 'dev@example.com'] },
      })
    );
    expect(toast.success).toHaveBeenCalledWith('Test sent');
  });

  it('deletes a template after confirmation and returns to the list', async () => {
    state.remove.mutateAsync.mockResolvedValue(undefined);
    renderTemplates();
    fireEvent.click(screen.getByRole('button', { name: 'Edit Welcome' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete template' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(state.remove.mutateAsync).toHaveBeenCalledWith(4));
    expect(toast.success).toHaveBeenCalledWith('Template deleted');
    expect(await screen.findByRole('button', { name: 'New template' })).toBeInTheDocument();
  });
});

function summary() {
  return {
    id: 4,
    name: 'Welcome',
    subject: 'Welcome, {{first_name|friend}}',
    variables: ['code', 'first_name'],
    created_at: now(),
    updated_at: now(),
  };
}

function template(overrides: Record<string, unknown> = {}) {
  return {
    ...summary(),
    html_body: '<p>Hi {{first_name|friend}}, code {{code}}</p>',
    text_body: 'Code {{code}}',
    sample_variables: { first_name: 'Ann', code: '1234' },
    ...overrides,
  };
}

function page(items: unknown[]) {
  return {
    items,
    meta: {
      current_page: 1,
      per_page: 20,
      total: items.length,
      last_page: 1,
      from: 1,
      to: items.length,
    },
    links: { first: '', last: '', prev: null, next: null },
  };
}

function now() {
  return '2026-11-16T10:00:00Z';
}

function renderTemplates() {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      <MailTemplates />
    </NextIntlClientProvider>
  );
}
