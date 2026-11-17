import 'server-only';

import { env } from '@/config/env';
import {
  MAIL_BROADCAST_LABEL_MAX_LENGTH,
  MAIL_BROADCAST_NAME_MAX_LENGTH,
  MAIL_BROADCAST_TEST_MAX_PERCENT,
  MAIL_BROADCAST_TEST_WAIT_DEFAULT_MINUTES,
  MAIL_BROADCAST_TEST_WAIT_MAX_MINUTES,
  MAIL_BROADCAST_TEST_WAIT_MIN_MINUTES,
} from '@/features/mailbox/schemas';
import { mockContactStore } from '@/features/mailbox/server/mock-contact-store';
import { mockMailboxStore } from '@/features/mailbox/server/mock-mailbox-store';
import type {
  MailBroadcast,
  MailBroadcastInput,
  MailBroadcastPage,
  MailBroadcastRecipient,
  MailBroadcastRecipientPage,
  MailRecipientStatus,
} from '@/features/mailbox/types';

/**
 * Development-only broadcasts. They follow the states of `contracts/MAILBOXES.md`, but the mock
 * has no worker: starting a broadcast sends every recipient at once through the mock mailbox,
 * and a scheduled broadcast waits until it is canceled. With a test wave, starting sends only the
 * test wave and pauses as if waiting; resuming sends the rest. There is no health check, since
 * the mock never bounces. Unsubscribe tokens use a fixed mock signature.
 */

export const MOCK_UNSUBSCRIBE_SIGNATURE = 'm'.repeat(22);

export type MockBroadcastResult =
  | { ok: true; broadcast: MailBroadcast }
  | {
      ok: false;
      error:
        | 'not_found'
        | 'invalid'
        | 'not_editable'
        | 'no_recipients'
        | 'mailbox_not_found'
        | 'template_not_found'
        | 'list_not_found';
    };

interface StoredBroadcast {
  broadcast: Omit<MailBroadcast, 'counts'>;
  recipients: (MailBroadcastRecipient & { wave: 1 | 2 })[];
}

export class MockBroadcastStore {
  private nextId = 1;
  private organizations = new Map<number, Map<number, StoredBroadcast>>();

  constructor(private readonly now: () => Date = () => new Date()) {}

  list(organizationId: number, page: number, perPage: number): MailBroadcastPage {
    const all = [...this.of(organizationId).values()].sort(
      (a, b) => b.broadcast.id - a.broadcast.id
    );
    const start = (page - 1) * perPage;
    const items = all.slice(start, start + perPage).map(withCounts);
    return {
      items,
      meta: {
        current_page: page,
        per_page: perPage,
        total: all.length,
        last_page: Math.max(1, Math.ceil(all.length / perPage)),
        from: items.length > 0 ? start + 1 : 0,
        to: start + items.length,
      },
      links: { first: '', last: '', prev: null, next: null },
    };
  }

  get(organizationId: number, id: number): MockBroadcastResult {
    const stored = this.of(organizationId).get(id);
    return stored ? { ok: true, broadcast: withCounts(stored) } : { ok: false, error: 'not_found' };
  }

  save(organizationId: number, input: MailBroadcastInput, id?: number): MockBroadcastResult {
    const broadcasts = this.of(organizationId);
    const existing = id === undefined ? undefined : broadcasts.get(id);
    if (id !== undefined && !existing) return { ok: false, error: 'not_found' };
    if (existing && existing.broadcast.status !== 'draft')
      return { ok: false, error: 'not_editable' };
    const name = input.name.trim().split(/\s+/u).filter(Boolean).join(' ');
    const label = (input.unsubscribe_label ?? '').trim();
    const testPercent = input.test_percent ?? 0;
    const testWait = input.test_wait_minutes || MAIL_BROADCAST_TEST_WAIT_DEFAULT_MINUTES;
    if (
      testPercent > MAIL_BROADCAST_TEST_MAX_PERCENT ||
      testWait < MAIL_BROADCAST_TEST_WAIT_MIN_MINUTES ||
      testWait > MAIL_BROADCAST_TEST_WAIT_MAX_MINUTES ||
      name === '' ||
      Array.from(name).length > MAIL_BROADCAST_NAME_MAX_LENGTH ||
      Array.from(label).length > MAIL_BROADCAST_LABEL_MAX_LENGTH
    ) {
      return { ok: false, error: 'invalid' };
    }
    if (!mockMailboxStore.hasMailbox(organizationId, input.mailbox_id)) {
      return { ok: false, error: 'mailbox_not_found' };
    }
    if (!mockMailboxStore.getTemplate(organizationId, input.template_id).ok) {
      return { ok: false, error: 'template_not_found' };
    }
    if (!mockContactStore.hasList(organizationId, input.list_id)) {
      return { ok: false, error: 'list_not_found' };
    }
    const now = this.timestamp();
    const stored: StoredBroadcast = existing ?? {
      broadcast: {
        id: this.nextId++,
        name,
        mailbox_id: input.mailbox_id,
        template_id: input.template_id,
        list_id: input.list_id,
        unsubscribe_label: label,
        variables: {},
        status: 'draft',
        scheduled_at: null,
        started_at: null,
        finished_at: null,
        stop_reason: '',
        test_percent: testPercent,
        test_wait_minutes: testWait,
        current_wave: 1,
        resume_at: null,
        safety_override: false,
        created_at: now,
        updated_at: now,
      },
      recipients: [],
    };
    stored.broadcast = {
      ...stored.broadcast,
      name,
      mailbox_id: input.mailbox_id,
      template_id: input.template_id,
      list_id: input.list_id,
      unsubscribe_label: label,
      variables: { ...input.variables },
      test_percent: testPercent,
      test_wait_minutes: testWait,
      updated_at: now,
    };
    broadcasts.set(stored.broadcast.id, stored);
    return { ok: true, broadcast: withCounts(stored) };
  }

