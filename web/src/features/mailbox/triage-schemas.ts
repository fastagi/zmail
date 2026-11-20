import {
  array,
  boolean,
  int,
  literal,
  maxLength,
  maximum,
  minLength,
  minimum,
  nonnegative,
  nullable,
  number,
  strictObject,
  string,
  union,
  type infer as Infer,
} from 'zod/mini';

import { safeIdSchema, timestampSchema } from '@/features/mailbox/schemas';

/** Limits from `contracts/MAILBOXES.md`, section AI Triage. */
export const MAIL_TRIAGE_DAILY_CAP_MAX = 100_000;
export const MAIL_REPLY_DRAFT_MAX_LENGTH = 20_000;
/** A thread's last messages and the template names a draft may name as its basis. */
export const MAIL_REPLY_DRAFT_MAX_BASIS = 60;
/** The categories a reader may choose; `review` is the model's alone. */
export const MAIL_TRIAGE_OVERRIDE_CATEGORIES = ['reply', 'deadline', 'fyi'] as const;

/** The organization's AI triage switches; `available` is false when the instance has no model. */
export const mailTriageSettingSchema = strictObject({
  available: boolean(),
  enabled: boolean(),
  daily_message_cap: number().check(int(), minimum(1), maximum(MAIL_TRIAGE_DAILY_CAP_MAX)),
  draft_enabled: boolean(),
  instance_daily_cap: number().check(int(), nonnegative()),
  updated_at: nullable(timestampSchema),
});

/** What a reply draft relied on: a thread message or a template, labeled by subject or name. */
export const mailReplyDraftBasisSchema = strictObject({
  kind: union([literal('message'), literal('template')]),
  id: safeIdSchema,
  label: string(),
});

/** A generated reply draft; it is returned once and never stored. */
export const mailReplyDraftSchema = strictObject({
  body: string().check(minLength(1), maxLength(MAIL_REPLY_DRAFT_MAX_LENGTH)),
  basis: array(mailReplyDraftBasisSchema).check(maxLength(MAIL_REPLY_DRAFT_MAX_BASIS)),
});

export type MailTriageSetting = Infer<typeof mailTriageSettingSchema>;
export type MailReplyDraft = Infer<typeof mailReplyDraftSchema>;
export type MailReplyDraftBasis = Infer<typeof mailReplyDraftBasisSchema>;
export type MailTriageOverrideCategory = (typeof MAIL_TRIAGE_OVERRIDE_CATEGORIES)[number];

export interface MailTriageSettingInput {
  enabled: boolean;
  daily_message_cap: number;
  draft_enabled: boolean;
}

export interface MailTriageOverrideInput {
  category: MailTriageOverrideCategory;
  due_at?: string | null;
}
