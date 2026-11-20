import { render, screen, within } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { describe, expect, it } from 'vitest';

import { InboxList } from '@/features/mailbox/components/inbox-list';
import { inboxItemSchema } from '@/features/mailbox/schemas';
import type { InboxItem, MailRole } from '@/features/mailbox/types';
import { messages } from '@/i18n/modules';
import zhHansMailbox from '@/i18n/modules/mailbox/zh-Hans';

function row(id: number, subject: string, source?: 'zmail' | 'gmail' | 'outlook'): InboxItem {
  return inboxItemSchema.parse({
    id,
    mailbox_id: 1,
    direction: 'inbound',
    status: 'received',
    from: { address: 'bob@example.com', name: 'Bob' },
    to: ['me@example.com'],
    cc: [],
    subject,
    read: true,
    failure_code: '',
    queued_reason: null,
    last_event: '',
    attachment_count: 0,
    ...(source ? { source } : {}),
    template_id: null,
    created_at: '2026-11-20T09:00:00Z',
    placement: 'default',
    folder_id: null,
    label_ids: [],
    starred: false,
    thread_id: id,
    trashed_at: null,
    mailbox_address: 'me@example.com',
    role_id: 7,
    snippet: '',
  });
}

const role: MailRole = {
  id: 7,
  name: 'Design',
  color: 'blue',
  description: '',
  owner_user_id: null,
  mailbox_ids: [1],
  created_at: '2026-11-20T09:00:00Z',
  updated_at: '2026-11-20T09:00:00Z',
};

function renderList(items: InboxItem[], locale: 'en-US' | 'zh-Hans' = 'en-US') {
  const localized = locale === 'en-US' ? messages : { ...messages, mailbox: zhHansMailbox };
  return render(
    <NextIntlClientProvider
      locale={locale}
      timeZone="UTC"
      messages={localized as unknown as AbstractIntlMessages}
    >
      <InboxList
        label="Inbox"
        pages={[{ items, meta: { per_page: 20, has_more: false, next_cursor: null } }]}
        roles={new Map([[role.id, role]])}
        sendable={new Set()}
        timeZone="UTC"
        selectedId={null}
        onOpen={() => undefined}
        onFilterMailbox={() => {}}
        onLoadMore={() => undefined}
        hasMore={false}
      />
    </NextIntlClientProvider>
  );
}

function option(subject: string) {
  return screen.getByRole('option', { name: new RegExp(subject, 'u') });
}

describe('inbox source badge', () => {
  it('marks Gmail and Outlook copies on the sender line and nothing for Zmail mail', () => {
    renderList([
      row(1, 'From Gmail', 'gmail'),
      row(2, 'From Outlook', 'outlook'),
      row(3, 'Own mail', 'zmail'),
      row(4, 'Older API'),
    ]);

    const gmail = within(option('From Gmail')).getByText('Gmail');
    expect(gmail).toHaveAttribute('data-source', 'gmail');
    expect(gmail).toHaveAttribute('title', 'Synced from a bound Gmail account (read-only)');
    // The badge follows the sender; the mailbox label moved to the subject line.
    expect(gmail.previousElementSibling).toHaveTextContent('Bob');
    expect(within(option('From Outlook')).getByText('Outlook')).toHaveAttribute(
      'data-source',
      'outlook'
    );
    for (const subject of ['Own mail', 'Older API']) {
      expect(option(subject).querySelector('[data-source]')).toBeNull();
    }
  });

  it('describes the badge in Simplified Chinese', () => {
    renderList([row(1, 'From Gmail', 'gmail')], 'zh-Hans');
    expect(within(option('From Gmail')).getByText('Gmail')).toHaveAttribute(
      'title',
      '从绑定的 Gmail 账号同步（只读）'
    );
  });

  it('refuses an unknown source', () => {
    expect(() => row(1, 'Unknown', 'imap' as 'gmail')).toThrow();
  });
});
