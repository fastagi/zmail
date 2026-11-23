import {
  array,
  boolean,
  enum as enumSchema,
  nullable,
  strictObject,
  string,
  type infer as Infer,
} from 'zod/mini';

import { timestampSchema } from '@/features/mailbox/schemas';

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
