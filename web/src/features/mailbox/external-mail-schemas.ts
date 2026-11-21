import {
  array,
  boolean,
  enum as zodEnum,
  literal,
  looseObject,
  nullable,
  regex,
  string,
  strictObject,
  type infer as Infer,
} from 'zod/mini';

import { safeIdSchema, timestampSchema } from '@/features/mailbox/schemas';

/**
 * External mailboxes (`contracts/MAILBOXES.md`, External Mailboxes): a member binds an outside
 * account read-only through a connector, and its mail is copied into an `external` mailbox only
 * they read.
 */

/** How far back a new binding first reads; the contract allows 1 to 90 and defaults to 14. */
export const EXTERNAL_MAIL_BACKFILL_CHOICES = [14, 30, 90] as const;
export type ExternalMailBackfillDays = (typeof EXTERNAL_MAIL_BACKFILL_CHOICES)[number];
export const EXTERNAL_MAIL_DEFAULT_BACKFILL_DAYS: ExternalMailBackfillDays = 14;

/** Connector ids match this pattern; new connectors add ids without a contract change. */
export const EXTERNAL_MAIL_CONNECTOR_ID_PATTERN = /^[a-z][a-z0-9_]{1,15}$/u;

export const externalMailConnectorIdSchema = string().check(
  regex(EXTERNAL_MAIL_CONNECTOR_ID_PATTERN)
);

/**
 * One connector of the instance. `auth_kind` is read as any text so a later kind (an app password,
 * say) arrives intact and is offered as "coming soon" rather than failing the whole list.
 */
export const externalMailConnectorSchema = looseObject({
  id: externalMailConnectorIdSchema,
  auth_kind: string(),
  capabilities: looseObject({ read: boolean() }),
});
export const externalMailConnectorListSchema = array(externalMailConnectorSchema);

export const EXTERNAL_MAIL_CONNECTION_STATUSES = ['active', 'needs_reconnect', 'revoked'] as const;
export const externalMailConnectionStatusSchema = zodEnum(EXTERNAL_MAIL_CONNECTION_STATUSES);

/** One of the caller's bound accounts; it never carries a token. */
export const externalMailConnectionSchema = strictObject({
  id: safeIdSchema,
  provider: externalMailConnectorIdSchema,
  account_address: string(),
  mailbox_id: safeIdSchema,
  status: externalMailConnectionStatusSchema,
  last_synced_at: nullable(timestampSchema),
  last_error_code: nullable(string()),
  created_at: timestampSchema,
});
export const externalMailConnectionListSchema = array(externalMailConnectionSchema);

/** The pending sign-in: the provider page to send the browser to, valid for 10 minutes. */
export const externalMailAuthorizationSchema = strictObject({
  authorization_url: string(),
  expires_at: timestampSchema,
});

export const externalMailAuthorizationEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: externalMailAuthorizationSchema,
});

export type ExternalMailConnector = Infer<typeof externalMailConnectorSchema>;
export type ExternalMailConnection = Infer<typeof externalMailConnectionSchema>;
export type ExternalMailConnectionStatus = Infer<typeof externalMailConnectionStatusSchema>;
export type ExternalMailAuthorization = Infer<typeof externalMailAuthorizationSchema>;

/** Start a new binding with a connector, or reconnect one of the caller's connections. */
export type StartExternalMailAuthorizationInput =
  | { provider: string; backfill_days?: number }
  | { connection_id: number };

export interface CompleteExternalMailAuthorizationInput {
  state: string;
  code: string;
}

/** `created` is a new mailbox (`201`); `restored` is the caller's own account again (`200`). */
export interface ExternalMailCompletion {
  outcome: 'created' | 'restored';
  connection: ExternalMailConnection;
}
