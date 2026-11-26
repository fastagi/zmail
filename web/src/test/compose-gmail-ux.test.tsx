import { useState } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ComposeDialog, emptyDraft } from '@/features/mailbox/components/compose-dialog';
import { RecipientChipsInput } from '@/features/mailbox/components/recipient-chips-input';
import { setUndoSendDelayMs } from '@/features/mailbox/components/undo-send';
import { messages } from '@/i18n/modules';

const state = vi.hoisted(() => ({
  send: {
    isPending: false,
    mutateAsync: vi.fn(),
  },
  saveDraft: {
    isPending: false,
    mutateAsync: vi.fn(),
  },
  deleteDraft: {
    isPending: false,
    mutateAsync: vi.fn(),
  },
  toast: Object.assign(vi.fn(), {
    success: vi.fn(),
    error: vi.fn(),
  }),
}));

vi.mock('sonner', () => ({ toast: state.toast }));
vi.mock('@/features/mailbox/components/mail-organization', () => ({
  useMailOrganizationId: () => 1,
}));
vi.mock('@/features/mailbox/hooks/use-mailbox', () => ({
  useSendMailMessage: () => state.send,
}));
vi.mock('@/features/mailbox/hooks/use-mail-organization', () => ({
  useChosenMemberFeatures: () => ({ templates: false }),
}));
vi.mock('@/features/mailbox/hooks/use-mail-templates', () => ({
  useMailTemplates: () => ({ data: [] }),
}));
vi.mock('@/features/mailbox/hooks/use-mail-signature', () => ({
  useMailSignature: () => ({ data: undefined }),
}));
vi.mock('@/features/mailbox/hooks/use-mail-drafts', () => ({
  useSaveMailDraft: () => state.saveDraft,
  useDeleteMailDraft: () => state.deleteDraft,
}));

function renderWithProviders(ui: React.ReactNode) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        {ui}
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
}

