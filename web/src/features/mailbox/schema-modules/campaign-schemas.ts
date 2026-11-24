import {
  _default,
  array,
  boolean,
  literal,
  looseObject,
  nonnegative,
  nullable,
  number,
  optional,
  record,
  strictObject,
  string,
  union,
} from 'zod/mini';

import {
  mailEventSummarySchema,
  nonnegativeIntegerSchema,
  pageLinksSchema,
  pageMetaSchema,
  positiveIntegerSchema,
  safeIdSchema,
  templateVariablesSchema,
  timestampSchema,
} from './shared-schemas';

export const MAIL_TEMPLATE_NAME_MAX_LENGTH = 100;
export const MAIL_TEMPLATE_MAX_PER_ORGANIZATION = 200;
export const MAIL_TEMPLATE_MAX_VARIABLES = 100;
/** The visual editor's design document, as stored by the API. */
export const MAIL_TEMPLATE_DESIGN_MAX_BYTES = 256 * 1_024;
export const MAIL_TEMPLATE_VALUE_MAX_LENGTH = 2_000;
export const MAIL_TEMPLATE_TEST_MAX_RECIPIENTS = 5;
export const MAIL_CONTACT_NAME_MAX_LENGTH = 100;
export const MAIL_CONTACT_MAX_PROPERTIES = 50;
export const MAIL_CONTACT_PROPERTY_MAX_LENGTH = 500;
export const MAIL_CONTACT_LIST_NAME_MAX_LENGTH = 100;
export const MAIL_CONTACT_MAX_LISTS = 100;
export const MAIL_CONTACT_IMPORT_MAX_BYTES = 5 * 1_024 * 1_024;
export const MAIL_CONTACT_IMPORT_MAX_ROWS = 10_000;
/** Contact fields with a fixed meaning; they cannot be custom properties. */
export const MAIL_BROADCAST_NAME_MAX_LENGTH = 100;
export const MAIL_BROADCAST_TEST_MAX_PERCENT = 50;
export const MAIL_BROADCAST_TEST_WAIT_DEFAULT_MINUTES = 30;
export const MAIL_BROADCAST_TEST_WAIT_MIN_MINUTES = 5;
export const MAIL_BROADCAST_TEST_WAIT_MAX_MINUTES = 1_440;
export const MAIL_BROADCAST_LABEL_MAX_LENGTH = 50;
/** An unsubscribe token: organization id, contact id, and a 16-byte base64url signature. */
export const MAIL_UNSUBSCRIBE_TOKEN_PATTERN = /^[1-9]\d{0,9}\.[1-9]\d{0,9}\.[A-Za-z0-9_-]{22}$/u;
export const MAIL_CONTACT_RESERVED_FIELDS = ['email', 'first_name', 'last_name'] as const;
/** Filled from each contact on every broadcast message; a broadcast cannot set them. */
export const MAIL_BROADCAST_CONTACT_VARIABLES = [
  ...MAIL_CONTACT_RESERVED_FIELDS,
  'unsubscribe_url',
] as const;
/** A variable name: a letter, then letters, digits, or underscores. */
export const MAIL_TEMPLATE_VARIABLE_PATTERN = /^[A-Za-z][A-Za-z0-9_]{0,63}$/u;

export const mailTemplateKindSchema = union([literal('standard'), literal('transactional')]);

export const mailTemplateVariableDefinitionSchema = strictObject({
  name: string(),
  secret: boolean(),
  required: boolean(),
});

/** The sender of a transactional template; null for a standard template. */
export const mailTemplateSenderSchema = strictObject({ address: string(), name: string() });

export const mailTemplateSummarySchema = strictObject({
  id: safeIdSchema,
  name: string(),
  subject: string(),
  variables: array(string()),
  created_at: timestampSchema,
  updated_at: timestampSchema,
  kind: mailTemplateKindSchema,
  key: nullable(string()),
  from: nullable(mailTemplateSenderSchema),
  variable_definitions: array(mailTemplateVariableDefinitionSchema),
  // True only for a developer whose sending scope no longer covers this transactional
  // template's sender; the template is then read-only for them. Older APIs omit it.
  outside_sending_scope: _default(boolean(), false),
});
export const mailTemplateListSchema = array(mailTemplateSummarySchema);

/** The contract checks only the version; the editor validates the rest of the document. */
export const mailTemplateDesignSchema = looseObject({ version: number() });

export const mailTemplateSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  subject: string(),
  variables: array(string()),
  created_at: timestampSchema,
  updated_at: timestampSchema,
  html_body: string(),
  text_body: string(),
  sample_variables: templateVariablesSchema,
  // The visual editor reads it with its own parser; the API stores it as given.
  design: nullable(mailTemplateDesignSchema),
  kind: mailTemplateKindSchema,
  key: nullable(string()),
  from: nullable(mailTemplateSenderSchema),
  variable_definitions: array(mailTemplateVariableDefinitionSchema),
  // True only for a developer whose sending scope no longer covers this transactional
  // template's sender; the template is then read-only for them. Older APIs omit it.
  outside_sending_scope: _default(boolean(), false),
});

export const mailTemplateInputSchema = strictObject({
  name: string(),
  subject: string(),
  html: optional(string()),
  text: optional(string()),
  sample_variables: optional(templateVariablesSchema),
  design: optional(nullable(mailTemplateDesignSchema)),
  kind: optional(mailTemplateKindSchema),
  key: optional(string()),
  from_address: optional(string()),
  from_name: optional(string()),
  variable_definitions: optional(array(mailTemplateVariableDefinitionSchema)),
});