  remove(organizationId: number, id: number): 'ok' | 'not_found' | 'not_editable' {
    const broadcasts = this.of(organizationId);
    const stored = broadcasts.get(id);
    if (!stored) return 'not_found';
    if (['scheduled', 'sending', 'paused'].includes(stored.broadcast.status)) {
      return 'not_editable';
    }
    broadcasts.delete(id);
    return 'ok';
  }

  send(organizationId: number, id: number, scheduledAt: string | null): MockBroadcastResult {
    const stored = this.of(organizationId).get(id);
    if (!stored) return { ok: false, error: 'not_found' };
    if (stored.broadcast.status !== 'draft') return { ok: false, error: 'not_editable' };
    const contacts = mockContactStore.subscribedIn(organizationId, stored.broadcast.list_id);
    if (contacts.length === 0) return { ok: false, error: 'no_recipients' };
    const now = this.now();
    const at = scheduledAt === null ? null : new Date(scheduledAt);
    if (at !== null && Number.isNaN(at.getTime())) return { ok: false, error: 'invalid' };
    const testSize =
      stored.broadcast.test_percent > 0
        ? Math.ceil((contacts.length * stored.broadcast.test_percent) / 100)
        : contacts.length;
    stored.recipients = contacts.map((contact, index) => ({
      id: this.nextId++,
      contact_id: contact.id,
      email: contact.email,
      status: 'pending',
      message_id: null,
      failure_code: '',
      updated_at: now.toISOString(),
      wave: index < testSize ? 1 : 2,
    }));
    if (at !== null && at > now) {
      stored.broadcast = {
        ...stored.broadcast,
        status: 'scheduled',
        scheduled_at: at.toISOString(),
      };
      return { ok: true, broadcast: withCounts(stored) };
    }
    stored.broadcast = {
      ...stored.broadcast,
      status: 'sending',
      started_at: now.toISOString(),
      current_wave: 1,
    };
    this.deliver(organizationId, stored, 1);
    return { ok: true, broadcast: withCounts(stored) };
  }

  /** Holds a broadcast whose test wave is waiting; the mock is never caught mid-send. */
  pause(organizationId: number, id: number): MockBroadcastResult {
    const stored = this.of(organizationId).get(id);
    if (!stored) return { ok: false, error: 'not_found' };
    if (stored.broadcast.status !== 'sending' && stored.broadcast.status !== 'paused') {
      return { ok: false, error: 'not_editable' };
    }
    stored.broadcast = {
      ...stored.broadcast,
      status: 'paused',
      stop_reason: 'paused_by_owner',
      resume_at: null,
      updated_at: this.timestamp(),
    };
    return { ok: true, broadcast: withCounts(stored) };
  }

  /** Continues a paused broadcast; after a test wave, the rest is sent at once. */
  resume(organizationId: number, id: number): MockBroadcastResult {
    const stored = this.of(organizationId).get(id);
    if (!stored) return { ok: false, error: 'not_found' };
    if (stored.broadcast.status !== 'paused') return { ok: false, error: 'not_editable' };
    const wave =
      stored.broadcast.stop_reason === 'test_wave_complete' ? 2 : stored.broadcast.current_wave;
    stored.broadcast = {
      ...stored.broadcast,
      status: 'sending',
      stop_reason: '',
      resume_at: null,
      current_wave: wave,
    };
    this.deliver(organizationId, stored, wave === 2 ? 2 : 1);
    return { ok: true, broadcast: withCounts(stored) };
  }