describe('Gmail docked floating composer & recipient chips UX', () => {
  beforeEach(() => {
    window.localStorage.clear();
    setUndoSendDelayMs(0);
    state.send.isPending = false;
    state.send.mutateAsync.mockReset();
    state.send.mutateAsync.mockResolvedValue({ id: 10, queued_reason: null });
    state.saveDraft.mutateAsync.mockReset();
    state.saveDraft.mutateAsync.mockResolvedValue({ id: 99 });
    state.deleteDraft.mutateAsync.mockReset();
    state.deleteDraft.mutateAsync.mockResolvedValue(undefined);
    state.toast.mockReset();
    state.toast.success.mockReset();
    state.toast.error.mockReset();
  });

  it('defaults to non-blocking bottom-right docked mode, minimizes/restores preserving draft & attachments, and toggles expanded modal', () => {
    const onClose = vi.fn();
    renderWithProviders(
      <ComposeDialog
        mailbox={{ id: 1, address: 'hello@mail.example.com' }}
        draft={emptyDraft}
        onClose={onClose}
      />
    );

    const dialog = screen.getByRole('dialog', { name: 'New message' });
    expect(dialog).toHaveAttribute('data-compose-mode', 'docked');
    expect(dialog).toHaveAttribute('data-state', 'open');
    expect(dialog.className).toContain('right-5');
    expect(dialog.className).toContain('bottom-0');
    expect(dialog.className).toContain('w-[560px]');
    expect(document.querySelector('[data-compose-backdrop="true"]')).toBeNull();
    expect(within(dialog).getByRole('heading', { name: 'New message' })).toHaveTextContent(
      'New message'
    );

    // Fill recipients, subject, body, and an attachment.
    const to = within(dialog).getByLabelText('To');
    fireEvent.change(to, { target: { value: 'alice@example.com' } });
    fireEvent.keyDown(to, { key: 'Enter' });
    fireEvent.change(within(dialog).getByLabelText('Subject'), {
      target: { value: 'Q4 Launch Notes' },
    });
    fireEvent.change(within(dialog).getByLabelText('Message'), {
      target: { value: 'Draft body stays intact.' },
    });
    const fileInput = dialog.querySelector<HTMLInputElement>('input[type="file"]');
    expect(fileInput).not.toBeNull();
    fireEvent.change(fileInput!, {
      target: { files: [new File(['hello'], 'brief.txt', { type: 'text/plain' })] },
    });

    // Title bar updates live to show the typed subject.
    expect(within(dialog).getByRole('heading', { name: 'New message' })).toHaveTextContent(
      'Q4 Launch Notes'
    );

    // Minimize collapses into compact bottom-right bar while preserving all state.
    fireEvent.click(within(dialog).getByRole('button', { name: 'Minimize' }));
    expect(dialog).toHaveAttribute('data-compose-mode', 'minimized');
    expect(dialog).toHaveAttribute('data-state', 'minimized');
    expect(dialog.className).toContain('w-72');
    expect(dialog.className).toContain('h-11');
    expect(within(dialog).getByRole('button', { name: 'Restore' })).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Close' })).toBeInTheDocument();
    expect(dialog.querySelector('form')).toHaveClass('hidden');

    // Clicking the minimized header restores immediately with chips, body, and attachment intact.
    const header = dialog.querySelector('[data-compose-header="true"]')!;
    fireEvent.click(header);
    expect(dialog).toHaveAttribute('data-compose-mode', 'docked');
    expect(dialog.querySelector('form')).not.toHaveClass('hidden');
    expect(within(dialog).getByText('alice@example.com')).toBeInTheDocument();
    expect(within(dialog).getByLabelText('Subject')).toHaveValue('Q4 Launch Notes');
    expect(within(dialog).getByLabelText('Message')).toHaveValue('Draft body stays intact.');
    expect(within(dialog).getByText('brief.txt')).toBeInTheDocument();

    // Expand toggles into centered large modal with subtle backdrop, then docks back.
    fireEvent.click(within(dialog).getByRole('button', { name: 'Full screen' }));
    expect(dialog).toHaveAttribute('data-compose-mode', 'expanded');
    expect(dialog.className).toContain('max-w-3xl');
    expect(dialog.className).toContain('h-[80vh]');
    expect(document.querySelector('[data-compose-backdrop="true"]')).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Exit full screen' }));
    expect(dialog).toHaveAttribute('data-compose-mode', 'docked');
    expect(document.querySelector('[data-compose-backdrop="true"]')).toBeNull();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('supports Escape to dismiss and Cmd/Ctrl+Enter to send immediately', async () => {
    const onClose = vi.fn();
    const first = renderWithProviders(
      <ComposeDialog
        mailbox={{ id: 1, address: 'hello@mail.example.com' }}
        draft={emptyDraft}
        onClose={onClose}
      />
    );

    const dialog = screen.getByRole('dialog', { name: 'New message' });
    // Quick typing without pressing Enter still sends seamlessly via Cmd+Enter.
    fireEvent.change(within(dialog).getByLabelText('To'), {
      target: { value: 'alice@example.com' },
    });
    fireEvent.change(within(dialog).getByLabelText('Subject'), { target: { value: 'Quick note' } });
    const body = within(dialog).getByLabelText('Message');
    fireEvent.change(body, { target: { value: 'Sent with shortcut' } });
    fireEvent.keyDown(body, { key: 'Enter', metaKey: true });

    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        to: ['alice@example.com'],
        subject: 'Quick note',
        text: 'Sent with shortcut',
      })
    );
    expect(onClose).toHaveBeenCalledTimes(1);
    first.unmount();

    // Escape dismisses an open docked composer.
    const onEscapeClose = vi.fn();
    renderWithProviders(
      <ComposeDialog
        mailbox={{ id: 1, address: 'hello@mail.example.com' }}
        draft={emptyDraft}
        onClose={onEscapeClose}
      />
    );
    fireEvent.keyDown(screen.getByRole('dialog', { name: 'New message' }), { key: 'Escape' });
    expect(onEscapeClose).toHaveBeenCalledTimes(1);
  });

  it('commits chips on Enter/comma/semicolon/Tab, pastes multiple, highlights invalid chips, and removes via Backspace or X', () => {
    const changes: string[] = [];
    function Harness() {
      const [value, setValue] = useState('');
      return (
        <div>
          <label htmlFor="recipients-test">To</label>
          <RecipientChipsInput
            id="recipients-test"
            name="to"
            value={value}
            onChange={next => {
              setValue(next);
              changes.push(next);
            }}
            removeLabel={address => `Remove ${address}`}
            invalidChipLabel={address => `Invalid ${address}`}
          />
        </div>
      );
    }

    renderWithProviders(<Harness />);
    const input = screen.getByLabelText('To');
    expect(input).toHaveAttribute('id', 'recipients-test');
    expect(input).toHaveAttribute('name', 'to');

    // Enter commits a valid chip.
    fireEvent.change(input, { target: { value: 'alice@example.com' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(screen.getByText('alice@example.com')).toBeInTheDocument();
    expect(changes.at(-1)).toBe('alice@example.com');

    // Comma commits another valid chip.
    fireEvent.change(input, { target: { value: 'bob@example.com' } });
    fireEvent.keyDown(input, { key: ',' });
    expect(screen.getByText('bob@example.com')).toBeInTheDocument();
    expect(changes.at(-1)).toBe('alice@example.com, bob@example.com');

    // Semicolon commits an invalid chip with red destructive styling.
    fireEvent.change(input, { target: { value: 'not-an-email' } });
    fireEvent.keyDown(input, { key: ';' });
    const badChip = screen.getByText('not-an-email').closest('[data-recipient-chip="true"]');
    expect(badChip).toHaveAttribute('data-invalid', 'true');
    expect(badChip?.className).toContain('border-destructive/50');
    expect(badChip?.className).toContain('bg-destructive/10');
    expect(badChip?.className).toContain('text-destructive');

    // Backspace on empty input removes the last chip (not-an-email).
    fireEvent.keyDown(input, { key: 'Backspace' });
    expect(screen.queryByText('not-an-email')).toBeNull();
    expect(changes.at(-1)).toBe('alice@example.com, bob@example.com');

    // Tab commits a typed address.
    fireEvent.change(input, { target: { value: 'carol@example.com' } });
    fireEvent.keyDown(input, { key: 'Tab' });
    expect(screen.getByText('carol@example.com')).toBeInTheDocument();

    // Paste multiple comma/semicolon/newline separated emails (deduping duplicates).
    fireEvent.paste(input, {
      clipboardData: {
        getData: () => 'dave@example.com, erin@example.com; bob@example.com\nfrank@example.com',
      },
    });
    expect(screen.getByText('dave@example.com')).toBeInTheDocument();
    expect(screen.getByText('erin@example.com')).toBeInTheDocument();
    expect(screen.getByText('frank@example.com')).toBeInTheDocument();
    expect(changes.at(-1)).toBe(
      'alice@example.com, bob@example.com, carol@example.com, dave@example.com, erin@example.com, frank@example.com'
    );

    // Clicking the chip's X removes that chip.
    fireEvent.click(screen.getByRole('button', { name: 'Remove carol@example.com' }));
    expect(screen.queryByText('carol@example.com')).toBeNull();
    expect(changes.at(-1)).toBe(
      'alice@example.com, bob@example.com, dave@example.com, erin@example.com, frank@example.com'
    );
  });

  it('submits cleanly when a user types an email directly without pressing Enter and also when mixing chips + uncommitted text', async () => {
    const onClose = vi.fn();
    renderWithProviders(
      <ComposeDialog
        mailbox={{ id: 1, address: 'hello@mail.example.com' }}
        draft={emptyDraft}
        onClose={onClose}
      />
    );

    const dialog = screen.getByRole('dialog', { name: 'New message' });
    const to = within(dialog).getByLabelText('To');
    fireEvent.change(to, { target: { value: 'alice@example.com' } });
    fireEvent.keyDown(to, { key: 'Enter' });
    // Leave second recipient uncommitted in the input before clicking Send.
    fireEvent.change(to, { target: { value: 'bob@example.com' } });
    fireEvent.change(within(dialog).getByLabelText('Subject'), { target: { value: 'Hello' } });
    fireEvent.change(within(dialog).getByLabelText('Message'), { target: { value: 'Body' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Send' }));

    await waitFor(() =>
      expect(state.send.mutateAsync).toHaveBeenCalledWith({
        to: ['alice@example.com', 'bob@example.com'],
        subject: 'Hello',
        text: 'Body',
      })
    );
  });
});