export const mailTemplateTestInputSchema = strictObject({
  mailbox_id: safeIdSchema,
  to: array(string()),
  variables: optional(templateVariablesSchema),
});

export const mailContactStatusSchema = union([
  literal('subscribed'),
  literal('unsubscribed'),
  literal('suppressed'),
]);

export const mailContactSchema = strictObject({
  id: safeIdSchema,
  email: string(),
  first_name: string(),
  last_name: string(),
  properties: record(string(), string()),
  status: mailContactStatusSchema,
  status_reason: string(),
  list_ids: array(safeIdSchema),
  created_at: timestampSchema,
  updated_at: timestampSchema,
});

export const mailContactPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailContactSchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

export const mailContactListSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  contact_count: nonnegativeIntegerSchema,
  created_at: timestampSchema,
});
export const mailContactListListSchema = array(mailContactListSchema);

export const mailContactImportSkipReasonSchema = union([
  literal('invalid_email'),
  literal('duplicate'),
  literal('invalid_name'),
  literal('invalid_property'),
  literal('too_many_properties'),
]);

export const mailContactImportSchema = strictObject({
  rows: nonnegativeIntegerSchema,
  created: nonnegativeIntegerSchema,
  updated: nonnegativeIntegerSchema,
  skipped_count: nonnegativeIntegerSchema,
  skipped: array(
    strictObject({ line: positiveIntegerSchema, reason: mailContactImportSkipReasonSchema })
  ),
  ignored_columns: array(string()),
});

export const mailContactInputSchema = strictObject({
  email: optional(string()),
  first_name: optional(string()),
  last_name: optional(string()),
  properties: optional(record(string(), string())),
  status: optional(union([literal('subscribed'), literal('unsubscribed')])),
  list_ids: optional(array(safeIdSchema)),
});

export const mailContactImportInputSchema = strictObject({
  csv: string(),
  list_id: optional(safeIdSchema),
});

export const mailContactListInputSchema = strictObject({ name: string() });

export const mailBroadcastStatusSchema = union([
  literal('draft'),
  literal('scheduled'),
  literal('sending'),
  literal('paused'),
  literal('sent'),
  literal('canceled'),
]);

export const mailRecipientStatusSchema = union([
  literal('pending'),
  literal('sent'),
  literal('failed'),
  literal('skipped'),
  literal('canceled'),
]);

/**
 * What a caller without `view` on the sender mailbox, or of a sealed broadcast, knows after pausing
 * or cancelling: the API answers 204, so only the new status.
 */
export const mailBroadcastStatusOnlySchema = strictObject({
  id: safeIdSchema,
  status: mailBroadcastStatusSchema,
});

/** How fast a running broadcast sends and when it should finish, estimated by the API. */
export const mailBroadcastProgressSchema = strictObject({
  rate_per_minute: nullable(number().check(nonnegative())),
  rate_window_minutes: positiveIntegerSchema,
  estimated_finish_at: nullable(timestampSchema),
  send_limit_pauses: boolean(),
  send_limit_resumes_at: nullable(timestampSchema),
  computed_at: timestampSchema,
});

export const mailBroadcastSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  mailbox_id: safeIdSchema,
  template_id: safeIdSchema,
  list_id: safeIdSchema,
  unsubscribe_label: string(),
  // Template values shared by every recipient; a contact's properties override them.
  variables: templateVariablesSchema,
  status: mailBroadcastStatusSchema,
  scheduled_at: nullable(timestampSchema),
  started_at: nullable(timestampSchema),
  finished_at: nullable(timestampSchema),
  stop_reason: string(),
  // Staged sending: test_percent of the recipients go first; the rest waits test_wait_minutes.
  test_percent: nonnegativeIntegerSchema,
  test_wait_minutes: nonnegativeIntegerSchema,
  current_wave: positiveIntegerSchema,
  resume_at: nullable(timestampSchema),
  // An owner resumed the broadcast after the health check paused it.
  safety_override: boolean(),
  counts: strictObject({
    total: nonnegativeIntegerSchema,
    pending: nonnegativeIntegerSchema,
    sent: nonnegativeIntegerSchema,
    failed: nonnegativeIntegerSchema,
    skipped: nonnegativeIntegerSchema,
    canceled: nonnegativeIntegerSchema,
    bounced: nonnegativeIntegerSchema,
    complained: nonnegativeIntegerSchema,
  }),
  // Send rate and estimated finish of one running broadcast; null in the listing. Optional so the
  // console keeps working against an API that predates it.
  progress: optional(nullable(mailBroadcastProgressSchema)),
  created_at: timestampSchema,
  updated_at: timestampSchema,
});

export const mailBroadcastPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailBroadcastSchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

export const mailBroadcastRecipientSchema = strictObject({
  id: safeIdSchema,
  contact_id: safeIdSchema,
  email: string(),
  status: mailRecipientStatusSchema,
  message_id: nullable(safeIdSchema),
  failure_code: string(),
  last_event: mailEventSummarySchema,
  contact_status: nullable(mailContactStatusSchema),
  updated_at: timestampSchema,
});

export const mailBroadcastRecipientPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(mailBroadcastRecipientSchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

export const mailBroadcastInputSchema = strictObject({
  name: string(),
  mailbox_id: safeIdSchema,
  template_id: safeIdSchema,
  list_id: safeIdSchema,
  unsubscribe_label: optional(string()),
  test_percent: optional(nonnegativeIntegerSchema),
  test_wait_minutes: optional(nonnegativeIntegerSchema),
  variables: optional(templateVariablesSchema),
});

export const mailBroadcastSendInputSchema = strictObject({
  scheduled_at: optional(nullable(string())),
});
