import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  mailDayStatsListSchema,
  mailLogEntrySchema,
  mailLogPageEnvelopeSchema,
  mailMessagePageEnvelopeSchema,
  mailMessageSchema,
  mailMessageSummarySchema,
  mailboxDomainListSchema,
  mailboxListSchema,
  normalizeDisplayName,
  normalizeLocalPart,
  normalizeMailMessage,
  normalizeRecipient,
  splitRecipients,
} from '@/features/mailbox/schemas';
import { mailboxService, parseMailMessage } from '@/features/mailbox/services/mailbox-service';
import {
  isOrganizationLost,
  isStoredSendFailure,
  resolveMailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode, ClientErrorCode } from '@/http/codes';
import request, { ApiError } from '@/http/request';

describe('mailbox service boundary', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('names the organization in a header on every call', async () => {
    const pageEnvelope = {
      code: 0 as const,
      message: 'success',
      data: [],
      meta: { current_page: 1, per_page: 20, total: 0, last_page: 1, from: 0, to: 0 },
      links: { first: '/a', last: '/a', prev: null, next: null },
    };
    const mailbox = {
      id: 12,
      address: 'hello@mail.example.com',
      display_name: '',
      unread_count: 0,
      created_at: '2026-11-16T10:00:00Z',
    };
    const get = vi.spyOn(request, 'get').mockImplementation(async url => {
      if (url === '/mail-message-stats') {
        return Array.from({ length: 7 }, (_, index) => ({
          day: `2026-11-1${index}`,
          sent: 0,
          failed: 0,
          received: 0,
        }));
      }
      return url.includes('/messages/') ? message() : [];
    });
    const getEnvelope = vi.spyOn(request, 'getEnvelope').mockResolvedValue(pageEnvelope);
    const post = vi
      .spyOn(request, 'post')
      .mockImplementation(async url => (url === '/mailboxes' ? mailbox : message()));
    const put = vi.spyOn(request, 'put').mockResolvedValue(message());
    const remove = vi.spyOn(request, 'delete').mockResolvedValue(undefined);

    await mailboxService.domains(7);
    await mailboxService.list(7);
    await mailboxService.stats(7);
    await mailboxService.message(7, 12, 7);
    await mailboxService.messages(7, 12, { direction: 'inbound' });
    await mailboxService.listLog(7, { status: 'failed' });
    await mailboxService.create(7, { local_part: 'hello' });
    await mailboxService.send(7, 12, { to: ['bob@example.com'], subject: 'Hi', text: 'Body' });
    await mailboxService.setRead(7, 12, 7, true);
    await mailboxService.remove(7, 12);
    await mailboxService.removeMessage(7, 12, 7);

    const header = { 'Organization-Id': '7' };
    const configs = [
      ...get.mock.calls.map(call => call[1]),
      ...getEnvelope.mock.calls.map(call => call[1]),
      ...post.mock.calls.map(call => call[2]),
      ...put.mock.calls.map(call => call[2]),
      ...remove.mock.calls.map(call => call[1]),
    ];
    expect(configs).toHaveLength(11);
    for (const config of configs) expect(config?.headers).toEqual(header);
    expect(get.mock.calls.map(call => call[0])).toEqual([
      '/mailbox-domains',
      '/mailboxes',
      '/mail-message-stats',
      '/mailboxes/12/messages/7',
    ]);
    expect(getEnvelope.mock.calls.map(call => call[0])).toEqual([
      '/mailboxes/12/messages',
      '/mail-messages',
    ]);
    // The organization is never a query parameter or part of the body.
    expect(getEnvelope.mock.calls[1][1]?.params).toEqual({
      status: 'failed',
      page: 1,
      per_page: 20,
    });
    expect(post.mock.calls[0][1]).toEqual({ local_part: 'hello' });
  });

  it('refuses a call without a valid organization before any request', async () => {
    const spies = [
      vi.spyOn(request, 'get'),
      vi.spyOn(request, 'getEnvelope'),
      vi.spyOn(request, 'post'),
      vi.spyOn(request, 'put'),
      vi.spyOn(request, 'delete'),
    ];

    for (const organizationId of [0, -1, 1.5, Number.NaN]) {
      const calls = [
        () => mailboxService.domains(organizationId),
        () => mailboxService.list(organizationId),
        () => mailboxService.stats(organizationId),
        () => mailboxService.message(organizationId, 12, 7),
        () => mailboxService.messages(organizationId, 12, { direction: 'inbound' }),
        () => mailboxService.listLog(organizationId),
        () => mailboxService.create(organizationId, { local_part: 'hello' }),
        () => mailboxService.send(organizationId, 12, { to: ['bob@example.com'], subject: 'Hi' }),
        () => mailboxService.setRead(organizationId, 12, 7, true),
        () => mailboxService.remove(organizationId, 12),
        () => mailboxService.removeMessage(organizationId, 12, 7),
      ];
      for (const call of calls) {
        await expect(call()).rejects.toMatchObject({ errorCode: ClientErrorCode.INVALID_RESPONSE });
      }
    }
    for (const spy of spies) expect(spy).not.toHaveBeenCalled();
  });

  it('accepts contract-shaped messages and rejects drift', () => {
    expect(parseMailMessage(message(), 7)).toMatchObject({ id: 7, status: 'received' });
    expect(() => parseMailMessage(message(), 8)).toThrow(ApiError);
    expect(() => parseMailMessage({ ...message(), extra: true })).toThrow(ApiError);
    expect(() => parseMailMessage({ ...message(), status: 'bounced' })).toThrow(ApiError);
    expect(() => parseMailMessage({ ...message(), html_body: undefined })).toThrow(ApiError);
  });

  it('requires the delivery summary and events and rejects unknown event types', () => {
    const sent = sentMessage();
    expect(parseMailMessage(sent, 7)).toMatchObject({
      last_event: 'bounced',
      events: [
        { type: 'accepted', recipient: '' },
        { type: 'bounced', recipient: 'bob@example.com', detail: '5.1.1 permanent_failure' },
      ],
    });
    for (const type of [
      'accepted',
      'delivered',
      'deferred',
      'bounced',
      'failed',
      'rejected',
      'complained',
    ]) {
      const one = { ...sent, last_event: type, events: [{ ...sent.events[1], type }] };
      expect(mailMessageSchema.safeParse(one).success, type).toBe(true);
    }

    // Both fields are required parts of the answer, on the detail and in every listing.
    for (const field of ['last_event', 'events']) {
      const partial: Record<string, unknown> = sentMessage();
      delete partial[field];
      expect(mailMessageSchema.safeParse(partial).success, field).toBe(false);
    }
    const withoutSummary: Record<string, unknown> = summary();
    delete withoutSummary.last_event;
    expect(mailMessageSummarySchema.safeParse(summary()).success).toBe(true);
    expect(mailMessageSummarySchema.safeParse(withoutSummary).success).toBe(false);
    const entry = { ...summary(), mailbox_address: 'hello@mail.example.com' };
    expect(mailLogEntrySchema.safeParse(entry).success).toBe(true);
    expect(mailLogEntrySchema.safeParse({ ...entry, last_event: 'delivered' }).success).toBe(true);
    expect(
      mailLogEntrySchema.safeParse({ ...withoutSummary, mailbox_address: entry.mailbox_address })
        .success
    ).toBe(false);
    // Only the detail carries the events.
    expect(mailMessageSummarySchema.safeParse({ ...summary(), events: [] }).success).toBe(false);
    expect(mailLogEntrySchema.safeParse({ ...entry, events: [] }).success).toBe(false);

    for (const drift of [
      { last_event: 'opened' },
      { last_event: null },
      { events: [{ ...sent.events[1], type: 'opened' }] },
      { events: [{ ...sent.events[1], type: '' }] },
      { events: [{ ...sent.events[1], occurred_at: 'yesterday' }] },
      { events: [{ ...sent.events[1], recipient: undefined }] },
      { events: [{ ...sent.events[1], detail: undefined }] },
      { events: [{ ...sent.events[1], smtp_response: '550 no such user' }] },
    ]) {
      expect(mailMessageSchema.safeParse({ ...sent, ...drift }).success).toBe(false);
    }
    expect(() =>
      parseMailMessage({ ...sent, events: [{ ...sent.events[1], type: 'opened' }] })
    ).toThrow(ApiError);
    expect(mailMessageSummarySchema.safeParse({ ...summary(), last_event: 'opened' }).success).toBe(
      false
    );
    expect(mailLogEntrySchema.safeParse({ ...entry, last_event: 'opened' }).success).toBe(false);
  });

  it('accepts domains, mailboxes, and pages without bodies', () => {
    expect(mailboxDomainListSchema.safeParse([domain()]).success).toBe(true);
    expect(mailboxDomainListSchema.safeParse([domain({ provider: 'log' })]).success).toBe(true);
    // The provider, the inbound state, and the send limit are required parts of the answer.
    for (const field of ['provider', 'inbound_enabled', 'daily_send_limit']) {
      const partial: Record<string, unknown> = domain();
      delete partial[field];
      expect(mailboxDomainListSchema.safeParse([partial]).success, field).toBe(false);
    }
    for (const drift of [
      { provider: 'smtp' },
      { inbound_enabled: 'yes' },
      { daily_send_limit: 0 },
      { daily_send_limit: 1.5 },
    ]) {
      expect(mailboxDomainListSchema.safeParse([domain(drift)]).success).toBe(false);
    }
    expect(
      mailboxListSchema.safeParse([
        {
          id: 12,
          address: 'hello@mail.example.com',
          display_name: '',
          unread_count: 3,
          created_at: '2026-11-16T10:00:00Z',
        },
      ]).success
    ).toBe(true);
    // A mailbox without its unread count is not a valid API answer.
    expect(
      mailboxListSchema.safeParse([
        {
          id: 12,
          address: 'hello@mail.example.com',
          display_name: '',
          created_at: '2026-11-16T10:00:00Z',
        },
      ]).success
    ).toBe(false);
    const page = (data: unknown[]) => ({
      code: 0,
      message: 'success',
      data,
      meta: { current_page: 1, per_page: 20, total: 1, last_page: 1, from: 1, to: 1 },
      links: { first: '/a', last: '/a', prev: null, next: null },
    });
    expect(mailMessagePageEnvelopeSchema.safeParse(page([summary()])).success).toBe(true);
    expect(mailMessagePageEnvelopeSchema.safeParse(page([message()])).success).toBe(false);
  });

  it('accepts log pages with the owning address and rejects drift', () => {
    const entry = { ...summary(), mailbox_address: 'hello@mail.example.com' };
    const page = (data: unknown[]) => ({
      code: 0,
      message: 'success',
      data,
      meta: { current_page: 1, per_page: 20, total: 1, last_page: 1, from: 1, to: 1 },
      links: { first: '/a', last: '/a', prev: null, next: null },
    });

    expect(mailLogPageEnvelopeSchema.safeParse(page([entry])).success).toBe(true);
    expect(mailLogPageEnvelopeSchema.safeParse(page([])).success).toBe(true);
    // A log entry always names its mailbox and never carries a body.
    expect(mailLogPageEnvelopeSchema.safeParse(page([summary()])).success).toBe(false);
    expect(
      mailLogPageEnvelopeSchema.safeParse(page([{ ...entry, text_body: 'Plain body' }])).success
    ).toBe(false);
    expect(
      mailLogPageEnvelopeSchema.safeParse(page([{ ...entry, status: 'bounced' }])).success
    ).toBe(false);
    expect(mailLogPageEnvelopeSchema.safeParse({ ...page([entry]), meta: undefined }).success).toBe(
      false
    );
  });

  it('accepts per-day statistics and rejects drift', () => {
    const day = { day: '2026-11-16', sent: 3, failed: 1, received: 2 };

    expect(mailDayStatsListSchema.safeParse([day, { ...day, day: '2026-11-17' }]).success).toBe(
      true
    );
    expect(mailDayStatsListSchema.safeParse([]).success).toBe(true);
    for (const drift of [
      { day: '2026-11-16T00:00:00Z' },
      { day: '16/11/2026' },
      { sent: -1 },
      { failed: 0.5 },
      { received: '2' },
      { queued: 1 },
    ]) {
      expect(mailDayStatsListSchema.safeParse([{ ...day, ...drift }]).success).toBe(false);
    }
    expect(
      mailDayStatsListSchema.safeParse([{ day: day.day, sent: day.sent, failed: day.failed }])
        .success
    ).toBe(false);
  });

  it('applies the address rules of the contract', () => {
    expect(normalizeLocalPart('  Hello.Team ')).toEqual({ ok: true, value: 'hello.team' });
    expect(normalizeLocalPart('a')).toEqual({ ok: true, value: 'a' });
    expect(normalizeLocalPart('a'.repeat(64))).toMatchObject({ ok: true });
    for (const invalid of ['', '.a', 'a.', 'a..b', 'a b', 'a@b', 'ä', 'a'.repeat(65)]) {
      expect(normalizeLocalPart(invalid), invalid).toEqual({ ok: false, reason: 'invalid' });
    }
    for (const reserved of ['postmaster', 'Admin', 'no-reply', 'mailer-daemon']) {
      expect(normalizeLocalPart(reserved), reserved).toEqual({ ok: false, reason: 'reserved' });
    }
    expect(normalizeDisplayName('  Hello   Team ')).toBe('Hello Team');
    expect(normalizeDisplayName('Bad <name>')).toBeNull();
    expect(normalizeDisplayName('"quoted"')).toBeNull();
    expect(normalizeDisplayName('n'.repeat(121))).toBeNull();
  });

  it('applies the send rules of the contract', () => {
    expect(normalizeRecipient(' Bob@Example.COM ')).toBe('bob@example.com');
    for (const invalid of ['', 'bob', 'bob@localhost', 'Bob <bob@example.com>', 'a@b@c.com']) {
      expect(normalizeRecipient(invalid), invalid).toBeNull();
    }
    expect(splitRecipients('a@example.com, b@example.com\n\nc@example.com')).toEqual([
      'a@example.com',
      'b@example.com',
      'c@example.com',
    ]);

    expect(
      normalizeMailMessage({
        to: ['Bob@example.com', 'bob@example.com'],
        cc: ['bob@example.com', 'carol@example.com'],
        subject: '  Hello ',
        text: 'Body',
        in_reply_to: '<id@example.com>',
      })
    ).toEqual({
      to: ['bob@example.com'],
      cc: ['carol@example.com'],
      subject: 'Hello',
      text: 'Body',
      html: '',
      in_reply_to: 'id@example.com',
    });
    const valid = { to: ['bob@example.com'], subject: 'Hello', text: 'Body' };
    expect(normalizeMailMessage({ ...valid, to: [] })).toBeNull();
    expect(normalizeMailMessage({ ...valid, to: ['Bob <bob@example.com>'] })).toBeNull();
    expect(normalizeMailMessage({ ...valid, subject: 'two\nlines' })).toBeNull();
    expect(normalizeMailMessage({ ...valid, subject: '   ' })).toBeNull();
    expect(normalizeMailMessage({ ...valid, text: '  ' })).toBeNull();
    expect(normalizeMailMessage({ ...valid, text: '', html: '<p>x</p>' })).not.toBeNull();
    expect(normalizeMailMessage({ ...valid, text: '字'.repeat(90_000) })).toBeNull();
    expect(normalizeMailMessage({ ...valid, in_reply_to: 'not-an-id' })).toBeNull();
    const many = Array.from({ length: 50 }, (_, index) => `user${index}@example.com`);
    expect(normalizeMailMessage({ ...valid, to: many })).not.toBeNull();
    expect(normalizeMailMessage({ ...valid, to: many, cc: ['extra@example.com'] })).toBeNull();
  });

  it('maps errors to local copy and knows which failed sends were stored', () => {
    const error = (code: ConstructorParameters<typeof ApiError>[1]) => new ApiError('x', code);
    expect(resolveMailboxErrorKey(error(ApiErrorCode.MAILBOX_ADDRESS_TAKEN))).toBe(
      'errors.addressTaken'
    );
    expect(resolveMailboxErrorKey(error(ApiErrorCode.MAILBOX_ADDRESS_INVALID))).toBe(
      'errors.addressInvalid'
    );
    expect(resolveMailboxErrorKey(error(ApiErrorCode.MAILBOX_ADDRESS_RESERVED))).toBe(
      'errors.addressReserved'
    );
    expect(resolveMailboxErrorKey(error(ApiErrorCode.MAILBOX_LIMIT_REACHED))).toBe(
      'errors.limitReached'
    );
    expect(resolveMailboxErrorKey(error(ApiErrorCode.MAILBOX_MESSAGE_INVALID))).toBe(
      'errors.messageInvalid'
    );
    expect(resolveMailboxErrorKey(error(ApiErrorCode.MAILBOX_SEND_LIMIT_REACHED))).toBe(
      'errors.sendLimitReached'
    );
    expect(resolveMailboxErrorKey(error(ApiErrorCode.MAILBOX_DELIVERY_FAILED))).toBe(
      'errors.deliveryFailed'
    );
    expect(resolveMailboxErrorKey(error(ApiErrorCode.MAILBOX_SENDER_NOT_CONFIGURED))).toBe(
      'errors.senderNotConfigured'
    );
    // A member who creates or deletes a mailbox, and a member who lost the organization.
    expect(resolveMailboxErrorKey(error(ApiErrorCode.PERMISSION_DENIED))).toBe(
      'errors.permissionDenied'
    );
    expect(resolveMailboxErrorKey(error(ApiErrorCode.ORGANIZATION_NOT_FOUND))).toBe(
      'errors.organizationNotFound'
    );
    expect(isOrganizationLost(error(ApiErrorCode.ORGANIZATION_NOT_FOUND))).toBe(true);
    expect(isOrganizationLost(error(ApiErrorCode.MAILBOX_NOT_FOUND))).toBe(false);
    expect(isOrganizationLost(new Error('x'))).toBe(false);
    expect(resolveMailboxErrorKey(error(ClientErrorCode.INVALID_RESPONSE))).toBe(
      'errors.invalidResponse'
    );
    expect(resolveMailboxErrorKey(new Error('x'))).toBe('errors.generic');

    expect(isStoredSendFailure(error(ApiErrorCode.MAILBOX_DELIVERY_FAILED))).toBe(true);
    expect(isStoredSendFailure(error(ApiErrorCode.MAILBOX_SENDER_NOT_CONFIGURED))).toBe(true);
    expect(isStoredSendFailure(error(ApiErrorCode.MAILBOX_SEND_LIMIT_REACHED))).toBe(false);
    expect(isStoredSendFailure(error(ApiErrorCode.MAILBOX_MESSAGE_INVALID))).toBe(false);
  });
});

