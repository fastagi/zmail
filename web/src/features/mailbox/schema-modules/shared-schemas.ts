import {
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
  regex,
  strictObject,
  string,
  union,
} from 'zod/mini';

export const MAIL_PAGE_MAX_SIZE = 100;
export const MAIL_LOG_QUERY_MAX_LENGTH = 200;
/** A mailbox search is at most this many characters as sent, then trimmed and not empty. */
export const MAIL_SEARCH_MAX_LENGTH = 200;

export const safeIdSchema = number().check(int(), positive(), maximum(Number.MAX_SAFE_INTEGER));
export const timestampSchema = iso.datetime({ offset: true });
export const positiveIntegerSchema = number().check(int(), positive());
export const nonnegativeIntegerSchema = number().check(int(), nonnegative());

export const mailDirectionSchema = union([literal('inbound'), literal('outbound')]);
export const mailStatusSchema = union([
  literal('received'),
  literal('queued'),
  literal('sent'),
  literal('failed'),
]);

/** What happened to an outbound message after the provider took it; one event per recipient. */
export const mailEventTypeSchema = union([
  literal('accepted'),
  literal('delivered'),
  literal('deferred'),
  literal('bounced'),
  literal('failed'),
  literal('rejected'),
  literal('complained'),
]);

/** The most severe current state among the recipients; '' when no event is recorded. */
export const mailEventSummarySchema = union([literal(''), mailEventTypeSchema]);

/** `recipient` is '' for a message-level event; `detail` holds short machine-readable tokens. */
export const mailEventSchema = strictObject({
  type: mailEventTypeSchema,
  recipient: string(),
  detail: string(),
  occurred_at: timestampSchema,
});

export const mailAddressSchema = strictObject({ address: string(), name: string() });

export const mailLabelColorSchema = union([
  literal('gray'),
  literal('red'),
  literal('orange'),
  literal('yellow'),
  literal('green'),
  literal('teal'),
  literal('blue'),
  literal('purple'),
  literal('pink'),
]);

export const MAIL_LABEL_COLORS = [
  'gray',
  'red',
  'orange',
  'yellow',
  'green',
  'teal',
  'blue',
  'purple',
  'pink',
] as const;

/**
 * `zmail` for Zmail's own mail; otherwise the id of the connector that synced a read-only copy of a
 * bound account, such as `gmail` or `outlook`. New connectors add ids without a contract change.
 */
export const mailSourceSchema = string().check(regex(/^[a-z][a-z0-9_]{1,15}$/u));

export const pageMetaSchema = strictObject({
  current_page: positiveIntegerSchema,
  per_page: positiveIntegerSchema,
  total: nonnegativeIntegerSchema,
  last_page: positiveIntegerSchema,
  from: nonnegativeIntegerSchema,
  to: nonnegativeIntegerSchema,
});

export const pageLinksSchema = strictObject({
  first: string(),
  last: string(),
  prev: nullable(string()),
  next: nullable(string()),
});

export const cursorPageMetaSchema = strictObject({
  per_page: positiveIntegerSchema,
  has_more: boolean(),
  next_cursor: nullable(string()),
});

export const templateVariablesSchema = record(string(), string());
