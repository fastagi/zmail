import { MAIL_DNS_RECORD_ID_PATTERN, mailMessageSchema } from '@/features/mailbox/schemas';
import type { MailMessage } from '@/features/mailbox/types';
import { ClientErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';

export function parseMailMessage(value: unknown, expectedId?: number): MailMessage {
  const parsed = mailMessageSchema.safeParse(value);
  if (!parsed.success || (expectedId !== undefined && parsed.data.id !== expectedId)) {
    throw invalidResponse();
  }
  return parsed.data;
}

export function parseOrganizeItem<T extends { id: number }>(
  schema: { safeParse: (value: unknown) => { success: true; data: T } | { success: false } },
  value: unknown,
  expectedId?: number
): T {
  const parsed = schema.safeParse(value);
  if (!parsed.success || (expectedId !== undefined && parsed.data.id !== expectedId)) {
    throw invalidResponse();
  }
  return parsed.data;
}

/** A Cloudflare record ID; anything else never leaves the browser. */
export function validRecordId(value: string): void {
  if (!MAIL_DNS_RECORD_ID_PATTERN.test(value)) throw invalidResponse();
}

/** The organization the request acts in; an invalid id never leaves the browser. */
export function organizationHeaders(organizationId: number): Record<string, string> {
  validId(organizationId);
  return { 'Organization-Id': String(organizationId) };
}

export function validId(value: number): void {
  if (!Number.isSafeInteger(value) || value < 1) throw invalidResponse();
}

export function invalidResponse(): ApiError {
  return new ApiError(
    'Mailbox service returned an invalid response',
    ClientErrorCode.INVALID_RESPONSE
  );
}
