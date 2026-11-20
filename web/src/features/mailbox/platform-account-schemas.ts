import {
  array,
  enum as zodEnum,
  literal,
  nullable,
  optional,
  strictObject,
  string,
  type infer as Infer,
} from 'zod/mini';

import {
  pageLinksSchema,
  pageMetaSchema,
  safeIdSchema,
  timestampSchema,
} from '@/features/mailbox/schemas';

/** Limits from `contracts/MAILBOXES.md`, section Platform Accounts. */
export const PLATFORM_NAME_MAX_LENGTH = 64;
export const PLATFORM_HANDLE_MAX_LENGTH = 200;
export const PLATFORM_PROFILE_URL_MAX_LENGTH = 2_048;
export const PLATFORM_ACCOUNT_NOTES_MAX_LENGTH = 2_000;
export const PLATFORM_ACCOUNT_QUERY_MAX_LENGTH = 100;
export const PLATFORM_ACCOUNT_PAGE_MAX_SIZE = 100;

export const PLATFORM_ACCOUNT_STATUSES = [
  'invited',
  'awaiting_confirmation',
  'active',
  'timed_out',
  'needs_review',
  'retired',
] as const;

export const PLATFORM_ACCOUNT_EVENT_KINDS = [
  'created',
  'status_changed',
  'owner_changed',
  'mailbox_changed',
  'deleted',
] as const;

export const platformAccountStatusSchema = zodEnum(PLATFORM_ACCOUNT_STATUSES);
export const platformAccountEventKindSchema = zodEnum(PLATFORM_ACCOUNT_EVENT_KINDS);

/**
 * One ledger entry. `mailbox_address` is null without a live registration mailbox or when the
 * caller cannot see it; `owner_user_id` may name a user who left the organization.
 */
export const platformAccountSchema = strictObject({
  id: safeIdSchema,
  platform: string(),
  handle: string(),
  profile_url: nullable(string()),
  role_id: nullable(safeIdSchema),
  mailbox_id: nullable(safeIdSchema),
  mailbox_address: nullable(string()),
  owner_user_id: nullable(safeIdSchema),
  status: platformAccountStatusSchema,
  notes: string(),
  created_at: timestampSchema,
  updated_at: timestampSchema,
});

export const platformAccountPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(platformAccountSchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

/** One history entry; `from` and `to` are a status or an id as text, null for none. */
export const platformAccountEventSchema = strictObject({
  id: safeIdSchema,
  account_id: safeIdSchema,
  kind: platformAccountEventKindSchema,
  actor_user_id: nullable(safeIdSchema),
  from: nullable(string()),
  to: nullable(string()),
  created_at: timestampSchema,
});

export const platformAccountEventPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(platformAccountEventSchema),
  meta: pageMetaSchema,
  links: pageLinksSchema,
});

/** Every field of an account; an omitted one takes its default (no references, active, empty). */
export const platformAccountInputSchema = strictObject({
  platform: string(),
  handle: string(),
  profile_url: optional(string()),
  role_id: optional(nullable(safeIdSchema)),
  mailbox_id: optional(nullable(safeIdSchema)),
  owner_user_id: optional(nullable(safeIdSchema)),
  status: optional(platformAccountStatusSchema),
  notes: optional(string()),
});

export const platformAccountHandoverInputSchema = strictObject({
  from_user_id: safeIdSchema,
  to_user_id: safeIdSchema,
});

/** The accounts a handover moved, ascending; possibly none. */
export const platformAccountHandoverSchema = strictObject({
  from_user_id: safeIdSchema,
  to_user_id: safeIdSchema,
  account_ids: array(safeIdSchema),
});

export type PlatformAccountStatus = Infer<typeof platformAccountStatusSchema>;
export type PlatformAccountEventKind = Infer<typeof platformAccountEventKindSchema>;
export type PlatformAccount = Infer<typeof platformAccountSchema>;
export type PlatformAccountPage = Infer<typeof platformAccountPageEnvelopeSchema>;
export type PlatformAccountEvent = Infer<typeof platformAccountEventSchema>;
export type PlatformAccountEventPage = Infer<typeof platformAccountEventPageEnvelopeSchema>;
export type PlatformAccountInput = Infer<typeof platformAccountInputSchema>;
export type PlatformAccountHandoverInput = Infer<typeof platformAccountHandoverInputSchema>;
export type PlatformAccountHandover = Infer<typeof platformAccountHandoverSchema>;

/** Ledger filters; each is optional and they combine with AND. */
export interface PlatformAccountFilters {
  q?: string;
  roleId?: number;
  mailboxId?: number;
  ownerUserId?: number;
  status?: PlatformAccountStatus;
}
