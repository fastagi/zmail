import { fireEvent, render, screen } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { MailThreadNote } from '@/features/mailbox/assignment-schemas';
import { ThreadNotes } from '@/features/mailbox/components/thread-notes';
import { messages } from '@/i18n/modules';

function renderWithIntl(ui: ReactElement) {
  return render(
    <NextIntlClientProvider
      locale="en-US"
      timeZone="UTC"
      messages={messages as unknown as AbstractIntlMessages}
    >
      {ui}
    </NextIntlClientProvider>
  );
}

const state = vi.hoisted(() => ({
  notes: undefined as MailThreadNote[] | undefined,
  add: vi.fn(),
  remove: vi.fn(),
  addError: null as unknown,
}));

vi.mock('@/features/mailbox/components/mail-organization', () => ({
  useMailOrganizationId: () => 7,
}));
vi.mock('@/features/auth/store/auth-store', async () => {
  const { createContext } = await import('react');
  return { AuthStoreContext: createContext({ getState: () => ({ user: { id: '2' } }) }) };
});
vi.mock('@/features/mailbox/hooks/use-thread-notes', () => ({
  useThreadNotes: () => ({ data: state.notes, isSuccess: state.notes !== undefined }),
  useAddThreadNote: () => ({
    mutate: state.add,
    isPending: false,
    isError: state.addError !== null,
    error: state.addError,
  }),
  useDeleteThreadNote: () => ({ mutate: state.remove, isPending: false, isError: false }),
}));
vi.mock('@/features/organization', () => ({
  useOrganizationMembers: () => ({
    data: {
      items: [
        { user_id: 2, username: 'ada', nickname: 'Ada Lovelace' },
        { user_id: 3, username: 'grace', nickname: '' },
      ],
    },
  }),
}));

function note(overrides: Partial<MailThreadNote> = {}): MailThreadNote {
  return {
    id: 1,
    thread_id: 9,
    author_user_id: 2,
    body: 'Customer wants a call back',
    created_at: '2026-11-22T08:00:00Z',
    ...overrides,
  };
}

describe('thread notes', () => {
  beforeEach(() => {
    state.add.mockReset();
    state.remove.mockReset();
    state.addError = null;
    state.notes = [];
  });

  it('says there are no notes and adds one', () => {
    renderWithIntl(<ThreadNotes mailboxId={4} threadId={9} canSend />);
    expect(screen.getByText('No notes yet.')).toBeInTheDocument();
    const add = screen.getByRole('button', { name: 'Add note' });
    expect(add).toBeDisabled();
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '  Call them  ' } });
    fireEvent.click(add);
    expect(state.add).toHaveBeenCalledWith('Call them', expect.anything());
  });

  it('lets only the author delete a note and names a deleted author', () => {
    state.notes = [
      note(),
      note({ id: 2, author_user_id: 3, body: 'On it' }),
      note({ id: 3, author_user_id: null, body: 'Old context' }),
    ];
    renderWithIntl(<ThreadNotes mailboxId={4} threadId={9} canSend />);
    expect(screen.getByText('3 notes')).toBeInTheDocument();
    expect(screen.getByText('Ada Lovelace')).toBeInTheDocument();
    expect(screen.getByText('grace')).toBeInTheDocument();
    expect(screen.getByText('Former member')).toBeInTheDocument();
    const deletes = screen.getAllByRole('button', { name: 'Delete note' });
    expect(deletes).toHaveLength(1);
    fireEvent.click(deletes[0]!);
    expect(state.remove).toHaveBeenCalledWith(1);
  });

  it('reads notes without a composer for a view-only member', () => {
    state.notes = [note()];
    renderWithIntl(<ThreadNotes mailboxId={4} threadId={9} canSend={false} />);
    expect(screen.getByText('Customer wants a call back')).toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  it('renders nothing until notes load and shows the limit error', () => {
    state.notes = undefined;
    const { container, rerender } = renderWithIntl(
      <ThreadNotes mailboxId={4} threadId={9} canSend />
    );
    expect(container).toBeEmptyDOMElement();
    state.notes = [];
    state.addError = Object.assign(new Error('limit'), { code: 'MAILBOX.NOTE_LIMIT_REACHED' });
    rerender(
      <NextIntlClientProvider
        locale="en-US"
        timeZone="UTC"
        messages={messages as unknown as AbstractIntlMessages}
      >
        <ThreadNotes mailboxId={4} threadId={9} canSend />
      </NextIntlClientProvider>
    );
    expect(screen.getByRole('alert')).toBeInTheDocument();
  });
});
