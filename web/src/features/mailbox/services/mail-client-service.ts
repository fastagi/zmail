import {
  mailAppPasswordCreatedSchema,
  mailAppPasswordListSchema,
  mailClientSettingsSchema,
  type CreateMailAppPasswordInput,
  type MailAppPasswordCreated,
  type MailAppPasswordList,
  type MailClientSettings,
} from '@/features/mailbox/mail-client-schemas';
import { ClientErrorCode } from '@/http/codes';
import request, { ApiError } from '@/http/request';

/**
 * Browser service for mail client access in `contracts/MAILBOXES.md` (section Mail Client
 * Access): the caller's own mail client passwords and the organization switch.
 */
export const mailClientService = {
  /** The caller's own passwords, whether mail apps can sign in, and the server to use. */
  async list(organizationId: number): Promise<MailAppPasswordList> {
    return parse(
      mailAppPasswordListSchema,
      await request.get<unknown>('/mail-app-passwords', { headers: organizationHeaders(organizationId) })
    );
  },

  /** Creates a password; the response is the only place its secret ever appears. */
  async create(
    organizationId: number,
    input: CreateMailAppPasswordInput
  ): Promise<MailAppPasswordCreated> {
    return parse(
      mailAppPasswordCreatedSchema,
      await request.post<unknown, CreateMailAppPasswordInput>('/mail-app-passwords', input, {
        headers: organizationHeaders(organizationId),
      })
    );
  },

  /** Revokes one of the caller's own passwords at once. */
  async revoke(organizationId: number, passwordId: number): Promise<void> {
    validId(passwordId);
    await request.delete(`/mail-app-passwords/${passwordId}`, {
      headers: organizationHeaders(organizationId),
    });
  },

  /** The organization switch; every member may read it. */
  async settings(organizationId: number): Promise<MailClientSettings> {
    return parse(
      mailClientSettingsSchema,
      await request.get<unknown>('/mail-client-settings', {
        headers: organizationHeaders(organizationId),
      })
    );
  },

  /** Turns mail app sign-in on or off for the organization; owners and admins only. */
  async updateSettings(organizationId: number, accessEnabled: boolean): Promise<MailClientSettings> {
    return parse(
      mailClientSettingsSchema,
      await request.put<unknown, { access_enabled: boolean }>(
        '/mail-client-settings',
        { access_enabled: accessEnabled },
        { headers: organizationHeaders(organizationId) }
      )
    );
  },
};

function parse<T>(schema: { safeParse: (value: unknown) => { success: true; data: T } | { success: false } }, value: unknown): T {
  const parsed = schema.safeParse(value);
  if (!parsed.success) throw invalidResponse();
  return parsed.data;
}

function organizationHeaders(organizationId: number): Record<string, string> {
  validId(organizationId);
  return { 'Organization-Id': String(organizationId) };
}

function validId(value: number): void {
  if (!Number.isSafeInteger(value) || value < 1) throw invalidResponse();
}

function invalidResponse(): ApiError {
  return new ApiError(
    'Mail client service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}
