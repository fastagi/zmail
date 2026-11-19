import { describe, expect, it } from 'vitest';

import { mailBadgeKey } from '@/features/mailbox/components/mail-shared';

describe('mailBadgeKey', () => {
  it('labels a message waiting for the send limit apart from one in flight', () => {
    const base = { status: 'queued', last_event: '' } as const;
    expect(mailBadgeKey({ ...base, queued_reason: 'send_limit' })).toBe('emails.statusWaiting');
    expect(mailBadgeKey({ ...base, queued_reason: null })).toBe('emails.statusQueued');
  });
});