  /** Sends every pending recipient up to the wave, then waits after a test wave or finishes. */
  private deliver(organizationId: number, stored: StoredBroadcast, upToWave: 1 | 2): void {
    const now = this.now();
    const contacts = mockContactStore.subscribedIn(organizationId, stored.broadcast.list_id);
    for (const recipient of stored.recipients) {
      if (recipient.status !== 'pending' || recipient.wave > upToWave) continue;
      const contact = contacts.find(candidate => candidate.id === recipient.contact_id);
      if (!contact) {
        recipient.status = 'skipped';
        recipient.failure_code = 'contact_not_subscribed';
        continue;
      }
      const result = mockMailboxStore.send(organizationId, stored.broadcast.mailbox_id, {
        to: [contact.email],
        template: {
          id: stored.broadcast.template_id,
          variables: {
            ...stored.broadcast.variables,
            ...contact.properties,
            email: contact.email,
            first_name: contact.first_name,
            last_name: contact.last_name,
            unsubscribe_url: `${env.NEXT_PUBLIC_APP_URL}/unsubscribe/${organizationId}.${contact.id}.${MOCK_UNSUBSCRIBE_SIGNATURE}`,
          },
        },
      });
      recipient.status = result.ok ? 'sent' : 'failed';
      recipient.message_id = result.ok ? result.message.id : null;
      recipient.failure_code = result.ok
        ? ''
        : result.error === 'variables_missing'
          ? 'template.variables_missing'
          : 'message_invalid';
      recipient.updated_at = now.toISOString();
    }
    if (stored.recipients.some(recipient => recipient.status === 'pending')) {
      const resumeAt = new Date(now.getTime() + stored.broadcast.test_wait_minutes * 60_000);
      stored.broadcast = {
        ...stored.broadcast,
        status: 'paused',
        stop_reason: 'test_wave_complete',
        resume_at: resumeAt.toISOString(),
      };
      return;
    }
    stored.broadcast = { ...stored.broadcast, status: 'sent', finished_at: now.toISOString() };
  }

  cancel(organizationId: number, id: number): MockBroadcastResult {
    const stored = this.of(organizationId).get(id);
    if (!stored) return { ok: false, error: 'not_found' };
    if (!['scheduled', 'sending', 'paused'].includes(stored.broadcast.status)) {
      return { ok: false, error: 'not_editable' };
    }
    const now = this.timestamp();
    for (const recipient of stored.recipients) {
      if (recipient.status === 'pending') recipient.status = 'canceled';
    }
    stored.broadcast = {
      ...stored.broadcast,
      status: 'canceled',
      finished_at: now,
      resume_at: null,
    };
    return { ok: true, broadcast: withCounts(stored) };
  }

  recipients(
    organizationId: number,
    id: number,
    status: MailRecipientStatus | undefined,
    page: number,
    perPage: number
  ): MailBroadcastRecipientPage | null {
    const stored = this.of(organizationId).get(id);
    if (!stored) return null;
    const all = stored.recipients
      .filter(recipient => !status || recipient.status === status)
      .map(publicRecipient);
    const start = (page - 1) * perPage;
    const items = all.slice(start, start + perPage);
    return {
      items,
      meta: {
        current_page: page,
        per_page: perPage,
        total: all.length,
        last_page: Math.max(1, Math.ceil(all.length / perPage)),
        from: items.length > 0 ? start + 1 : 0,
        to: start + items.length,
      },
      links: { first: '', last: '', prev: null, next: null },
    };
  }

  /** Test seam: forget every broadcast. */
  reset(): void {
    this.nextId = 1;
    this.organizations.clear();
  }

  private of(organizationId: number): Map<number, StoredBroadcast> {
    const existing = this.organizations.get(organizationId);
    if (existing) return existing;
    const created = new Map<number, StoredBroadcast>();
    this.organizations.set(organizationId, created);
    return created;
  }

  private timestamp(): string {
    return this.now().toISOString();
  }
}

export const mockBroadcastStore = new MockBroadcastStore();

/** A recipient as the API shows it: the wave is internal. */
function publicRecipient(recipient: StoredBroadcast['recipients'][number]): MailBroadcastRecipient {
  return {
    id: recipient.id,
    contact_id: recipient.contact_id,
    email: recipient.email,
    status: recipient.status,
    message_id: recipient.message_id,
    failure_code: recipient.failure_code,
    updated_at: recipient.updated_at,
  };
}

function withCounts(stored: StoredBroadcast): MailBroadcast {
  const count = (status: MailRecipientStatus) =>
    stored.recipients.filter(recipient => recipient.status === status).length;
  return {
    ...stored.broadcast,
    counts: {
      total: stored.recipients.length,
      pending: count('pending'),
      sent: count('sent'),
      failed: count('failed'),
      skipped: count('skipped'),
      canceled: count('canceled'),
      // The mock never bounces and never hears complaints.
      bounced: 0,
      complained: 0,
    },
  };
}
