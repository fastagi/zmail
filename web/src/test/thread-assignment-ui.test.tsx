import { fireEvent, render, screen } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ThreadAssignment } from '@/features/mailbox/components/thread-assignment';
import type { MailThreadAssignment } from '@/features/mailbox/assignment-schemas';
import { messages } from '@/i18n/modules';

function renderWithIntl(ui: ReactElement) {
  return render(
    <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
      {ui}
    </NextIntlClientProvider>
  );
}

const state = vi.hoisted(() => ({
  assignment: undefined as MailThreadAssignment | undefined,
  mutate: vi.fn(),
}));

vi.mock('@/features/mailbox/components/mail-organization', () => ({
  useMailOrganizationId: () => 7,
}));
vi.mock('@/features/mailbox/hooks/use-thread-assignment', () => ({
  useThreadAssignment: () => ({ data: state.assignment, isSuccess: state.assignment !== undefined }),
  useSaveThreadAssignment: () => ({ mutate: state.mutate, isPending: false, isError: false }),
}));
vi.mock('@/features/organization', () => ({
  useOrganizationMembers: () => ({
    data: { items: [{ user_id: 2, username: 'ada', nickname: 'Ada Lovelace' }] },
  }),
}));

function assignment(overrides: Partial<MailThreadAssignment> = {}): MailThreadAssignment {
  return {
    thread_id: 9,
    assignee_user_id: 2,
    status: 'open',
    updated_by_user_id: 2,
    updated_at: '2026-11-22T00:00:00Z',
    assignable_user_ids: [2, 3],
    ...overrides,
  };
}

describe('thread assignment', () => {
  beforeEach(() => {
    state.mutate.mockReset();
    state.assignment = assignment();
  });

  it('shows the assignee by name and marks the thread done', () => {
    renderWithIntl(<ThreadAssignment mailboxId={4} threadId={9} canSend />);
    expect(screen.getByRole('combobox', { name: 'Assignee' })).toHaveTextContent('Ada Lovelace');
    expect(screen.getByText('Open')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Mark as done' }));
    expect(state.mutate).toHaveBeenCalledWith({ assignee_user_id: 2, status: 'closed' });
  });

  it('lets a reader without send only look', () => {
    state.assignment = assignment({ status: 'closed', assignee_user_id: null });
    renderWithIntl(<ThreadAssignment mailboxId={4} threadId={9} canSend={false} />);
    expect(screen.getByRole('combobox', { name: 'Assignee' })).toBeDisabled();
    expect(screen.getByRole('combobox', { name: 'Assignee' })).toHaveTextContent('Unassigned');
    expect(screen.getByText('Done')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reopen' })).toBeNull();
  });

  it('renders nothing until the API answers', () => {
    state.assignment = undefined;
    const { container } = renderWithIntl(<ThreadAssignment mailboxId={4} threadId={9} canSend />);
    expect(container).toBeEmptyDOMElement();
  });
});
