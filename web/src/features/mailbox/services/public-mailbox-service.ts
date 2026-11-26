import {
  array,
  boolean,
  literal,
  nullable,
  object,
  optional,
  string,
  union,
  type z,
} from 'zod/mini';

import {
  MAILBOX_LOCAL_PART_MAX_LENGTH,
  mailboxSchema,
  safeIdSchema,
} from '@/features/mailbox/schemas';
import { nonnegativeIntegerSchema } from '@/features/mailbox/schema-modules/shared-schemas';
import type { Mailbox } from '@/features/mailbox/types';
import request from '@/http/request';

import { invalidResponse, organizationHeaders } from './mailbox-service-shared';

export const publicMailboxAvailabilityStatusSchema = union([
  literal('available'),
  literal('taken'),
  literal('reserved'),
  literal('invalid'),
  literal('too_short'),
]);

export const publicMailboxOptionsSchema = object({
  enabled: boolean(),
  domains: array(string()),
  min_local_length: nonnegativeIntegerSchema,
  limit_per_user: nonnegativeIntegerSchema,
  claimed_count: nonnegativeIntegerSchema,
  claimed_address: nullable(string()),
  claimed_organization_id: nullable(safeIdSchema),
  claimed_mailbox_id: nullable(safeIdSchema),
  daily_send_limit_new: nonnegativeIntegerSchema,
  daily_send_limit: nonnegativeIntegerSchema,
  max_recipients_new: nonnegativeIntegerSchema,
  max_recipients: nonnegativeIntegerSchema,
});

export const publicMailboxAvailabilitySchema = object({
  address: string(),
  local_part: string(),
  domain: string(),
  status: publicMailboxAvailabilityStatusSchema,
  suggestions: array(string()),
});

export const claimPublicMailboxInputSchema = object({
  local_part: string(),
  domain: optional(string()),
  display_name: optional(string()),
});

export type PublicMailboxAvailabilityStatus = z.infer<
  typeof publicMailboxAvailabilityStatusSchema
>;
export type PublicMailboxOptions = z.infer<typeof publicMailboxOptionsSchema>;
export type PublicMailboxAvailability = z.infer<typeof publicMailboxAvailabilitySchema>;
export type ClaimPublicMailboxInput = z.infer<typeof claimPublicMailboxInputSchema>;

/**
 * Derives a clean default handle suggestion from the signed-in user's email prefix,
 * username, or display name, stripped to `[a-z0-9._-]` without consecutive/edge punctuation.
 */
export function suggestHandleFromUser(
  user: { email?: string | null; username?: string | null; name?: string | null } | null
): string {
  if (!user) return '';
  const candidates = [
    user.email ? user.email.split('@')[0] : '',
    user.username ?? '',
    user.name ?? '',
  ];
  for (const raw of candidates) {
    const cleaned = sanitizeLocalPartInput(raw);
    if (cleaned.length > 0) return cleaned;
  }
  return '';
}

/** Strips invalid characters and edge punctuation to produce a clean local-part candidate. */
export function sanitizeLocalPartInput(raw: string): string {
  return raw
    .trim()
    .toLowerCase()
    .replace(/\s+/gu, '.')
    .replace(/[^a-z0-9._-]+/gu, '')
    .replace(/\.{2,}/gu, '.')
    .replace(/^[._-]+|[._-]+$/gu, '')
    .slice(0, MAILBOX_LOCAL_PART_MAX_LENGTH);
}

/** Extracts the local part when an availability suggestion is either `alice01` or `alice01@zmail.im`. */
export function extractSuggestedLocalPart(suggestion: string): string {
  const trimmed = suggestion.trim().toLowerCase();
  const atIndex = trimmed.indexOf('@');
  return atIndex > 0 ? trimmed.slice(0, atIndex) : trimmed;
}

export const publicMailboxService = {
  async options(): Promise<PublicMailboxOptions> {
    const value = await request.get<unknown>('/public-mailbox/options');
    const parsed = publicMailboxOptionsSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async availability(localPart: string, domain?: string): Promise<PublicMailboxAvailability> {
    const params: Record<string, string> = { local_part: localPart };
    if (domain) params.domain = domain;
    const value = await request.get<unknown>('/public-mailbox/availability', { params });
    const parsed = publicMailboxAvailabilitySchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },

  async claim(organizationId: number, input: ClaimPublicMailboxInput): Promise<Mailbox> {
    const value = await request.post<unknown, ClaimPublicMailboxInput>(
      '/public-mailbox/claim',
      input,
      { headers: organizationHeaders(organizationId) }
    );
    const parsed = mailboxSchema.safeParse(value);
    if (!parsed.success) throw invalidResponse();
    return parsed.data;
  },
};
