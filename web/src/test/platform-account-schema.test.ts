import { describe, expect, it } from 'vitest';

import {
  PLATFORM_ACCOUNT_STATUSES,
  platformAccountEventPageEnvelopeSchema,
  platformAccountHandoverSchema,
  platformAccountInputSchema,
  platformAccountPageEnvelopeSchema,
  platformAccountSchema,
} from '@/features/mailbox/platform-account-schemas';

const account = {
  id: 12,
  platform: 'Medium',
  handle: '@acme',
  profile_url: 'https://medium.com/@acme',
  role_id: 3,
  mailbox_id: 14,
  mailbox_address: 'design@mail.example.com',
  owner_user_id: 7,
  status: 'active',
  notes: '',
  created_at: '2026-11-20T10:00:00Z',
  updated_at: '2026-11-20T10:00:00Z',
};

const meta = { current_page: 1, per_page: 15, total: 1, last_page: 1, from: 1, to: 1 };
const links = { first: '/v1/x?page=1', last: '/v1/x?page=1', prev: null, next: null };

describe('platform account schemas', () => {
  it('accepts the contract example and every nullable reference as null', () => {
    expect(platformAccountSchema.safeParse(account).success).toBe(true);
    expect(
      platformAccountSchema.safeParse({
        ...account,
        profile_url: null,
        role_id: null,
        mailbox_id: null,
        mailbox_address: null,
        owner_user_id: null,
      }).success
    ).toBe(true);
  });

  it('rejects unknown statuses, unknown fields, and unsafe ids', () => {
    expect(platformAccountSchema.safeParse({ ...account, status: 'lost' }).success).toBe(false);
    expect(platformAccountSchema.safeParse({ ...account, extra: true }).success).toBe(false);
    expect(platformAccountSchema.safeParse({ ...account, id: 0 }).success).toBe(false);
    expect(
      platformAccountSchema.safeParse({ ...account, owner_user_id: Number.MAX_SAFE_INTEGER + 1 })
        .success
    ).toBe(false);
  });

  it('lists the six statuses of the contract', () => {
    expect(PLATFORM_ACCOUNT_STATUSES).toEqual([
      'invited',
      'awaiting_confirmation',
      'active',
      'timed_out',
      'needs_review',
      'retired',
    ]);
  });

  it('parses the paginated ledger and history envelopes', () => {
    expect(
      platformAccountPageEnvelopeSchema.safeParse({
        code: 0,
        message: 'success',
        data: [account],
        meta,
        links,
      }).success
    ).toBe(true);
    const event = {
      id: 40,
      account_id: 12,
      kind: 'owner_changed',
      actor_user_id: 1,
      from: '7',
      to: '9',
      created_at: '2026-11-21T09:00:00Z',
    };
    expect(
      platformAccountEventPageEnvelopeSchema.safeParse({
        code: 0,
        message: 'success',
        data: [event, { ...event, id: 41, kind: 'created', from: null, actor_user_id: null }],
        meta,
        links,
      }).success
    ).toBe(true);
    expect(
      platformAccountEventPageEnvelopeSchema.safeParse({
        code: 0,
        message: 'success',
        data: [{ ...event, kind: 'renamed' }],
        meta,
        links,
      }).success
    ).toBe(false);
  });

  it('parses a handover result, including one that moved nothing', () => {
    expect(
      platformAccountHandoverSchema.safeParse({ from_user_id: 7, to_user_id: 9, account_ids: [] })
        .success
    ).toBe(true);
    expect(
      platformAccountHandoverSchema.safeParse({ from_user_id: 7, to_user_id: 9, account_ids: [0] })
        .success
    ).toBe(false);
  });

  it('keeps the input to the contract fields with optional defaults', () => {
    expect(platformAccountInputSchema.safeParse({ platform: 'X', handle: 'acme' }).success).toBe(
      true
    );
    expect(
      platformAccountInputSchema.safeParse({
        platform: 'X',
        handle: 'acme',
        role_id: null,
        mailbox_id: 3,
        owner_user_id: null,
        status: 'retired',
        profile_url: '',
        notes: 'n',
      }).success
    ).toBe(true);
    expect(platformAccountInputSchema.safeParse({ handle: 'acme' }).success).toBe(false);
  });
});
