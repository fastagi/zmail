import {
  array,
  boolean,
  enum as enumSchema,
  int,
  literal,
  maximum,
  minimum,
  nullable,
  number,
  regex,
  strictObject,
  string,
  type infer as Infer,
} from 'zod/mini';

import { safeIdSchema, timestampSchema } from '@/features/mailbox/schemas';

/** Limits from `contracts/MAILBOXES.md`, section Mail Client Access. */
export const MAIL_APP_PASSWORD_NAME_MAX_LENGTH = 64;

export const mailClientProtocolSchema = enumSchema(['imap', 'smtp']);
export type MailClientProtocol = Infer<typeof mailClientProtocolSchema>;

/** A mail client password as listed: never its secret, only the last four letters. */
export const mailAppPasswordSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  scopes: array(mailClientProtocolSchema),
  hint: string().check(regex(/^[a-z]{4}$/u)),
  created_at: timestampSchema,
  last_used_at: nullable(timestampSchema),
  last_used_protocol: nullable(mailClientProtocolSchema),
});
export type MailAppPassword = Infer<typeof mailAppPasswordSchema>;

/** The creation response, the only one that carries the secret. */
export const mailAppPasswordCreatedSchema = strictObject({
  ...mailAppPasswordSchema.shape,
  secret: string().check(regex(/^[a-z]{4}( [a-z]{4}){3}$/u)),
});
export type MailAppPasswordCreated = Infer<typeof mailAppPasswordCreatedSchema>;

export const mailClientAccessReasonSchema = enumSchema([
  'unavailable',
  'organization_disabled',
  'feature_not_granted',
]);
export type MailClientAccessReason = Infer<typeof mailClientAccessReasonSchema>;

export const mailAppPasswordListSchema = strictObject({
  items: array(mailAppPasswordSchema),
  limit: number().check(int(), minimum(1)),
  access: strictObject({
    enabled: boolean(),
    reason: nullable(mailClientAccessReasonSchema),
  }),
  server: strictObject({
    imap_host: string(),
    imap_port: number().check(int(), minimum(1), maximum(65535)),
    imap_security: literal('ssl_tls'),
    smtp_host: string(),
    smtp_port: number().check(int(), minimum(1), maximum(65535)),
    smtp_security: literal('ssl_tls'),
    smtp_starttls_port: number().check(int(), minimum(1), maximum(65535)),
  }),
});
export type MailAppPasswordList = Infer<typeof mailAppPasswordListSchema>;

export const mailClientSettingsSchema = strictObject({
  access_enabled: boolean(),
  updated_at: nullable(timestampSchema),
});
export type MailClientSettings = Infer<typeof mailClientSettingsSchema>;

/** The body of `POST /v1/mail-app-passwords`; the credentials are never stored by the console. */
export interface CreateMailAppPasswordInput {
  name: string;
  scopes: MailClientProtocol[];
  current_password: string;
  totp_code?: string;
}