function domain(overrides: Record<string, unknown> = {}) {
  return {
    domain: 'mail.example.com',
    max_mailboxes: 50,
    provider: 'cloudflare',
    inbound_enabled: true,
    daily_send_limit: 200,
    ...overrides,
  };
}

function summary() {
  return {
    id: 7,
    mailbox_id: 12,
    direction: 'inbound',
    status: 'received',
    from: { address: 'bob@example.com', name: 'Bob' },
    to: ['hello@mail.example.com'],
    cc: [],
    subject: 'Hello',
    read: false,
    failure_code: '',
    last_event: '',
    created_at: '2026-11-16T10:00:00.123456+08:00',
  };
}

function message() {
  return {
    ...summary(),
    text_body: 'Plain body',
    html_body: '<p>HTML body</p>',
    message_id: 'previous-id@example.com',
    in_reply_to: '',
    events: [],
  };
}

function sentMessage() {
  return {
    ...message(),
    direction: 'outbound',
    status: 'sent',
    read: true,
    last_event: 'bounced',
    events: [
      { type: 'accepted', recipient: '', detail: '', occurred_at: '2026-11-16T10:00:00Z' },
      {
        type: 'bounced',
        recipient: 'bob@example.com',
        detail: '5.1.1 permanent_failure',
        occurred_at: '2026-11-16T10:00:05Z',
      },
    ],
  };
}
