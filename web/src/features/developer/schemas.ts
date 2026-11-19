import {
  array,
  boolean,
  int,
  iso,
  literal,
  maximum,
  nonnegative,
  nullable,
  number,
  positive,
  record,
  strictObject,
  string,
  union,
} from 'zod/mini';

import { mailEventTypeSchema } from '@/features/mailbox/schemas';

/** Limits of `contracts/MAILBOXES.md`, Transactional Mail. */
export const TRANSACTIONAL_TEMPLATE_KEY_PATTERN = /^[a-z][a-z0-9-]{0,63}$/u;
export const TRANSACTIONAL_LOG_DEFAULT_PAGE_SIZE = 50;
export const TRANSACTIONAL_LOG_MAX_PAGE_SIZE = 100;
export const TRANSACTIONAL_LOG_RECIPIENT_MAX_LENGTH = 320;
export const TRANSACTIONAL_LOG_DEFAULT_RANGE_DAYS = 7;
export const TRANSACTIONAL_LOG_MAX_RANGE_DAYS = 30;
/** The log's cursor is the id of the last entry of the previous page. */
export const TRANSACTIONAL_LOG_CURSOR_PATTERN = /^[1-9]\d{0,15}$/u;
/** Console test sends one user may make per minute. */
export const TRANSACTIONAL_TEST_SENDS_PER_MINUTE = 5;

export const TRANSACTIONAL_MODES = ['live', 'test'] as const;
export const TRANSACTIONAL_STATUSES = ['queued', 'sent', 'failed', 'suppressed'] as const;
/** The `last_event` values the log filters by; `accepted` is not one. */
export const TRANSACTIONAL_LOG_EVENTS = [
  'delivered',
  'deferred',
  'bounced',
  'failed',
  'rejected',
  'complained',
] as const;
export const TRANSACTIONAL_OUTCOMES = ['delivered', 'problem'] as const;

const safeIdSchema = number().check(int(), positive(), maximum(Number.MAX_SAFE_INTEGER));
const timestampSchema = iso.datetime({ offset: true });
const nonnegativeIntegerSchema = number().check(int(), nonnegative());

export const transactionalModeSchema = union([literal('live'), literal('test')]);
export const transactionalStatusSchema = union([
  literal('queued'),
  literal('sent'),
  literal('failed'),
  literal('suppressed'),
]);
export const transactionalQueuedReasonSchema = nullable(
  union([literal('send_limit'), literal('retry')])
);
/** The most severe recorded delivery event; null when none was recorded. */
export const transactionalLastEventSchema = nullable(mailEventTypeSchema);

export const transactionalSenderSchema = strictObject({ address: string(), name: string() });

export const transactionalMessageEventSchema = strictObject({
  type: mailEventTypeSchema,
  detail: string(),
  occurred_at: timestampSchema,
});

const transactionalVariablesSchema = record(string(), string());

/** Fields every view of a transactional message shares; none holds the full recipient. */
const sharedFields = {
  id: safeIdSchema,
  mode: transactionalModeSchema,
  simulated: boolean(),
  template: string(),
  to_masked: string(),
  subject: string(),
  status: transactionalStatusSchema,
  queued_reason: transactionalQueuedReasonSchema,
  failure_code: nullable(string()),
  last_event: transactionalLastEventSchema,
  idempotency_key: nullable(string()),
  created_at: timestampSchema,
  sent_at: nullable(timestampSchema),
};

/** One row of the console log: never the full recipient or the variables. */
export const transactionalLogEntrySchema = strictObject(sharedFields);

/** One log entry with its sender, masked variables, and events; still without the recipient. */
export const transactionalLogEntryDetailSchema = strictObject({
  ...sharedFields,
  from: transactionalSenderSchema,
  variables: transactionalVariablesSchema,
  events: array(transactionalMessageEventSchema),
});

export const transactionalLogPageMetaSchema = strictObject({
  next_cursor: nullable(string()),
  total_24h: nonnegativeIntegerSchema,
});

export const transactionalLogPageSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(transactionalLogEntrySchema),
  meta: transactionalLogPageMetaSchema,
});

/** A console test send as the API answers it: the message, including the caller's address. */
export const transactionalMessageSchema = strictObject({
  ...sharedFields,
  from: transactionalSenderSchema,
  to: string(),
  variables: transactionalVariablesSchema,
  attempts: nonnegativeIntegerSchema,
  events: array(transactionalMessageEventSchema),
});
