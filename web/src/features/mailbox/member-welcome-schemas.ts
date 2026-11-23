import {
  array,
  boolean,
  enum as enumSchema,
  int,
  nonnegative,
  nullable,
  number,
  strictObject,
  string,
  type infer as Infer,
} from 'zod/mini';

import { safeIdSchema, timestampSchema } from '@/features/mailbox/schemas';

/** Limits from `contracts/MAILBOXES.md`, section Member Welcome Mail. */
export const MEMBER_WELCOME_SUBJECT_MAX_LENGTH = 200;
export const MEMBER_WELCOME_BODY_MAX_LENGTH = 5000;

export const memberWelcomeLanguageSchema = enumSchema(['zh-Hans', 'en-US']);
export type MemberWelcomeLanguage = Infer<typeof memberWelcomeLanguageSchema>;

export const memberWelcomeVariableSchema = enumSchema([
  'member_name',
  'mailbox_address',
  'organization_name',
  'sign_in_url',
]);
export type MemberWelcomeVariable = Infer<typeof memberWelcomeVariableSchema>;

const memberWelcomeCopySchema = strictObject({ subject: string(), body: string() });
export type MemberWelcomeCopy = Infer<typeof memberWelcomeCopySchema>;

export const memberWelcomeSettingSchema = strictObject({
  enabled: boolean(),
  language: memberWelcomeLanguageSchema,
  subject: string(),
  body: string(),
  customized: boolean(),
  variables: array(memberWelcomeVariableSchema),
  defaults: strictObject({ 'en-US': memberWelcomeCopySchema, 'zh-Hans': memberWelcomeCopySchema }),
  updated_at: nullable(timestampSchema),
});
export type MemberWelcomeSetting = Infer<typeof memberWelcomeSettingSchema>;

export const memberWelcomePreviewSchema = strictObject({
  subject: string(),
  text: string(),
  html: string(),
});
export type MemberWelcomePreview = Infer<typeof memberWelcomePreviewSchema>;

export interface UpdateMemberWelcomeSettingInput {
  enabled: boolean;
  language: MemberWelcomeLanguage;
  subject: string;
  body: string;
}

export interface MemberWelcomePreviewInput {
  language: MemberWelcomeLanguage;
  subject: string;
  body: string;
}

/** The welcome mail outcome of one roster row. */
export const memberWelcomeMailStatusSchema = enumSchema([
  'queued',
  'suppressed',
  'already_sent',
  'disabled',
  'not_configured',
  'failed',
]);
export type MemberWelcomeMailStatus = Infer<typeof memberWelcomeMailStatusSchema>;

/** What became of a mailbox's latest welcome mail (`contracts/MAILBOXES.md`, Welcome Mail Status). */
export const memberWelcomeDeliveryStatusSchema = enumSchema([
  'not_sent',
  'queued',
  'sent',
  'failed',
  'suppressed',
  'expired',
]);
export type MemberWelcomeDeliveryStatus = Infer<typeof memberWelcomeDeliveryStatusSchema>;

/** A mailbox that can be welcomed, with its latest welcome mail; never a recipient address. */
export const memberWelcomeMailSchema = strictObject({
  mailbox_id: safeIdSchema,
  address: string(),
  display_name: string(),
  owner_user_id: nullable(safeIdSchema),
  invitation_pending: boolean(),
  status: memberWelcomeDeliveryStatusSchema,
  failure_code: nullable(string()),
  queued_at: nullable(timestampSchema),
  sent_at: nullable(timestampSchema),
  resend_count: number().check(int(), nonnegative()),
});
export type MemberWelcomeMail = Infer<typeof memberWelcomeMailSchema>;

export const memberWelcomeMailListSchema = array(memberWelcomeMailSchema);
