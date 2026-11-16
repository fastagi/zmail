import { describe, expect, it } from 'vitest';

import {
  mailMessagePageEnvelopeSchema,
  mailboxDomainListSchema,
  mailboxListSchema,
  normalizeDisplayName,
  normalizeLocalPart,
  normalizeMailMessage,
  normalizeRecipient,
  splitRecipients,
} from '@/features/mailbox/schemas';
import { parseMailMessage } from '@/features/mailbox/services/mailbox-service';
import {
  isStoredSendFailure,
  resolveMailboxErrorKey,
} from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode, ClientErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';

describe('mailbox service boundary', () => {
  it('accepts contract-shaped messages and rejects drift', () => {
    expect(parseMailMessage(message(), 7)).toMatchObject({ id: 7, status: 'received' });
    expect(() => parseMailMessage(message(), 8)).toThrow(ApiError);
    expect(() => parseMailMessage({ ...message(), extra: true })).toThrow(ApiError);
    expect(() => parseMailMessage({ ...message(), status: 'bounced' })).toThrow(ApiError);
    expect(() => parseMailMessage({ ...message(), html_body: undefined })).toThrow(ApiError);
  });

  it('accepts domains, mailboxes, and pages without bodies', () => {
    expect(
      mailboxDomainListSchema.safeParse([{ domain: 'mail.example.com', max_mailboxes: 10 }]).success
    ).toBe(true);
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
  };
}
