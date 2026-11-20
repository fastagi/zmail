import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  attachmentsWithinLimits,
  base64DecodedLength,
  mailDayStatsListSchema,
  mailLogEntrySchema,
  mailLogPageEnvelopeSchema,
  mailMessagePageEnvelopeSchema,
  mailMessageSchema,
  mailMessageSummarySchema,
  mailboxAccessEventSchema,
  mailboxDomainListSchema,
  mailboxListSchema,
  mailboxMemberSchema,
  normalizeDisplayName,
  normalizeLocalPart,
  normalizeMailMessage,
  normalizeRecipient,
  splitRecipients,
} from '@/features/mailbox/schemas';
import { mailboxAdminService } from '@/features/mailbox/services/mailbox-admin-service';
import { mailboxService, parseMailMessage } from '@/features/mailbox/services/mailbox-service';
import {
  isAccessConflict,
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

  it('reads the directory, renames, imports, and exports mailboxes', async () => {
    const header = { 'Organization-Id': '7' };
    const meta = {
      per_page: 15,
      has_more: true,
      next_cursor: 'MTI',
      daily_send_limit: 200,
      sent_last_24h: 4,
      mailbox_count: 30,
      max_mailboxes: 50,
    };
    const entry = {
      id: 12,
      address: 'team@mail.example.com',
      display_name: 'Team',
      type: 'shared',
      owner_user_id: null,
      awaiting_handover: false,
      created_at: '2026-11-19T10:00:00Z',
      member_count: 2,
      sent_last_24h: 4,
    };
    const getEnvelope = vi
      .spyOn(request, 'getEnvelope')
      .mockResolvedValueOnce({ code: 0, message: 'success', data: [entry], meta });
    const page = await mailboxAdminService.directory(7, { type: 'shared', q: ' sa ' }, 'abc');
    expect(page).toEqual({ items: [entry], meta });
    expect(getEnvelope).toHaveBeenCalledWith('/mailbox-directory', {
      headers: header,
      params: { type: 'shared', q: 'sa', cursor: 'abc' },
    });

    // A page whose meta lacks `next_cursor` is not the documented page.
    const { next_cursor: _dropped, ...withoutCursor } = meta;
    getEnvelope.mockResolvedValueOnce({
      code: 0,
      message: 'success',
      data: [],
      meta: withoutCursor,
    });
    await expect(mailboxAdminService.directory(7, {})).rejects.toMatchObject({
      errorCode: ClientErrorCode.INVALID_RESPONSE,
    });

    const get = vi.spyOn(request, 'get').mockResolvedValue([]);
    await mailboxService.list(7, 'usable');
    expect(get).toHaveBeenCalledWith('/mailboxes', {
      headers: header,
      params: { scope: 'usable' },
    });

    const put = vi.spyOn(request, 'put').mockResolvedValue(accessMailbox());
    await mailboxAdminService.rename(7, 12, 'Billing');
    expect(put).toHaveBeenCalledWith(
      '/mailboxes/12',
      { display_name: 'Billing' },
      { headers: header }
    );

    const result = {
      dry_run: true,
      succeeded: 1,
      failed: 0,
      rows: [
        {
          row: 1,
          address: 'sales@mail.example.com',
          status: 'valid',
          mailbox_id: null,
          error_code: null,
          missing_emails: [],
        },
      ],
    };
    const post = vi.spyOn(request, 'post').mockResolvedValue(result);
    const rows = [{ address: 'sales@mail.example.com', display_name: '', members: 'a@b.co' }];
    await expect(mailboxAdminService.importMailboxes(7, rows, true)).resolves.toEqual(result);
    expect(post).toHaveBeenCalledWith(
      '/mailbox-imports',
      { dry_run: true, rows },
      { headers: header }
    );

    const getFile = vi.spyOn(request, 'getFile').mockResolvedValue({
      blob: new Blob(['address']),
      disposition: 'attachment; filename="shared-mailboxes-20261119.csv"',
    });
    const exported = await mailboxAdminService.exportMailboxes(7, 'shared');
    expect(exported.filename).toBe('shared-mailboxes-20261119.csv');
    expect(getFile).toHaveBeenCalledWith('/mailbox-exports', {
      headers: header,
      params: { type: 'shared' },
    });
    // Without a usable disposition the name falls back to the type.
    getFile.mockResolvedValueOnce({ blob: new Blob(['address']), disposition: null });
    expect((await mailboxAdminService.exportMailboxes(7, 'personal')).filename).toBe(
      'personal-mailboxes.csv'
    );
  });

  it('names the organization in a header on every call', async () => {
    const pageEnvelope = {
      code: 0 as const,
      message: 'success',
      data: [],
      meta: { current_page: 1, per_page: 20, total: 0, last_page: 1, from: 0, to: 0 },
      links: { first: '/a', last: '/a', prev: null, next: null },
    };
    const mailbox = accessMailbox();
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
    await mailboxService.create(7, { local_part: 'hello', type: 'personal', owner_user_id: 3 });
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
    expect(post.mock.calls[0][1]).toEqual({
      local_part: 'hello',
      type: 'personal',
      owner_user_id: 3,
    });
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
        () =>
          mailboxService.create(organizationId, {
            local_part: 'hello',
            type: 'personal',
            owner_user_id: 3,
          }),
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

  it('parses mailboxes with and without role_id', async () => {
    const get = vi
      .spyOn(request, 'get')
      .mockResolvedValueOnce([accessMailbox({ role_id: 3 }), accessMailbox({ id: 13 })]);
    const mailboxes = await mailboxService.list(7);
    expect(mailboxes.map(mailbox => mailbox.role_id)).toEqual([3, undefined]);

    get.mockResolvedValueOnce([accessMailbox({ role_id: 0 })]);
    await expect(mailboxService.list(7)).rejects.toBeInstanceOf(ApiError);
  });

  it('accepts contract-shaped messages and rejects drift', () => {
    expect(parseMailMessage(message(), 7)).toMatchObject({ id: 7, status: 'received' });
    expect(() => parseMailMessage(message(), 8)).toThrow(ApiError);
    expect(() => parseMailMessage({ ...message(), extra: true })).toThrow(ApiError);
    expect(() => parseMailMessage({ ...message(), status: 'bounced' })).toThrow(ApiError);
    expect(() => parseMailMessage({ ...message(), html_body: undefined })).toThrow(ApiError);
  });

  it('accepts a message waiting for the send limit and rejects other queue reasons', () => {
    expect(
      parseMailMessage({
        ...message(),
        direction: 'outbound',
        status: 'queued',
        queued_reason: 'send_limit',
      })
    ).toMatchObject({ status: 'queued', queued_reason: 'send_limit' });
    expect(() => parseMailMessage({ ...message(), queued_reason: 'paused' })).toThrow(ApiError);
    const { queued_reason: _omitted, ...withoutReason } = message();
    expect(() => parseMailMessage(withoutReason)).toThrow(ApiError);
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
    const entry = { ...logSummary(), mailbox_address: 'hello@mail.example.com' };
    expect(mailLogEntrySchema.safeParse(entry).success).toBe(true);
    expect(mailLogEntrySchema.safeParse({ ...entry, last_event: 'delivered' }).success).toBe(true);
    const entryWithoutSummary: Record<string, unknown> = { ...entry };
    delete entryWithoutSummary.last_event;
    expect(mailLogEntrySchema.safeParse(entryWithoutSummary).success).toBe(false);
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
    // The provider, the inbound state, the lane limits, and the transactional settings are required.
    for (const field of [
      'provider',
      'inbound_enabled',
      'daily_send_limit',
      'daily_broadcast_limit',
      'daily_transactional_limit',
      'transactional_enabled',
      'transactional_recipient_limit_per_minute',
      'transactional_key_limit_per_second',
      'simulator_domain',
    ]) {
      const partial: Record<string, unknown> = domain();
      delete partial[field];
      expect(mailboxDomainListSchema.safeParse([partial]).success, field).toBe(false);
    }
    for (const drift of [
      { provider: 'smtp' },
      { inbound_enabled: 'yes' },
      { daily_send_limit: 0 },
      { daily_send_limit: 1.5 },
      { daily_broadcast_limit: 0 },
      { daily_transactional_limit: 1.5 },
      { transactional_enabled: 'yes' },
      { transactional_recipient_limit_per_minute: 0 },
      { transactional_key_limit_per_second: 1.5 },
    ]) {
      expect(mailboxDomainListSchema.safeParse([domain(drift)]).success).toBe(false);
    }
    expect(mailboxListSchema.safeParse([accessMailbox({ unread_count: 3 })]).success).toBe(true);
    // A mailbox without its unread count is not a valid API answer.
    const withoutUnread: Record<string, unknown> = accessMailbox();
    delete withoutUnread.unread_count;
    expect(mailboxListSchema.safeParse([withoutUnread]).success).toBe(false);
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
    const entry = { ...logSummary(), mailbox_address: 'hello@mail.example.com' };
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
    for (const role of ['support', 'Admin', 'postmaster']) {
      expect(normalizeLocalPart(role, { sharedDomain: false }), role).toEqual({
        ok: true,
        value: role.toLowerCase(),
      });
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

describe('attachment limits', () => {
  it('measures base64 and applies the contract limits', () => {
    expect(base64DecodedLength('')).toBeNull();
    expect(base64DecodedLength('YQ==')).toBe(1);
    expect(base64DecodedLength('YWI=')).toBe(2);
    expect(base64DecodedLength('YWJj')).toBe(3);
    expect(base64DecodedLength('YWJ')).toBeNull();
    expect(base64DecodedLength('YW J')).toBeNull();

    const file = { filename: 'a.txt', content: 'YQ==' };
    expect(attachmentsWithinLimits([])).toBe(true);
    expect(attachmentsWithinLimits([file])).toBe(true);
    expect(attachmentsWithinLimits(Array.from({ length: 10 }, () => file))).toBe(true);
    expect(attachmentsWithinLimits(Array.from({ length: 11 }, () => file))).toBe(false);
    expect(attachmentsWithinLimits([{ ...file, filename: 'dir/' }])).toBe(false);
    expect(attachmentsWithinLimits([{ ...file, content: '' }])).toBe(false);
    const big = 'A'.repeat(((4 * 1_024 * 1_024) / 3) * 4 + 4);
    expect(attachmentsWithinLimits([{ ...file, content: big }])).toBe(false);
  });

  it('accepts attachment metadata and rejects attachment content in a message', () => {
    const attachment = { id: 3, filename: 'a.pdf', content_type: 'application/pdf', size: 8 };
    expect(
      parseMailMessage({ ...message(), attachment_count: 1, attachments: [attachment] }, 7)
    ).toMatchObject({ attachments: [attachment] });
    expect(() =>
      parseMailMessage({ ...message(), attachments: [{ ...attachment, content: 'YQ==' }] }, 7)
    ).toThrow(ApiError);
    expect(() => parseMailMessage({ ...message(), attachments: undefined }, 7)).toThrow(ApiError);
  });
});

function domain(overrides: Record<string, unknown> = {}) {
  return {
    domain: 'mail.example.com',
    kind: 'shared',
    max_mailboxes: 50,
    provider: 'cloudflare',
    inbound_enabled: true,
    daily_send_limit: 200,
    daily_broadcast_limit: 200,
    daily_transactional_limit: 200,
    transactional_enabled: true,
    transactional_recipient_limit_per_minute: 3,
    transactional_key_limit_per_second: 20,
    simulator_domain: 'test.zmail.im',
    broadcasts_enabled: true,
    custom_domains_enabled: false,
    ...overrides,
  };
}

/** A summary as the log lists it: the log does not carry placements, stars, or threads. */
function logSummary() {
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
    queued_reason: null,
    last_event: '',
    attachment_count: 0,
    template_id: null,
    created_at: '2026-11-16T10:00:00.123456+08:00',
  };
}

function summary() {
  return {
    ...logSummary(),
    placement: 'default',
    folder_id: null,
    label_ids: [] as number[],
    starred: false,
    thread_id: 7,
    trashed_at: null,
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
    attachments: [],
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

describe('mailbox access service boundary', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('calls the member, history, handover, sealed-history, and webhook routes with the header', async () => {
    const member = {
      user_id: 9,
      permission: 'send',
      granted_by: 3,
      created_at: '2026-11-19T10:00:00Z',
    };
    const eventPage = {
      code: 0 as const,
      message: 'success',
      data: [accessEvent()],
      meta: { current_page: 2, per_page: 10, total: 11, last_page: 2, from: 11, to: 11 },
      links: { first: '/a', last: '/a', prev: '/a', next: null },
    };
    const get = vi.spyOn(request, 'get').mockResolvedValue([member]);
    const getEnvelope = vi.spyOn(request, 'getEnvelope').mockResolvedValue(eventPage);
    const put = vi
      .spyOn(request, 'put')
      .mockImplementation(async url =>
        url.endsWith('/webhook-setting') ? accessMailbox({ webhooks_enabled: true }) : member
      );
    const post = vi.spyOn(request, 'post').mockResolvedValue(accessMailbox({ sealed_before: now }));
    const remove = vi
      .spyOn(request, 'delete')
      .mockImplementation(async url =>
        url.endsWith('/sealed-messages')
          ? { deleted_messages: 3, deleted_broadcasts: 1 }
          : undefined
      );

    await expect(mailboxService.members(7, 12)).resolves.toEqual([member]);
    await expect(mailboxService.setMember(7, 12, 9, 'send')).resolves.toEqual(member);
    await expect(mailboxService.removeMember(7, 12, 9)).resolves.toBeUndefined();
    const events = await mailboxService.accessEvents(7, 12, { page: 2, perPage: 10 });
    expect(events.items).toHaveLength(1);
    expect(events.meta.current_page).toBe(2);
    await expect(mailboxService.handOver(7, 12, 9)).resolves.toMatchObject({ sealed_before: now });
    await expect(
      mailboxService.deleteSealedHistory(7, 12, 'Alice@mail.example.com')
    ).resolves.toEqual({ deleted_messages: 3, deleted_broadcasts: 1 });
    await expect(mailboxService.setWebhooks(7, 12, true)).resolves.toMatchObject({
      webhooks_enabled: true,
    });

    const header = { 'Organization-Id': '7' };
    expect(get.mock.calls).toEqual([['/mailboxes/12/members', { headers: header }]]);
    expect(getEnvelope.mock.calls).toEqual([
      ['/mailboxes/12/access-events', { headers: header, params: { page: 2, per_page: 10 } }],
    ]);
    expect(put.mock.calls).toEqual([
      ['/mailboxes/12/members/9', { permission: 'send' }, { headers: header }],
      ['/mailboxes/12/webhook-setting', { enabled: true }, { headers: header }],
    ]);
    expect(post.mock.calls).toEqual([
      ['/mailboxes/12/handovers', { successor_user_id: 9 }, { headers: header }],
    ]);
    expect(remove.mock.calls).toEqual([
      ['/mailboxes/12/members/9', { headers: header }],
      [
        '/mailboxes/12/sealed-messages',
        { headers: header, data: { confirm_address: 'Alice@mail.example.com' } },
      ],
    ]);
  });

  it('sends the access of a new mailbox with it', async () => {
    const post = vi.spyOn(request, 'post').mockResolvedValue(accessMailbox());
    await mailboxService.create(7, { local_part: 'me', type: 'personal', owner_user_id: 4 });
    await mailboxService.create(7, {
      local_part: 'team',
      type: 'shared',
      members: [
        { user_id: 4, permission: 'manage' },
        { user_id: 5, permission: 'view' },
      ],
    });
    expect(post.mock.calls.map(call => call[1])).toEqual([
      { local_part: 'me', type: 'personal', owner_user_id: 4 },
      {
        local_part: 'team',
        type: 'shared',
        members: [
          { user_id: 4, permission: 'manage' },
          { user_id: 5, permission: 'view' },
        ],
      },
    ]);
  });

  it('refuses invalid ids before any request', async () => {
    const spies = [
      vi.spyOn(request, 'get'),
      vi.spyOn(request, 'getEnvelope'),
      vi.spyOn(request, 'post'),
      vi.spyOn(request, 'put'),
      vi.spyOn(request, 'delete'),
    ];
    const calls = [
      () => mailboxService.members(0, 12),
      () => mailboxService.members(7, 0),
      () => mailboxService.setMember(7, 12, 0, 'view'),
      () => mailboxService.removeMember(7, 12, 1.5),
      () => mailboxService.accessEvents(7, 12, { page: 0 }),
      () => mailboxService.accessEvents(7, 12, { perPage: 101 }),
      () => mailboxService.handOver(7, 12, -1),
      () => mailboxService.deleteSealedHistory(7, Number.NaN, 'a@b.c'),
      () => mailboxService.setWebhooks(-7, 12, true),
    ];
    for (const call of calls) {
      await expect(call()).rejects.toMatchObject({ errorCode: ClientErrorCode.INVALID_RESPONSE });
    }
    for (const spy of spies) expect(spy).not.toHaveBeenCalled();
  });

  it('accepts contract-shaped access answers and rejects drift', () => {
    expect(mailboxListSchema.safeParse([accessMailbox()]).success).toBe(true);
    expect(
      mailboxListSchema.safeParse([
        accessMailbox({ type: 'personal', owner_user_id: 4, awaiting_handover: true }),
      ]).success
    ).toBe(true);
    for (const field of [
      'type',
      'owner_user_id',
      'awaiting_handover',
      'sealed_before',
      'webhooks_enabled',
      'access',
    ]) {
      const partial: Record<string, unknown> = accessMailbox();
      delete partial[field];
      expect(mailboxListSchema.safeParse([partial]).success, field).toBe(false);
    }
    for (const drift of [
      { type: 'group' },
      { access: { view: true, send: true, manage_members: true, administer: true } },
      { access: { ...accessMailbox().access, delete: true } },
      { sealed_before: 'yesterday' },
      { owner_user_id: 0 },
    ]) {
      expect(mailboxListSchema.safeParse([accessMailbox(drift)]).success).toBe(false);
    }
    expect(mailboxAccessEventSchema.safeParse(accessEvent()).success).toBe(true);
    // Clients ignore detail keys they do not know, and Zmail's own changes have no actor.
    expect(
      mailboxAccessEventSchema.safeParse(
        accessEvent({ actor_user_id: null, detail: { reason: 'left_organization', later: 1 } })
      ).success
    ).toBe(true);
    expect(mailboxAccessEventSchema.safeParse(accessEvent({ type: 'opened' })).success).toBe(false);
    expect(
      mailboxMemberSchema.safeParse({
        user_id: 9,
        permission: 'admin',
        granted_by: 3,
        created_at: now,
      }).success
    ).toBe(false);
  });

  it('maps the access error codes to local copy', () => {
    expect(
      resolveMailboxErrorKey(new ApiError('x', ApiErrorCode.MAILBOX_ACCESS_INVALID, 422))
    ).toBe('errors.accessInvalid');
    expect(
      resolveMailboxErrorKey(new ApiError('x', ApiErrorCode.MAILBOX_ACCESS_CONFLICT, 409))
    ).toBe('errors.accessConflict');
    expect(isAccessConflict(new ApiError('x', ApiErrorCode.MAILBOX_ACCESS_CONFLICT, 409))).toBe(
      true
    );
    expect(isAccessConflict(new ApiError('x', ApiErrorCode.MAILBOX_NOT_FOUND, 404))).toBe(false);
  });
});

const now = '2026-11-19T10:00:00Z';

function accessMailbox(overrides: Record<string, unknown> = {}) {
  return {
    id: 12,
    address: 'hello@mail.example.com',
    display_name: '',
    type: 'shared',
    owner_user_id: null,
    awaiting_handover: false,
    sealed_before: null,
    webhooks_enabled: false,
    access: {
      view: true,
      send: true,
      manage_members: true,
      administer: true,
      see_history: true,
      organize: true,
    },
    unread_count: 0,
    created_at: now,
    ...overrides,
  };
}

function accessEvent(overrides: Record<string, unknown> = {}) {
  return {
    id: 41,
    type: 'member_set',
    actor_user_id: 3,
    subject_user_id: 9,
    detail: { membership_id: 30, permission: 'view' },
    created_at: now,
    ...overrides,
  };
}

describe('stopping a broadcast without view on its sender', () => {
  afterEach(() => vi.restoreAllMocks());

  it('reports the new status when the API answers 204 with no body', async () => {
    vi.spyOn(request, 'post').mockResolvedValue('');
    await expect(mailboxService.controlBroadcast(3, 9, 'pauses')).resolves.toEqual({
      id: 9,
      status: 'paused',
    });
    await expect(mailboxService.controlBroadcast(3, 9, 'cancellations')).resolves.toEqual({
      id: 9,
      status: 'canceled',
    });
  });

  it('still requires a full broadcast when resuming', async () => {
    vi.spyOn(request, 'post').mockResolvedValue('');
    await expect(mailboxService.controlBroadcast(3, 9, 'resumptions')).rejects.toThrow();
  });
});

describe('mailbox organize service boundary', () => {
  afterEach(() => vi.restoreAllMocks());

  const folder = { id: 4, name: 'Invoices', created_at: now };
  const label = { id: 9, name: 'Urgent', color: 'red', created_at: now };
  const counts = {
    placements: { default_inbound_unread: 3, archive_unread: 0 },
    folders: [{ folder_id: 4, unread: 1 }],
    starred: 7,
  };
  const pageEnvelope = (data: unknown[]) => ({
    code: 0 as const,
    message: 'success',
    data,
    meta: { current_page: 1, per_page: 20, total: data.length, last_page: 1, from: 1, to: 1 },
    links: { first: '/a', last: '/a', prev: null, next: null },
  });

  it('calls the organize routes with the header and the documented bodies', async () => {
    const get = vi.spyOn(request, 'get').mockImplementation(async url => {
      if (url.endsWith('/mail-folders')) return [folder];
      if (url.endsWith('/mail-labels')) return [label];
      if (url.endsWith('/message-counts')) return counts;
      if (url.includes('/threads/')) return [message()];
      throw new Error(`unexpected ${url}`);
    });
    const post = vi.spyOn(request, 'post').mockImplementation(async url => {
      if (url.endsWith('/message-updates')) return { updated: 2 };
      if (url.endsWith('/trash-purges')) return { deleted: 5 };
      if (url.endsWith('/mail-folders')) return folder;
      if (url.endsWith('/mail-labels')) return label;
      throw new Error(`unexpected ${url}`);
    });
    const put = vi.spyOn(request, 'put').mockImplementation(async url => {
      if (url.includes('/mail-folders/')) return folder;
      if (url.includes('/mail-labels/')) return label;
      return message();
    });
    const remove = vi.spyOn(request, 'delete').mockResolvedValue(undefined);

    await expect(
      mailboxService.updateMessages(7, 12, {
        message_ids: [41, 42],
        placement: 'archive',
        add_label_ids: [9],
      })
    ).resolves.toEqual({ updated: 2 });
    await expect(mailboxService.purgeTrash(7, 12)).resolves.toEqual({ deleted: 5 });
    await expect(mailboxService.getThread(7, 12, 7)).resolves.toHaveLength(1);
    await expect(mailboxService.getMessageCounts(7, 12)).resolves.toEqual(counts);
    await expect(mailboxService.listFolders(7, 12)).resolves.toEqual([folder]);
    await expect(mailboxService.createFolder(7, 12, { name: 'Invoices' })).resolves.toEqual(folder);
    await expect(mailboxService.renameFolder(7, 12, 4, { name: 'Invoices' })).resolves.toEqual(
      folder
    );
    await mailboxService.deleteFolder(7, 12, 4);
    await expect(mailboxService.listLabels(7, 12)).resolves.toEqual([label]);
    await expect(
      mailboxService.createLabel(7, 12, { name: 'Urgent', color: 'red' })
    ).resolves.toEqual(label);
    await expect(
      mailboxService.updateLabel(7, 12, 9, { name: 'Urgent', color: 'red' })
    ).resolves.toEqual(label);
    await mailboxService.deleteLabel(7, 12, 9);
    await mailboxService.updateMessage(7, 12, 7, { starred: true, placement: 'trash' });

    expect(get.mock.calls.map(call => call[0])).toEqual([
      '/mailboxes/12/threads/7',
      '/mailboxes/12/message-counts',
      '/mailboxes/12/mail-folders',
      '/mailboxes/12/mail-labels',
    ]);
    expect(post.mock.calls.map(call => [call[0], call[1]])).toEqual([
      [
        '/mailboxes/12/message-updates',
        { message_ids: [41, 42], placement: 'archive', add_label_ids: [9] },
      ],
      ['/mailboxes/12/trash-purges', undefined],
      ['/mailboxes/12/mail-folders', { name: 'Invoices' }],
      ['/mailboxes/12/mail-labels', { name: 'Urgent', color: 'red' }],
    ]);
    expect(put.mock.calls.map(call => [call[0], call[1]])).toEqual([
      ['/mailboxes/12/mail-folders/4', { name: 'Invoices' }],
      ['/mailboxes/12/mail-labels/9', { name: 'Urgent', color: 'red' }],
      ['/mailboxes/12/messages/7', { starred: true, placement: 'trash' }],
    ]);
    expect(remove.mock.calls.map(call => call[0])).toEqual([
      '/mailboxes/12/mail-folders/4',
      '/mailboxes/12/mail-labels/9',
    ]);
    const configs = [
      ...get.mock.calls.map(call => call[1]),
      ...post.mock.calls.map(call => call[2]),
      ...put.mock.calls.map(call => call[2]),
      ...remove.mock.calls.map(call => call[1]),
    ];
    for (const config of configs) expect(config?.headers).toEqual({ 'Organization-Id': '7' });
  });

  it('sends the listing filters as the documented query', async () => {
    const getEnvelope = vi
      .spyOn(request, 'getEnvelope')
      .mockResolvedValue(pageEnvelope([threadRow()]));

    const page = await mailboxService.messages(7, 12, {
      placement: 'folder',
      folderId: 4,
      direction: 'inbound',
      group: 'thread',
      page: 2,
      perPage: 50,
    });
    expect(page.items[0].thread).toEqual(threadRow().thread);
    await mailboxService.messages(7, 12, {
      labelId: 9,
      starred: true,
      unread: false,
      hasAttachments: true,
      q: '  invoice  ',
    });
    await mailboxService.messages(7, 12, { direction: 'outbound' });

    expect(getEnvelope.mock.calls.map(call => call[1]?.params)).toEqual([
      {
        direction: 'inbound',
        placement: 'folder',
        folder_id: 4,
        group: 'thread',
        page: 2,
        per_page: 50,
      },
      {
        label_id: 9,
        starred: true,
        unread: false,
        has_attachments: true,
        q: 'invoice',
        page: 1,
        per_page: 20,
      },
      { direction: 'outbound', page: 1, per_page: 20 },
    ]);
  });

  it('refuses invalid ids, batches, and searches before any request', async () => {
    const spies = [
      vi.spyOn(request, 'get'),
      vi.spyOn(request, 'getEnvelope'),
      vi.spyOn(request, 'post'),
      vi.spyOn(request, 'put'),
      vi.spyOn(request, 'delete'),
    ];
    const ids = (count: number) => Array.from({ length: count }, (_, index) => index + 1);

    const calls = [
      () => mailboxService.updateMessages(7, 12, { message_ids: [], read: true }),
      () => mailboxService.updateMessages(7, 12, { message_ids: ids(101), read: true }),
      () => mailboxService.updateMessages(7, 12, { message_ids: [0], read: true }),
      () => mailboxService.updateMessages(7, 0, { message_ids: [1], read: true }),
      () => mailboxService.purgeTrash(0, 12),
      () => mailboxService.getThread(7, 12, 0),
      () => mailboxService.getMessageCounts(7, -1),
      () => mailboxService.listFolders(7, 1.5),
      () => mailboxService.renameFolder(7, 12, 0, { name: 'x' }),
      () => mailboxService.deleteFolder(7, 12, 0),
      () => mailboxService.updateLabel(7, 12, 0, { name: 'x', color: 'red' }),
      () => mailboxService.deleteLabel(7, 12, Number.NaN),
      () => mailboxService.messages(7, 12, { folderId: 0, placement: 'folder' }),
      () => mailboxService.messages(7, 12, { q: 'x'.repeat(201) }),
      () => mailboxService.messages(7, 12, { q: '   ' }),
    ];
    for (const call of calls) {
      await expect(call()).rejects.toMatchObject({ errorCode: ClientErrorCode.INVALID_RESPONSE });
    }
    for (const spy of spies) expect(spy).not.toHaveBeenCalled();
  });

  it('accepts contract-shaped organize answers and rejects drift', async () => {
    const get = vi.spyOn(request, 'get');
    const post = vi.spyOn(request, 'post');
    const reject = async (call: () => Promise<unknown>) =>
      expect(call()).rejects.toMatchObject({ errorCode: ClientErrorCode.INVALID_RESPONSE });

    get.mockResolvedValueOnce([{ ...label, color: 'black' }]);
    await reject(() => mailboxService.listLabels(7, 12));
    get.mockResolvedValueOnce([{ ...folder, color: 'red' }]);
    await reject(() => mailboxService.listFolders(7, 12));
    get.mockResolvedValueOnce({ ...counts, starred: -1 });
    await reject(() => mailboxService.getMessageCounts(7, 12));
    get.mockResolvedValueOnce({ ...counts, placements: { default_inbound_unread: 3 } });
    await reject(() => mailboxService.getMessageCounts(7, 12));
    // A thread lists full messages, never summaries.
    get.mockResolvedValueOnce([summary()]);
    await reject(() => mailboxService.getThread(7, 12, 7));
    post.mockResolvedValueOnce({ updated: '2' });
    await reject(() => mailboxService.updateMessages(7, 12, { message_ids: [1], read: true }));
    post.mockResolvedValueOnce({ deleted: 1, extra: true });
    await reject(() => mailboxService.purgeTrash(7, 12));
    // An answer about another folder than the one renamed is not the API's answer.
    vi.spyOn(request, 'put').mockResolvedValueOnce({ ...folder, id: 5 });
    await reject(() => mailboxService.renameFolder(7, 12, 4, { name: 'Invoices' }));
  });

  it('requires placements, stars, and threads on messages but not on log entries', () => {
    expect(mailMessageSummarySchema.safeParse(summary()).success).toBe(true);
    expect(mailMessageSummarySchema.safeParse(threadRow()).success).toBe(true);
    expect(
      mailMessageSchema.safeParse({ ...message(), placement: 'trash', trashed_at: now }).success
    ).toBe(true);
    for (const field of [
      'placement',
      'folder_id',
      'label_ids',
      'starred',
      'thread_id',
      'trashed_at',
    ]) {
      const partial: Record<string, unknown> = summary();
      delete partial[field];
      expect(mailMessageSummarySchema.safeParse(partial).success, field).toBe(false);
    }
    for (const drift of [
      { placement: 'spam' },
      { folder_id: 0 },
      { label_ids: [0] },
      { thread: { ...threadRow().thread, message_count: -1 } },
      { thread: { ...threadRow().thread, extra: 1 } },
    ]) {
      expect(mailMessageSummarySchema.safeParse({ ...summary(), ...drift }).success).toBe(false);
    }
    // Only listings group by thread; the detail never carries `thread`.
    expect(mailMessageSchema.safeParse({ ...message(), thread: threadRow().thread }).success).toBe(
      false
    );
    expect(
      mailLogEntrySchema.safeParse({ ...summary(), mailbox_address: 'hello@mail.example.com' })
        .success
    ).toBe(false);
    // The organize flag is a required part of the access answer.
    const withoutOrganize: Record<string, boolean> = { ...accessMailbox().access };
    delete withoutOrganize.organize;
    expect(mailboxListSchema.safeParse([accessMailbox({ access: withoutOrganize })]).success).toBe(
      false
    );
  });

  it('listInbox parses a cursor page', async () => {
    const meta = { per_page: 15, has_more: true, next_cursor: 'MTc2My40Mg' };
    const getEnvelope = vi.spyOn(request, 'getEnvelope').mockResolvedValue({
      code: 0,
      message: 'success',
      data: [inboxItem(), { ...inboxItem(), id: 8, thread: threadRow().thread }],
      meta,
    });

    const page = await mailboxService.listInbox(7, { view: 'all', group: 'thread', tz: 'UTC' });
    expect(page).toEqual({
      items: [inboxItem(), { ...inboxItem(), id: 8, thread: threadRow().thread }],
      meta,
    });
    await mailboxService.listInbox(
      7,
      { view: 'sent', roleId: 'none', mailboxId: 12, unread: false, tz: 'Asia/Shanghai' },
      'MTc2My40Mg',
      50
    );
    await mailboxService.listInbox(7, { view: 'all', roleId: 3, q: '  invoice  ', tz: 'UTC' });

    expect(getEnvelope.mock.calls.map(call => call[0])).toEqual([
      '/inbox-messages',
      '/inbox-messages',
      '/inbox-messages',
    ]);
    for (const call of getEnvelope.mock.calls) {
      expect(call[1]?.headers).toEqual({ 'Organization-Id': '7' });
    }
    expect(getEnvelope.mock.calls.map(call => call[1]?.params)).toEqual([
      { view: 'all', group: 'thread', tz: 'UTC' },
      {
        view: 'sent',
        role_id: 'none',
        mailbox_id: 12,
        unread: false,
        tz: 'Asia/Shanghai',
        cursor: 'MTc2My40Mg',
        per_page: 50,
      },
      { view: 'all', role_id: 3, q: 'invoice', tz: 'UTC' },
    ]);
  });

  it('inboxCounts sends only tz and parses the counts', async () => {
    const counts = {
      views: { all_unread: 4, today: 5, today_unread: 2, pending: 1, sent_waiting: 3 },
      roles: [{ role_id: 3, unread: 3 }],
      ungrouped_unread: 1,
      mailboxes: [
        { mailbox_id: 12, unread: 3 },
        { mailbox_id: 14, unread: 1 },
      ],
      today_starts_at: '2026-11-18T16:00:00Z',
      today_ends_at: '2026-11-19T16:00:00Z',
    };
    const get = vi.spyOn(request, 'get').mockResolvedValue(counts);

    expect(await mailboxService.inboxCounts(7, 'Asia/Shanghai')).toEqual(counts);
    expect(get).toHaveBeenCalledWith('/inbox-message-counts', {
      headers: { 'Organization-Id': '7' },
      params: { tz: 'Asia/Shanghai' },
    });

    for (const value of [
      { ...counts, views: { ...counts.views, today: -1 } },
      { ...counts, roles: [{ role_id: 0, unread: 1 }] },
      { ...counts, mailboxes: [{ mailbox_id: 12 }] },
      { ...counts, today_ends_at: 'tomorrow' },
      { ...counts, extra: true },
    ]) {
      get.mockResolvedValueOnce(value);
      await expect(mailboxService.inboxCounts(7, 'UTC')).rejects.toMatchObject({
        errorCode: ClientErrorCode.INVALID_RESPONSE,
      });
    }
    get.mockClear();
    for (const zone of ['', 'A'.repeat(65)]) {
      await expect(mailboxService.inboxCounts(7, zone)).rejects.toMatchObject({
        errorCode: ClientErrorCode.INVALID_RESPONSE,
      });
    }
    expect(get).not.toHaveBeenCalled();
  });

  it('rejects an item without mailbox_address', async () => {
    const getEnvelope = vi.spyOn(request, 'getEnvelope');
    const page = (
      data: unknown[],
      meta: unknown = { per_page: 15, has_more: false, next_cursor: null }
    ) => ({
      code: 0 as const,
      message: 'success',
      data,
      meta,
    });
    const reject = async () =>
      expect(mailboxService.listInbox(7, { view: 'all', tz: 'UTC' })).rejects.toMatchObject({
        errorCode: ClientErrorCode.INVALID_RESPONSE,
      });

    const withoutAddress: Record<string, unknown> = inboxItem();
    delete withoutAddress.mailbox_address;
    const withoutSnippet: Record<string, unknown> = inboxItem();
    delete withoutSnippet.snippet;
    const withoutRole: Record<string, unknown> = inboxItem();
    delete withoutRole.role_id;
    for (const item of [
      withoutAddress,
      withoutSnippet,
      withoutRole,
      { ...inboxItem(), role_id: 0 },
      { ...inboxItem(), text_body: 'x' },
      summary(),
    ]) {
      getEnvelope.mockResolvedValueOnce(page([item]));
      await reject();
    }
    // The cursor meta, not the offset one.
    getEnvelope.mockResolvedValueOnce(page([], { per_page: 15, has_more: false }));
    await reject();
    getEnvelope.mockResolvedValueOnce(
      page([], { current_page: 1, per_page: 15, total: 0, last_page: 1, from: 0, to: 0 })
    );
    await reject();
  });

  it('refuses invalid inbox filters before any request', async () => {
    const getEnvelope = vi.spyOn(request, 'getEnvelope');
    const calls = [
      () => mailboxService.listInbox(0, { view: 'all', tz: 'UTC' }),
      () => mailboxService.listInbox(7, { view: 'spam' as 'all', tz: 'UTC' }),
      () => mailboxService.listInbox(7, { view: 'all', mailboxId: 0, tz: 'UTC' }),
      () => mailboxService.listInbox(7, { view: 'all', roleId: -1, tz: 'UTC' }),
      () => mailboxService.listInbox(7, { view: 'all', q: '   ', tz: 'UTC' }),
      () => mailboxService.listInbox(7, { view: 'all', q: 'x'.repeat(201), tz: 'UTC' }),
      () => mailboxService.listInbox(7, { view: 'all', tz: '' }),
      () => mailboxService.listInbox(7, { view: 'all', tz: 'A'.repeat(65) }),
      () => mailboxService.listInbox(7, { view: 'all', tz: 'UTC' }, ''),
      () => mailboxService.listInbox(7, { view: 'all', tz: 'UTC' }, undefined, 0),
      () => mailboxService.listInbox(7, { view: 'all', tz: 'UTC' }, undefined, 101),
    ];
    for (const call of calls) {
      await expect(call()).rejects.toMatchObject({ errorCode: ClientErrorCode.INVALID_RESPONSE });
    }
    expect(getEnvelope).not.toHaveBeenCalled();
  });

  it('calls the mail role routes with the header and parses the answers', async () => {
    const role = mailRole();
    const get = vi.spyOn(request, 'get').mockResolvedValue([role]);
    const post = vi.spyOn(request, 'post').mockResolvedValue(role);
    const put = vi
      .spyOn(request, 'put')
      .mockImplementation(async url =>
        url.endsWith('/role-assignment') ? accessMailbox({ role_id: 3 }) : role
      );
    const remove = vi.spyOn(request, 'delete').mockResolvedValue(undefined);
    const input = { name: 'Design', color: 'orange' as const, description: '', owner_user_id: 7 };

    await expect(mailboxService.listRoles(7)).resolves.toEqual([role]);
    await expect(mailboxService.createRole(7, input)).resolves.toEqual(role);
    await expect(mailboxService.updateRole(7, 3, input)).resolves.toEqual(role);
    await mailboxService.deleteRole(7, 3);
    await expect(mailboxService.assignRole(7, 12, 3)).resolves.toMatchObject({
      id: 12,
      role_id: 3,
    });
    put.mockResolvedValueOnce(accessMailbox({ role_id: null }));
    await expect(mailboxService.assignRole(7, 12, null)).resolves.toMatchObject({ role_id: null });

    expect(get.mock.calls.map(call => call[0])).toEqual(['/mail-roles']);
    expect(post.mock.calls.map(call => [call[0], call[1]])).toEqual([['/mail-roles', input]]);
    expect(put.mock.calls.map(call => [call[0], call[1]])).toEqual([
      ['/mail-roles/3', input],
      ['/mailboxes/12/role-assignment', { role_id: 3 }],
      ['/mailboxes/12/role-assignment', { role_id: null }],
    ]);
    expect(remove.mock.calls.map(call => call[0])).toEqual(['/mail-roles/3']);
    const configs = [
      ...get.mock.calls.map(call => call[1]),
      ...post.mock.calls.map(call => call[2]),
      ...put.mock.calls.map(call => call[2]),
      ...remove.mock.calls.map(call => call[1]),
    ];
    for (const config of configs) expect(config?.headers).toEqual({ 'Organization-Id': '7' });

    // Drift, another role than the one written, or another mailbox, is an invalid response.
    const invalid = expect.objectContaining({ errorCode: ClientErrorCode.INVALID_RESPONSE });
    get.mockResolvedValueOnce([{ ...role, color: 'black' }]);
    await expect(mailboxService.listRoles(7)).rejects.toEqual(invalid);
    put.mockResolvedValueOnce({ ...role, id: 4 });
    await expect(mailboxService.updateRole(7, 3, input)).rejects.toEqual(invalid);
    put.mockResolvedValueOnce(accessMailbox({ id: 13, role_id: 3 }));
    await expect(mailboxService.assignRole(7, 12, 3)).rejects.toEqual(invalid);
    post.mockResolvedValueOnce({ ...role, mailbox_ids: undefined });
    await expect(mailboxService.createRole(7, input)).rejects.toEqual(invalid);

    // Invalid ids never leave the browser.
    const calls = [get, post, put, remove].map(spy => spy.mock.calls.length);
    for (const call of [
      () => mailboxService.updateRole(7, 0, input),
      () => mailboxService.deleteRole(7, Number.NaN),
      () => mailboxService.assignRole(7, 0, 3),
      () => mailboxService.assignRole(7, 12, -1),
      () => mailboxService.listRoles(0),
    ]) {
      await expect(call()).rejects.toEqual(invalid);
    }
    expect([get, post, put, remove].map(spy => spy.mock.calls.length)).toEqual(calls);
  });

  it('maps the mail role error codes to local copy', () => {
    expect(
      resolveMailboxErrorKey(new ApiError('x', ApiErrorCode.MAILBOX_ROLE_NOT_FOUND, 404))
    ).toBe('errors.roleNotFound');
    expect(
      resolveMailboxErrorKey(new ApiError('x', ApiErrorCode.MAILBOX_ROLE_NAME_TAKEN, 409))
    ).toBe('errors.roleNameTaken');
    expect(
      resolveMailboxErrorKey(new ApiError('x', ApiErrorCode.MAILBOX_ROLE_LIMIT_REACHED, 422))
    ).toBe('errors.roleLimitReached');
  });

  it('maps the organize error codes to local copy', () => {
    expect(
      resolveMailboxErrorKey(new ApiError('x', ApiErrorCode.MAILBOX_SEARCH_TOO_BROAD, 422))
    ).toBe('errors.searchTooBroad');
    expect(
      resolveMailboxErrorKey(new ApiError('x', ApiErrorCode.MAILBOX_FOLDER_NAME_TAKEN, 409))
    ).toBe('errors.folderNameTaken');
    expect(
      resolveMailboxErrorKey(new ApiError('x', ApiErrorCode.MAILBOX_LABEL_LIMIT_REACHED, 422))
    ).toBe('errors.labelLimitReached');
  });
});

function threadRow() {
  return {
    ...summary(),
    thread: { message_count: 2, message_ids: [3, 7], unread: true, starred: false, label_ids: [9] },
  };
}

/** A unified inbox row: a listing summary with the owning address, the mail role, and a snippet. */
function inboxItem() {
  return {
    ...summary(),
    mailbox_address: 'hello@mail.example.com',
    role_id: null,
    snippet: 'Hi Bob, the invoice is attached.',
  };
}

function mailRole(overrides: Record<string, unknown> = {}) {
  return {
    id: 3,
    name: 'Design',
    color: 'orange',
    description: '',
    owner_user_id: 7,
    mailbox_ids: [12, 14],
    created_at: now,
    updated_at: now,
    ...overrides,
  };
}
